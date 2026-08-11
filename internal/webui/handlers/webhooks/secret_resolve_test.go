package webhooks

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	connectorsmodule "github.com/tysonthomas9/loomcli/internal/modules/connectors"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// uniform401Body is the single body every inbound verification failure
// returns (writeJSON's encoder appends the newline).
const uniform401Body = `{"error":"webhook signature verification failed"}` + "\n"

func seedConnector(t *testing.T, st store.Store, id string, kind connectorsmodule.ConnectorSourceKind, secret string, status connectorsmodule.ConnectorStatus) {
	t.Helper()
	if _, err := st.Connectors().CreateConnectorRecord(context.Background(), connectorsmodule.CreateConnectorMutation{
		WorkspaceKey: testWS, ConnectorID: id, SourceKind: kind, InboundSecret: secret, Status: status,
	}); err != nil {
		t.Fatalf("seed connector %s: %v", id, err)
	}
}

// signedRequestWith signs a github webhook request with an arbitrary secret
// (signedRequest in module_test.go is pinned to the binding secret).
func signedRequestWith(secret, delivery string, body []byte) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/api/workspaces/"+testWS+"/webhooks/github", bytes.NewReader(body))
	r.Header.Set(githubEventHeader, "pull_request")
	r.Header.Set(githubDeliveryHeader, delivery)
	r.Header.Set(githubSignatureHeader, githubSignature(secret, body))
	return r
}

// captureLogs redirects the default slog logger into a buffer for the test's
// duration so stale-secret audit signals can be asserted.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// TestWebhookConnectorSecretAuthority proves Connectors is the sole inbound
// verification root. Missing, unrelated, and disabled connectors all fail
// closed.
func TestWebhookConnectorSecretAuthority(t *testing.T) {
	const connSecret = "connector-inbound-secret"
	tests := []struct {
		name       string
		seed       func(t *testing.T, st store.Store)
		signSecret string
		wantStatus int
		wantCalls  int
	}{
		{
			name: "connector secret verifies",
			seed: func(t *testing.T, st store.Store) {
				seedConnector(t, st, "gh-main", connectorsmodule.ConnectorSourceGitHub, connSecret, "")
			},
			signSecret: connSecret,
			wantStatus: http.StatusAccepted,
			wantCalls:  1,
		},
		{
			name: "wrong secret rejected when connector exists",
			seed: func(t *testing.T, st store.Store) {
				seedConnector(t, st, "gh-main", connectorsmodule.ConnectorSourceGitHub, connSecret, "")
			},
			signSecret: testSecret,
			wantStatus: http.StatusUnauthorized,
			wantCalls:  0,
		},
		{
			name:       "no connector fails closed",
			seed:       func(*testing.T, store.Store) {},
			signSecret: testSecret,
			wantStatus: http.StatusUnauthorized,
			wantCalls:  0,
		},
		{
			name: "connector for another source fails closed",
			seed: func(t *testing.T, st store.Store) {
				seedConnector(t, st, "slack-main", connectorsmodule.ConnectorSourceSlack, "slack-secret", "")
			},
			signSecret: testSecret,
			wantStatus: http.StatusUnauthorized,
			wantCalls:  0,
		},
		{
			name: "disabled connector fails closed",
			seed: func(t *testing.T, st store.Store) {
				seedConnector(t, st, "gh-off", connectorsmodule.ConnectorSourceGitHub, connSecret, connectorsmodule.ConnectorStatusDisabled)
			},
			signSecret: testSecret,
			wantStatus: http.StatusUnauthorized,
			wantCalls:  0,
		},
		{
			name: "any active connector for the source kind verifies",
			seed: func(t *testing.T, st store.Store) {
				seedConnector(t, st, "gh-a", connectorsmodule.ConnectorSourceGitHub, "secret-a", "")
				seedConnector(t, st, "gh-b", connectorsmodule.ConnectorSourceGitHub, "secret-b", "")
			},
			signSecret: "secret-b",
			wantStatus: http.StatusAccepted,
			wantCalls:  1,
		},
		{
			name: "tampered signature 401 with connector",
			seed: func(t *testing.T, st store.Store) {
				seedConnector(t, st, "gh-main", connectorsmodule.ConnectorSourceGitHub, connSecret, "")
			},
			signSecret: "not-the-secret",
			wantStatus: http.StatusUnauthorized,
			wantCalls:  0,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := seedStoreWithoutConnector(t, true)
			tc.seed(t, st)
			admission := &testAdmission{}
			mux := newServerWithPorts(st, admission, testAutomationQueries{})

			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, signedRequestWith(tc.signSecret, "delivery-1", prOpenedBody))
			if rr.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d, body %s", rr.Code, tc.wantStatus, rr.Body.String())
			}
			if tc.wantStatus == http.StatusUnauthorized && rr.Body.String() != uniform401Body {
				t.Errorf("401 body = %q, want uniform %q", rr.Body.String(), uniform401Body)
			}
			// Verification failures must never cross Automation's admission
			// port; successful verification crosses it exactly once.
			if calls := len(admission.commands); calls != tc.wantCalls {
				t.Fatalf("admission calls = %d, want %d", calls, tc.wantCalls)
			}
		})
	}
}

// TestWebhookRotationWindow pins the dual-secret rotation behavior: the
// previous secret verifies inside the window (with a stale-secret audit
// signal) and is rejected once the window closes.
func TestWebhookRotationWindow(t *testing.T) {
	rotate := func(t *testing.T, st store.Store, validUntil time.Time) {
		t.Helper()
		if _, err := st.Connectors().RotateConnectorSecretsRecord(context.Background(), testWS, "gh-main", connectorsmodule.RotateConnectorSecretsMutation{
			NewInboundSecret: "new-secret", PreviousSecretValidUntil: validUntil,
		}); err != nil {
			t.Fatalf("rotate: %v", err)
		}
	}

	t.Run("new secret verifies after rotation", func(t *testing.T) {
		st := seedStoreWithoutConnector(t, true)
		seedConnector(t, st, "gh-main", connectorsmodule.ConnectorSourceGitHub, "old-secret", "")
		rotate(t, st, time.Time{}) // default window: now + 15m
		rr := httptest.NewRecorder()
		newServer(st).ServeHTTP(rr, signedRequestWith("new-secret", "d-new", prOpenedBody))
		if rr.Code != http.StatusAccepted {
			t.Fatalf("status = %d, want 202, body %s", rr.Code, rr.Body.String())
		}
	})

	t.Run("previous secret accepted inside window with stale audit signal", func(t *testing.T) {
		st := seedStoreWithoutConnector(t, true)
		seedConnector(t, st, "gh-main", connectorsmodule.ConnectorSourceGitHub, "old-secret", "")
		rotate(t, st, time.Time{})
		logs := captureLogs(t)
		rr := httptest.NewRecorder()
		newServer(st).ServeHTTP(rr, signedRequestWith("old-secret", "d-old", prOpenedBody))
		if rr.Code != http.StatusAccepted {
			t.Fatalf("status = %d, want 202, body %s", rr.Code, rr.Body.String())
		}
		if out := logs.String(); !strings.Contains(out, "connector_stale_inbound_secret") ||
			!strings.Contains(out, "gh-main") {
			t.Errorf("stale-secret audit signal not emitted, logs: %s", out)
		}
	})

	t.Run("current secret match emits no stale signal", func(t *testing.T) {
		st := seedStoreWithoutConnector(t, true)
		seedConnector(t, st, "gh-main", connectorsmodule.ConnectorSourceGitHub, "old-secret", "")
		rotate(t, st, time.Time{})
		logs := captureLogs(t)
		rr := httptest.NewRecorder()
		newServer(st).ServeHTTP(rr, signedRequestWith("new-secret", "d-new-2", prOpenedBody))
		if rr.Code != http.StatusAccepted {
			t.Fatalf("status = %d, want 202", rr.Code)
		}
		if strings.Contains(logs.String(), "connector_stale_inbound_secret") {
			t.Errorf("unexpected stale signal for current-secret match, logs: %s", logs.String())
		}
	})

	t.Run("previous secret rejected after window", func(t *testing.T) {
		st := seedStoreWithoutConnector(t, true)
		seedConnector(t, st, "gh-main", connectorsmodule.ConnectorSourceGitHub, "old-secret", "")
		rotate(t, st, time.Now().UTC().Add(-time.Second)) // already expired
		rr := httptest.NewRecorder()
		newServer(st).ServeHTTP(rr, signedRequestWith("old-secret", "d-expired", prOpenedBody))
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401, body %s", rr.Code, rr.Body.String())
		}
		if rr.Body.String() != uniform401Body {
			t.Errorf("401 body = %q, want uniform %q", rr.Body.String(), uniform401Body)
		}
		events, _ := st.TriggerEvents().List(context.Background(), testWS, store.TriggerEventFilter{})
		if len(events) != 0 {
			t.Fatalf("expired-secret request persisted %d trigger events", len(events))
		}
	})
}

// connectorOverrideStore swaps the connector store while keeping memstore's
// binding reads real.
type connectorOverrideStore struct {
	store.Store
	conns connectorsmodule.ManagementStore
}

func (s connectorOverrideStore) Connectors() connectorsmodule.ManagementStore { return s.conns }

// stubConnectorStore overrides List/ResolveInboundSecret on the fail-closed
// placeholder so resolver edge cases (errors, contract anomalies) can be
// driven directly.
type stubConnectorStore struct {
	connectorsmodule.ManagementStore
	conns      []*connectorsmodule.Connector
	listErr    error
	secrets    map[string]*connectorsmodule.InboundSecrets
	resolveErr map[string]error
}

func (s *stubConnectorStore) ListConnectorRecords(context.Context, string, connectorsmodule.ConnectorFilter) ([]*connectorsmodule.Connector, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.conns, nil
}

func (s *stubConnectorStore) ResolveInboundSecretsRecord(_ context.Context, _, connectorID string) (*connectorsmodule.InboundSecrets, error) {
	if err := s.resolveErr[connectorID]; err != nil {
		return nil, err
	}
	secrets, ok := s.secrets[connectorID]
	if !ok {
		return nil, fmt.Errorf("connector %q: %w", connectorID, connectorsmodule.ErrNotFound)
	}
	return secrets, nil
}

func ghConnector(id string) *connectorsmodule.Connector {
	return &connectorsmodule.Connector{
		WorkspaceKey: testWS, ConnectorID: id,
		SourceKind: connectorsmodule.ConnectorSourceGitHub, Status: connectorsmodule.ConnectorStatusActive,
	}
}

// TestConnectorSecretCandidates unit-tests fail-closed edge cases and the
// verifier-side rotation-window re-check.
func TestConnectorSecretCandidates(t *testing.T) {
	now := time.Now().UTC()
	errBoom := errors.New("fleet-db unreachable")
	tests := []struct {
		name           string
		conns          connectorSecretSource
		wantSecrets    []string
		wantStaleCount int
		wantErr        error
	}{
		{
			name:        "nil connector store has no candidates",
			conns:       nil,
			wantSecrets: []string{},
		},
		{
			name:        "no connectors has no candidates",
			conns:       &stubConnectorStore{},
			wantSecrets: []string{},
		},
		{
			name:    "list error propagates",
			conns:   &stubConnectorStore{listErr: errBoom},
			wantErr: errBoom,
		},
		{
			name: "resolve error propagates",
			conns: &stubConnectorStore{
				conns:      []*connectorsmodule.Connector{ghConnector("gh-a")},
				resolveErr: map[string]error{"gh-a": errBoom},
			},
			wantErr: errBoom,
		},
		{
			name: "connector deleted between list and resolve has no candidates",
			conns: &stubConnectorStore{
				conns: []*connectorsmodule.Connector{ghConnector("gh-gone")},
			},
			wantSecrets: []string{},
		},
		{
			name: "connector with empty secrets fails closed without fallback",
			conns: &stubConnectorStore{
				conns:   []*connectorsmodule.Connector{ghConnector("gh-a")},
				secrets: map[string]*connectorsmodule.InboundSecrets{"gh-a": {}},
			},
			wantSecrets: []string{},
		},
		{
			name: "previous secret inside window is a stale candidate",
			conns: &stubConnectorStore{
				conns: []*connectorsmodule.Connector{ghConnector("gh-a")},
				secrets: map[string]*connectorsmodule.InboundSecrets{
					"gh-a": {Current: "cur", Previous: "prev", PreviousValidUntil: now.Add(time.Minute)},
				},
			},
			wantSecrets:    []string{"cur", "prev"},
			wantStaleCount: 1,
		},
		{
			name: "previous secret past window dropped despite store anomaly",
			conns: &stubConnectorStore{
				conns: []*connectorsmodule.Connector{ghConnector("gh-a")},
				secrets: map[string]*connectorsmodule.InboundSecrets{
					"gh-a": {Current: "cur", Previous: "prev", PreviousValidUntil: now.Add(-time.Minute)},
				},
			},
			wantSecrets: []string{"cur"},
		},
		{
			name: "previous secret with zero window dropped (fail closed)",
			conns: &stubConnectorStore{
				conns: []*connectorsmodule.Connector{ghConnector("gh-a")},
				secrets: map[string]*connectorsmodule.InboundSecrets{
					"gh-a": {Current: "cur", Previous: "prev"},
				},
			},
			wantSecrets: []string{"cur"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			verifier := NewVerifier(VerifierConfig{Connectors: tc.conns})
			got, err := verifier.connectorSecretCandidates(context.Background(), testWS, "github", now)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("connectorSecretCandidates: %v", err)
			}
			secrets := make([]string, 0, len(got))
			stale := 0
			for _, c := range got {
				secrets = append(secrets, c.secret)
				if c.stale {
					stale++
				}
			}
			if len(secrets) != len(tc.wantSecrets) {
				t.Fatalf("candidates = %v, want %v", secrets, tc.wantSecrets)
			}
			for i := range secrets {
				if secrets[i] != tc.wantSecrets[i] {
					t.Fatalf("candidates = %v, want %v", secrets, tc.wantSecrets)
				}
			}
			if stale != tc.wantStaleCount {
				t.Errorf("stale candidates = %d, want %d", stale, tc.wantStaleCount)
			}
		})
	}
}

// TestConnectorSecretResolutionFailureIsUniform401 pins the no-oracle rule at
// the handler level: a secret-resolution infrastructure failure returns the
// same 401 body as a bad signature and persists nothing.
func TestConnectorSecretResolutionFailureIsUniform401(t *testing.T) {
	logs := captureLogs(t)
	st := seedStoreWithoutConnector(t, true)
	mux := newServer(connectorOverrideStore{Store: st, conns: &stubConnectorStore{listErr: errors.New("fleet-db unreachable")}})

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, signedRequest("github", "d-resolve-fail", prOpenedBody))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401, body %s", rr.Code, rr.Body.String())
	}
	if rr.Body.String() != uniform401Body {
		t.Errorf("401 body = %q, want uniform %q", rr.Body.String(), uniform401Body)
	}
	if !strings.Contains(logs.String(), "webhook inbound secret resolution failed") {
		t.Errorf("resolution failure not logged server-side, logs: %s", logs.String())
	}
	events, _ := st.TriggerEvents().List(context.Background(), testWS, store.TriggerEventFilter{})
	if len(events) != 0 {
		t.Fatalf("resolution failure persisted %d trigger events", len(events))
	}
}

func TestWebhookNilResolvedConnectorSecretFailsClosed(t *testing.T) {
	st := seedStoreWithoutConnector(t, true)
	connectors := &stubConnectorStore{
		conns:   []*connectorsmodule.Connector{ghConnector("gh-nil")},
		secrets: map[string]*connectorsmodule.InboundSecrets{"gh-nil": nil},
	}
	mux := newServer(connectorOverrideStore{Store: st, conns: connectors})

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, signedRequest("github", "d-nil-secret", prOpenedBody))
	if recorder.Code != http.StatusUnauthorized || recorder.Body.String() != uniform401Body {
		t.Fatalf("nil secret result = %d %q, want uniform 401", recorder.Code, recorder.Body.String())
	}
	events, err := st.TriggerEvents().List(t.Context(), testWS, store.TriggerEventFilter{})
	if err != nil || len(events) != 0 {
		t.Fatalf("nil secret result persisted events = %d, %v", len(events), err)
	}
}

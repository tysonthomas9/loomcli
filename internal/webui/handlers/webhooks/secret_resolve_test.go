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

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// uniform401Body is the single body every inbound verification failure
// returns (writeJSON's encoder appends the newline).
const uniform401Body = `{"error":"webhook signature verification failed"}` + "\n"

func seedConnector(t *testing.T, st store.Store, id string, kind domain.ConnectorSourceKind, secret string, status domain.ConnectorStatus) {
	t.Helper()
	if _, err := st.Connectors().Create(context.Background(), store.ConnectorCreate{
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

// TestWebhookConnectorSecretMigration covers the verification-root migration:
// once an active connector exists for the binding's source kind it is the
// only verification root; without one, the per-binding secret keeps working.
func TestWebhookConnectorSecretMigration(t *testing.T) {
	const connSecret = "connector-inbound-secret"
	tests := []struct {
		name       string
		seed       func(t *testing.T, st store.Store)
		signSecret string
		wantStatus int
		wantEvents int
	}{
		{
			name: "connector secret verifies",
			seed: func(t *testing.T, st store.Store) {
				seedConnector(t, st, "gh-main", domain.ConnectorSourceGitHub, connSecret, "")
			},
			signSecret: connSecret,
			wantStatus: http.StatusAccepted,
			wantEvents: 1,
		},
		{
			name: "binding secret rejected once connector exists",
			seed: func(t *testing.T, st store.Store) {
				seedConnector(t, st, "gh-main", domain.ConnectorSourceGitHub, connSecret, "")
			},
			signSecret: testSecret,
			wantStatus: http.StatusUnauthorized,
			wantEvents: 0,
		},
		{
			name:       "binding secret fallback when no connector",
			seed:       func(*testing.T, store.Store) {},
			signSecret: testSecret,
			wantStatus: http.StatusAccepted,
			wantEvents: 1,
		},
		{
			name: "binding secret fallback when connector is for another source",
			seed: func(t *testing.T, st store.Store) {
				seedConnector(t, st, "slack-main", domain.ConnectorSourceSlack, "slack-secret", "")
			},
			signSecret: testSecret,
			wantStatus: http.StatusAccepted,
			wantEvents: 1,
		},
		{
			name: "disabled connector does not become the verification root",
			seed: func(t *testing.T, st store.Store) {
				seedConnector(t, st, "gh-off", domain.ConnectorSourceGitHub, connSecret, domain.ConnectorStatusDisabled)
			},
			signSecret: testSecret,
			wantStatus: http.StatusAccepted,
			wantEvents: 1,
		},
		{
			name: "any active connector for the source kind verifies",
			seed: func(t *testing.T, st store.Store) {
				seedConnector(t, st, "gh-a", domain.ConnectorSourceGitHub, "secret-a", "")
				seedConnector(t, st, "gh-b", domain.ConnectorSourceGitHub, "secret-b", "")
			},
			signSecret: "secret-b",
			wantStatus: http.StatusAccepted,
			wantEvents: 1,
		},
		{
			name: "tampered signature 401 with connector",
			seed: func(t *testing.T, st store.Store) {
				seedConnector(t, st, "gh-main", domain.ConnectorSourceGitHub, connSecret, "")
			},
			signSecret: "not-the-secret",
			wantStatus: http.StatusUnauthorized,
			wantEvents: 0,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := seedStore(t, true)
			tc.seed(t, st)
			mux := newServer(st)

			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, signedRequestWith(tc.signSecret, "delivery-1", prOpenedBody))
			if rr.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d, body %s", rr.Code, tc.wantStatus, rr.Body.String())
			}
			if tc.wantStatus == http.StatusUnauthorized && rr.Body.String() != uniform401Body {
				t.Errorf("401 body = %q, want uniform %q", rr.Body.String(), uniform401Body)
			}
			// Auth failures must never persist a TriggerEvent (existing e2e
			// invariant preserved on the connector path).
			events, _ := st.TriggerEvents().List(context.Background(), testWS, store.TriggerEventFilter{})
			if len(events) != tc.wantEvents {
				t.Fatalf("trigger events = %d, want %d", len(events), tc.wantEvents)
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
		if _, err := st.Connectors().RotateSecrets(context.Background(), testWS, "gh-main", store.ConnectorSecretRotation{
			NewInboundSecret: "new-secret", PreviousSecretValidUntil: validUntil,
		}); err != nil {
			t.Fatalf("rotate: %v", err)
		}
	}

	t.Run("new secret verifies after rotation", func(t *testing.T) {
		st := seedStore(t, true)
		seedConnector(t, st, "gh-main", domain.ConnectorSourceGitHub, "old-secret", "")
		rotate(t, st, time.Time{}) // default window: now + 15m
		rr := httptest.NewRecorder()
		newServer(st).ServeHTTP(rr, signedRequestWith("new-secret", "d-new", prOpenedBody))
		if rr.Code != http.StatusAccepted {
			t.Fatalf("status = %d, want 202, body %s", rr.Code, rr.Body.String())
		}
	})

	t.Run("previous secret accepted inside window with stale audit signal", func(t *testing.T) {
		st := seedStore(t, true)
		seedConnector(t, st, "gh-main", domain.ConnectorSourceGitHub, "old-secret", "")
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
		st := seedStore(t, true)
		seedConnector(t, st, "gh-main", domain.ConnectorSourceGitHub, "old-secret", "")
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
		st := seedStore(t, true)
		seedConnector(t, st, "gh-main", domain.ConnectorSourceGitHub, "old-secret", "")
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
// binding/secret lookups real.
type connectorOverrideStore struct {
	store.Store
	conns store.ConnectorStore
}

func (s connectorOverrideStore) Connectors() store.ConnectorStore { return s.conns }

// stubConnectorStore overrides List/ResolveInboundSecret on the fail-closed
// placeholder so resolver edge cases (errors, contract anomalies) can be
// driven directly.
type stubConnectorStore struct {
	store.UnimplementedConnectorStore
	conns      []*domain.Connector
	listErr    error
	secrets    map[string]*store.ConnectorInboundSecrets
	resolveErr map[string]error
}

func (s *stubConnectorStore) List(context.Context, string, store.ConnectorFilter) ([]*domain.Connector, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.conns, nil
}

func (s *stubConnectorStore) ResolveInboundSecret(_ context.Context, _, connectorID string) (*store.ConnectorInboundSecrets, error) {
	if err := s.resolveErr[connectorID]; err != nil {
		return nil, err
	}
	secrets, ok := s.secrets[connectorID]
	if !ok {
		return nil, fmt.Errorf("connector %q: %w", connectorID, domain.ErrConnectorNotFound)
	}
	return secrets, nil
}

func ghConnector(id string) *domain.Connector {
	return &domain.Connector{
		WorkspaceKey: testWS, ConnectorID: id,
		SourceKind: domain.ConnectorSourceGitHub, Status: domain.ConnectorStatusActive,
	}
}

// TestResolveInboundSecretCandidates unit-tests the resolver's edge cases
// against a stubbed connector store: fallback triggers, fail-closed branches,
// and the verifier-side rotation-window re-check.
func TestResolveInboundSecretCandidates(t *testing.T) {
	now := time.Now().UTC()
	binding := &domain.TriggerBinding{
		WorkspaceKey: testWS, BindingID: "b1", SourceKind: "github", Enabled: true,
	}
	errBoom := errors.New("fleet-db unreachable")
	tests := []struct {
		name           string
		conns          store.ConnectorStore
		wantSecrets    []string
		wantStaleCount int
		wantErr        error
	}{
		{
			name:        "nil connector store falls back to binding secret",
			conns:       nil,
			wantSecrets: []string{testSecret},
		},
		{
			name:        "unsupported connector store falls back to binding secret",
			conns:       store.UnimplementedConnectorStore{Backend: "test"},
			wantSecrets: []string{testSecret},
		},
		{
			name:        "no connectors falls back to binding secret",
			conns:       &stubConnectorStore{},
			wantSecrets: []string{testSecret},
		},
		{
			name:    "list error propagates",
			conns:   &stubConnectorStore{listErr: errBoom},
			wantErr: errBoom,
		},
		{
			name: "resolve error propagates",
			conns: &stubConnectorStore{
				conns:      []*domain.Connector{ghConnector("gh-a")},
				resolveErr: map[string]error{"gh-a": errBoom},
			},
			wantErr: errBoom,
		},
		{
			name: "connector deleted between list and resolve falls back",
			conns: &stubConnectorStore{
				conns: []*domain.Connector{ghConnector("gh-gone")},
			},
			wantSecrets: []string{testSecret},
		},
		{
			name: "connector with empty secrets fails closed without fallback",
			conns: &stubConnectorStore{
				conns:   []*domain.Connector{ghConnector("gh-a")},
				secrets: map[string]*store.ConnectorInboundSecrets{"gh-a": {}},
			},
			wantSecrets: []string{},
		},
		{
			name: "previous secret inside window is a stale candidate",
			conns: &stubConnectorStore{
				conns: []*domain.Connector{ghConnector("gh-a")},
				secrets: map[string]*store.ConnectorInboundSecrets{
					"gh-a": {Current: "cur", Previous: "prev", PreviousValidUntil: now.Add(time.Minute)},
				},
			},
			wantSecrets:    []string{"cur", "prev"},
			wantStaleCount: 1,
		},
		{
			name: "previous secret past window dropped despite store anomaly",
			conns: &stubConnectorStore{
				conns: []*domain.Connector{ghConnector("gh-a")},
				secrets: map[string]*store.ConnectorInboundSecrets{
					"gh-a": {Current: "cur", Previous: "prev", PreviousValidUntil: now.Add(-time.Minute)},
				},
			},
			wantSecrets: []string{"cur"},
		},
		{
			name: "previous secret with zero window dropped (fail closed)",
			conns: &stubConnectorStore{
				conns: []*domain.Connector{ghConnector("gh-a")},
				secrets: map[string]*store.ConnectorInboundSecrets{
					"gh-a": {Current: "cur", Previous: "prev"},
				},
			},
			wantSecrets: []string{"cur"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := seedStore(t, true)
			verifier := NewCompatibilityVerifier(CompatibilityVerifierConfig{
				Bindings: st.TriggerBindings(), Connectors: tc.conns,
			})
			got, err := verifier.resolveInboundSecretCandidates(context.Background(), testWS, binding, now)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveInboundSecretCandidates: %v", err)
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

// TestWebhookSecretResolutionFailureIsUniform401 pins the no-oracle rule at
// the handler level: a secret-resolution infrastructure failure returns the
// same 401 body as a bad signature and persists nothing.
func TestWebhookSecretResolutionFailureIsUniform401(t *testing.T) {
	logs := captureLogs(t)
	st := seedStore(t, true)
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
	st := seedStore(t, true)
	connectors := &stubConnectorStore{
		conns:   []*domain.Connector{ghConnector("gh-nil")},
		secrets: map[string]*store.ConnectorInboundSecrets{"gh-nil": nil},
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

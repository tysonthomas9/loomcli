package serve

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	infrafleetdb "github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	interactionfleetdb "github.com/tysonthomas9/loomcli/internal/modules/interaction/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

type interactionServeRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn interactionServeRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestInteractionFleetDBOwnedCommandConsumesCredentialOnce(t *testing.T) {
	now := time.Now().UTC()
	var calls int
	client, err := infrafleetdb.New(infrafleetdb.Config{
		BaseURL: "http://fleet.invalid",
		HTTPClient: &http.Client{Transport: interactionServeRoundTripFunc(
			func(request *http.Request) (*http.Response, error) {
				calls++
				if request.Method != http.MethodPost ||
					request.URL.Path != "/api/v1/WS/interaction/sessions/session-1/heartbeat" {
					t.Fatalf("request = %s %s", request.Method, request.URL.Path)
				}
				if got := request.Header.Get("X-Agent-Lease-Token"); got != "raw-session-token" {
					t.Fatalf("lease credential header = %q", got)
				}
				body, err := io.ReadAll(request.Body)
				if err != nil {
					t.Fatal(err)
				}
				if bytes.Contains(body, []byte("raw-session-token")) {
					t.Fatalf("raw credential entered request body: %s", body)
				}
				payload, err := json.Marshal(infrafleetdb.InteractionSessionMutationResult{
					Session: &domain.AgentSession{
						WorkspaceKey: "WS", SessionID: "session-1",
						AgentID: "agent-1", NodeID: "node-1",
						Kind:   domain.AgentSessionKind("interactive"),
						Status: domain.AgentSessionRunning,
					},
					Lease: &domain.AgentLease{
						WorkspaceKey: "WS", LeaseID: "lease-1",
						SessionID: "session-1", AgentID: "agent-1",
						NodeID: "node-1", FencingToken: 7,
						Status:    domain.AgentLeaseActive,
						ExpiresAt: now.Add(time.Minute),
					},
				})
				if err != nil {
					t.Fatal(err)
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(bytes.NewReader(payload)),
				}, nil
			},
		)},
	})
	if err != nil {
		t.Fatal(err)
	}
	issuer := authority.NewIssuer()
	principal, err := issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject: "session:session-1", Class: authority.ClassSession,
		Workspace: "WS", Actions: []authority.Action{interaction.ActionHeartbeatSession},
		ExpiresAt: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte("raw-session-token")
	sessionAuthority, err := issuer.IssueSessionForOwnerWithCredential(
		principal,
		"WS",
		interaction.ActionHeartbeatSession,
		authority.SessionOwner{
			SessionID: "session-1", AgentID: "agent-1", NodeID: "node-1",
			LeaseID: "lease-1", FencingToken: 7,
		},
		raw,
	)
	clear(raw)
	if err != nil {
		t.Fatal(err)
	}
	transport := newInteractionFleetDBAuthorityTransport(client)
	session, lease, err := transport.HeartbeatSessionOwned(
		t.Context(),
		"WS",
		sessionAuthority.SessionOwner(),
		interaction.SessionHeartbeat{At: now, LeaseTTL: time.Minute},
	)
	if err != nil {
		t.Fatal(err)
	}
	if session == nil || lease == nil || calls != 1 {
		t.Fatalf("session = %+v, lease = %+v, calls = %d", session, lease, calls)
	}
	_, _, err = transport.HeartbeatSessionOwned(
		t.Context(),
		"WS",
		sessionAuthority.SessionOwner(),
		interaction.SessionHeartbeat{At: now, LeaseTTL: time.Minute},
	)
	if !errors.Is(err, interactionfleetdb.ErrTransportInvalid) ||
		!strings.Contains(err.Error(), "one-use lease credential") {
		t.Fatalf("replay error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("credential replay reached FleetDB; calls = %d", calls)
	}
}

func TestInteractionFleetDBForceInterruptPropagatesExpectedLeaseGeneration(t *testing.T) {
	var captured map[string]any
	client, err := infrafleetdb.New(infrafleetdb.Config{
		BaseURL: "http://fleet.invalid",
		HTTPClient: &http.Client{Transport: interactionServeRoundTripFunc(
			func(request *http.Request) (*http.Response, error) {
				if request.Method != http.MethodPost ||
					request.URL.Path != "/api/v1/WS/interaction/sessions/session-1/force-interrupt" {
					t.Fatalf("request = %s %s", request.Method, request.URL.Path)
				}
				if err := json.NewDecoder(request.Body).Decode(&captured); err != nil {
					t.Fatal(err)
				}
				payload, err := json.Marshal(
					infrafleetdb.InteractionSessionForceInterruptResult{},
				)
				if err != nil {
					t.Fatal(err)
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(bytes.NewReader(payload)),
				}, nil
			},
		)},
	})
	if err != nil {
		t.Fatal(err)
	}
	transport := newInteractionFleetDBAuthorityTransport(client)
	if _, err := transport.ForceInterrupt(
		t.Context(),
		interaction.ForceInterruptCommand{
			WorkspaceKey: "WS", SessionID: "session-1", AgentID: "agent-1",
			TerminalID: "terminal-1", ExpectedLeaseID: "lease-7",
			ExpectedLeaseFencingToken: 7, StreamRef: "terminal:WS/tab-1",
			TerminalTab: "tab-1", Reason: "operator stop",
		},
	); err != nil {
		t.Fatal(err)
	}
	if captured["expected_lease_id"] != "lease-7" ||
		captured["expected_lease_fencing_token"] != float64(7) {
		t.Fatalf("force interrupt body = %#v", captured)
	}
}

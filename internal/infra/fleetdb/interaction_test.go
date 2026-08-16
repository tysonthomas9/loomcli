package fleetdb

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
)

type interactionRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn interactionRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

type validateInteractionSessionAuthorityRequest struct {
	SessionID    string `json:"session_id"`
	AgentID      string `json:"agent_id"`
	NodeID       string `json:"node_id"`
	LeaseID      string `json:"lease_id"`
	FencingToken int64  `json:"fencing_token"`
}

type validateInteractionSessionAuthorityResponse struct {
	Lease *interaction.LeaseRecord `json:"lease"`
}

func TestInteractionValidateSessionAuthorityUsesHeaderCredentialAndDurableIdentity(t *testing.T) {
	expiresAt := time.Now().Add(time.Minute).UTC()
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		calls++
		switch calls {
		case 1:
			if request.Method != http.MethodPost ||
				request.URL.Path != "/api/v1/WS/agent-session-authority/validate" {
				t.Fatalf("validation request = %s %s", request.Method, request.URL.Path)
			}
			if got := request.Header.Get("X-Agent-Lease-Token"); got != "raw-session-token" {
				t.Fatalf("lease token header = %q", got)
			}
			body, err := io.ReadAll(request.Body)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(body), "raw-session-token") {
				t.Fatalf("raw token entered JSON body: %s", body)
			}
			var got validateInteractionSessionAuthorityRequest
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatal(err)
			}
			if got.SessionID != "session-1" || got.AgentID != "agent-1" ||
				got.NodeID != "node-1" || got.LeaseID != "lease-1" ||
				got.FencingToken != 7 {
				t.Fatalf("validation body = %+v", got)
			}
			_ = json.NewEncoder(response).Encode(validateInteractionSessionAuthorityResponse{
				Lease: &interaction.LeaseRecord{
					WorkspaceKey: "WS", SessionID: "session-1", AgentID: "agent-1",
					NodeID: "node-1", LeaseID: "lease-1", FencingToken: 7,
					Status: interaction.LeaseRecordActive, ExpiresAt: expiresAt,
				},
			})
		case 2:
			if request.Method != http.MethodGet ||
				request.URL.Path != "/api/v1/WS/agent-sessions/session-1" {
				t.Fatalf("session request = %s %s", request.Method, request.URL.Path)
			}
			_ = json.NewEncoder(response).Encode(&interaction.SessionRecord{
				WorkspaceKey: "WS", SessionID: "session-1", AgentID: "agent-1",
				NodeID: "node-1", TerminalID: "terminal-1",
				Status: interaction.SessionRecordRunning,
			})
		default:
			t.Fatalf("unexpected call %d", calls)
		}
	}))
	defer server.Close()

	client, err := New(Config{BaseURL: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	value, err := client.Interaction().ValidateSessionAuthority(
		t.Context(),
		InteractionSessionAuthorityProof{
			WorkspaceKey: "WS", SessionID: "session-1", AgentID: "agent-1",
			TerminalID: "terminal-1", NodeID: "node-1", LeaseID: "lease-1",
			LeaseToken: "raw-session-token", FencingToken: 7,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if value == nil || value.TerminalID != "terminal-1" ||
		value.FencingToken != 7 || !value.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("validation = %+v", value)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}

func TestInteractionValidateSessionAuthorityMapsForbiddenWithoutLeakingCredential(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.WriteHeader(http.StatusForbidden)
		_, _ = response.Write([]byte(`{"error":{"code":"forbidden","message":"session authority rejected"}}`))
	}))
	defer server.Close()
	client, err := New(Config{BaseURL: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Interaction().ValidateSessionAuthority(
		t.Context(),
		InteractionSessionAuthorityProof{
			WorkspaceKey: "WS", SessionID: "session-1", AgentID: "agent-1",
			NodeID: "node-1", LeaseID: "lease-1",
			LeaseToken: "do-not-leak", FencingToken: 7,
		},
	)
	if !errors.Is(err, ErrInteractionNotOwner) {
		t.Fatalf("error = %v, want ErrInteractionNotOwner", err)
	}
	if strings.Contains(err.Error(), "do-not-leak") {
		t.Fatalf("error leaked credential: %v", err)
	}
}

func TestInteractionValidateSessionAuthorityRejectsCrossTerminalProjection(t *testing.T) {
	expiresAt := time.Now().Add(time.Minute).UTC()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if strings.HasSuffix(request.URL.Path, "/validate") {
			_ = json.NewEncoder(response).Encode(validateInteractionSessionAuthorityResponse{
				Lease: &interaction.LeaseRecord{
					WorkspaceKey: "WS", SessionID: "session-1", AgentID: "agent-1",
					NodeID: "node-1", LeaseID: "lease-1", FencingToken: 7,
					Status: interaction.LeaseRecordActive, ExpiresAt: expiresAt,
				},
			})
			return
		}
		_ = json.NewEncoder(response).Encode(&interaction.SessionRecord{
			WorkspaceKey: "WS", SessionID: "session-1", AgentID: "agent-1",
			NodeID: "node-1", TerminalID: "terminal-2",
			Status: interaction.SessionRecordRunning,
		})
	}))
	defer server.Close()
	client, err := New(Config{BaseURL: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Interaction().ValidateSessionAuthority(
		t.Context(),
		InteractionSessionAuthorityProof{
			WorkspaceKey: "WS", SessionID: "session-1", AgentID: "agent-1",
			TerminalID: "terminal-1", NodeID: "node-1", LeaseID: "lease-1",
			LeaseToken: "token", FencingToken: 7,
		},
	)
	if !errors.Is(err, ErrInteractionInvalidPersistedState) {
		t.Fatalf("error = %v, want invalid persisted state", err)
	}
}

func TestInteractionValidateSessionAuthorityRejectsInvalidProofBeforeRequest(t *testing.T) {
	client, err := New(Config{
		BaseURL: "http://unused.invalid",
		HTTPClient: &http.Client{Transport: interactionRoundTripFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("invalid proof reached HTTP")
			return nil, nil
		})},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Interaction().ValidateSessionAuthority(
		t.Context(),
		InteractionSessionAuthorityProof{
			WorkspaceKey: "WS", SessionID: "session-1", AgentID: "agent-1",
			NodeID: "node-1", LeaseID: "lease-1", FencingToken: 7,
		},
	)
	if !errors.Is(err, ErrInteractionInvalid) {
		t.Fatalf("error = %v, want ErrInteractionInvalid", err)
	}
}

package fleetdb

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// fleet-db #322 made POST /agent-inbox-messages/{id}/complete claimer-only:
// the body must carry claimed_by, and it must equal the session holding the
// live claim (api/openapi.yaml CompleteAgentInboxMessageRequest, required
// whenever the message is claimed). Omitting it is a 403.
func TestAgentInboxCompleteSendsClaimedBy(t *testing.T) {
	tests := []struct {
		name   string
		update store.AgentInboxMessageComplete
	}{
		{
			name: "delivered",
			update: store.AgentInboxMessageComplete{
				Outcome:           "delivered",
				DeliveredThreadID: "thread-9",
				ClaimedBy:         "codex:lead-session:thread-9",
			},
		},
		{
			name: "retry",
			update: store.AgentInboxMessageComplete{
				Outcome:    "retry",
				ErrorClass: "codex_delivery_pending",
				Error:      "runtime busy",
				ClaimedBy:  "codex:lead-session:thread-9",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var body map[string]any
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/api/v1/WS/agent-inbox-messages/msg-1/complete" {
					t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatalf("decode complete body: %v", err)
				}
				writeJSON(t, w, domain.AgentInboxMessage{WorkspaceKey: "WS", InboxMessageID: "msg-1"})
			}))
			defer ts.Close()

			client, err := New(Config{BaseURL: ts.URL, Actor: "tester"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.AgentInboxMessages().Complete(t.Context(), "WS", "msg-1", tt.update); err != nil {
				t.Fatalf("complete: %v", err)
			}
			if got, _ := body["claimed_by"].(string); got != tt.update.ClaimedBy {
				t.Fatalf("complete body claimed_by = %q, want %q (body = %v)", got, tt.update.ClaimedBy, body)
			}
			if got, _ := body["outcome"].(string); got != tt.update.Outcome {
				t.Fatalf("complete body outcome = %q, want %q", got, tt.update.Outcome)
			}
		})
	}
}

// ClaimNext must surface the server-assigned claimed_by so the completion can
// echo the value the claim actually recorded rather than re-deriving it.
func TestAgentInboxClaimNextReturnsClaimedBy(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/WS/agent-inbox-messages/claim-next" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode claim body: %v", err)
		}
		if got, _ := req["claimed_by"].(string); got != "codex:lead-session" {
			t.Fatalf("claim body claimed_by = %q", got)
		}
		if got, _ := req["lease_ttl_ms"].(float64); got != 120000 {
			t.Fatalf("claim body lease_ttl_ms = %v", req["lease_ttl_ms"])
		}
		writeJSON(t, w, domain.AgentInboxMessage{
			WorkspaceKey:   "WS",
			InboxMessageID: "msg-1",
			ClaimedBy:      "codex:lead-session",
		})
	}))
	defer ts.Close()

	client, err := New(Config{BaseURL: ts.URL, Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	msg, err := client.AgentInboxMessages().ClaimNext(t.Context(), store.AgentInboxMessageClaim{
		WorkspaceKey:  "WS",
		TargetAgentID: "nova",
		SessionID:     "lead-session",
		ClaimedBy:     "codex:lead-session",
		LeaseTTL:      2 * time.Minute,
	})
	if err != nil {
		t.Fatalf("claim next: %v", err)
	}
	if msg.ClaimedBy != "codex:lead-session" {
		t.Fatalf("claimed message ClaimedBy = %q", msg.ClaimedBy)
	}
}

// fleet-db returns 403 when claimed_by does not match the live claim holder
// (or is absent), and 410 while the stored claim is expired and nobody has
// re-claimed. Both must reach the caller as a recognisable, typed refusal —
// not the generic ErrConflict that hides the reason.
func TestAgentInboxCompleteClaimRefusalsAreTyped(t *testing.T) {
	tests := []struct {
		name   string
		status int
		code   string
		want   error
	}{
		{name: "not the claim holder", status: http.StatusForbidden, code: "forbidden", want: domain.ErrNotOwner},
		{name: "claim expired", status: http.StatusGone, code: "lease_expired", want: domain.ErrGone},
		{name: "already completed", status: http.StatusConflict, code: "invalid_transition", want: domain.ErrInvalidTransition},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.status)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"error": map[string]any{"code": tt.code, "message": "complete agent inbox message failed"},
				})
			}))
			defer ts.Close()

			client, err := New(Config{BaseURL: ts.URL, Actor: "tester"})
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.AgentInboxMessages().Complete(t.Context(), "WS", "msg-1", store.AgentInboxMessageComplete{
				Outcome:   "delivered",
				ClaimedBy: "codex:stale-session",
			})
			if !errors.Is(err, tt.want) {
				t.Fatalf("complete err = %v, want %v", err, tt.want)
			}
		})
	}
}

package fleetdb

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
)

func TestInteractionStartUsesAtomicCommandAndReturnsOneTimeToken(t *testing.T) {
	var captured map[string]any
	client := newInteractionCommandTestClient(t, func(request *http.Request) *http.Response {
		if request.Method != http.MethodPost ||
			request.URL.Path != "/api/v1/WS/interaction/sessions" {
			t.Fatalf("request = %s %s", request.Method, request.URL.Path)
		}
		if token := request.Header.Get("X-Agent-Lease-Token"); token != "" {
			t.Fatalf("start sent lease token header %q", token)
		}
		if err := json.NewDecoder(request.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		return interactionCommandJSONResponse(t, http.StatusCreated, InteractionSessionStartResult{
			Session: &interaction.SessionRecord{
				WorkspaceKey: "WS", SessionID: "session-1", AgentID: "agent-1",
				NodeID: "node-1", Kind: interaction.SessionRecordKind("interactive"),
				Status: interaction.SessionRecordStarting,
			},
			Lease: &interaction.LeaseRecord{
				WorkspaceKey: "WS", LeaseID: "lease-1", SessionID: "session-1",
				AgentID: "agent-1", NodeID: "node-1", FencingToken: 1,
				Status: interaction.LeaseRecordActive, ExpiresAt: time.Now().Add(time.Minute),
			},
			Token: "raw-session-token",
		})
	})

	result, err := client.Interaction().StartInteractionSession(
		t.Context(),
		InteractionSessionStartInput{
			WorkspaceKey: "WS", SessionID: "session-1", AgentID: "agent-1",
			NodeID: "node-1", Kind: "interactive", LeaseID: "lease-1",
			LeaseTTL: 1500 * time.Millisecond,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.Session == nil || result.Lease == nil ||
		result.Token != "raw-session-token" {
		t.Fatalf("result = %+v", result)
	}
	if captured["lease_ttl_seconds"] != float64(2) {
		t.Fatalf("lease_ttl_seconds = %#v", captured["lease_ttl_seconds"])
	}
}

func TestInteractionRecoverStartUsesExactGenerationAndNoCredentialReceipt(t *testing.T) {
	var body []byte
	client := newInteractionCommandTestClient(t, func(request *http.Request) *http.Response {
		if request.Method != http.MethodPost ||
			request.URL.Path != "/api/v1/WS/interaction/sessions/session-1/recover-start" {
			t.Fatalf("request = %s %s", request.Method, request.URL.Path)
		}
		var err error
		body, err = io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(body, []byte("raw-replacement-token")) ||
			bytes.Contains(body, []byte("token_hash")) ||
			bytes.Contains(body, []byte(`"at"`)) {
			t.Fatalf("recovery request leaked credential or caller time: %s", body)
		}
		return interactionCommandJSONResponse(t, http.StatusOK, InteractionSessionStartResult{
			Session: &interaction.SessionRecord{
				WorkspaceKey: "WS", SessionID: "session-1", AgentID: "agent-1",
				NodeID: "node-1", Kind: interaction.SessionRecordKind("interactive"),
				Status:         interaction.SessionRecordStarting,
				CurrentLeaseID: "lease-2", CurrentLeaseFencingToken: 8,
			},
			Lease: &interaction.LeaseRecord{
				WorkspaceKey: "WS", LeaseID: "lease-2", SessionID: "session-1",
				AgentID: "agent-1", NodeID: "node-1", FencingToken: 8,
				Status: interaction.LeaseRecordActive, ExpiresAt: time.Now().Add(time.Minute),
			},
			Token: "raw-replacement-token",
		})
	})
	result, err := client.Interaction().RecoverInteractionSessionStart(
		t.Context(),
		InteractionSessionStartRecoveryInput{
			Original: InteractionSessionStartInput{
				WorkspaceKey: "WS", SessionID: "session-1", AgentID: "agent-1",
				NodeID: "node-1", Kind: "interactive", TaskID: "TASK-1",
				TerminalID: "terminal-1", Phase: "starting", Attempt: 1,
				LeaseID: "lease-1", LeaseTTL: time.Minute,
				Metadata: map[string]string{"intent": "stable"},
			},
			ExpectedLeaseID: "lease-1", ExpectedLeaseFencingToken: 7,
			ReplacementLeaseID: "lease-2", ReplacementLeaseTTL: 90 * time.Second,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.Token != "raw-replacement-token" ||
		result.Lease == nil || result.Lease.LeaseID != "lease-2" {
		t.Fatalf("result = %+v", result)
	}
	var captured map[string]any
	if err := json.Unmarshal(body, &captured); err != nil {
		t.Fatal(err)
	}
	if captured["expected_lease_id"] != "lease-1" ||
		captured["expected_lease_fencing_token"] != float64(7) ||
		captured["replacement_lease_id"] != "lease-2" ||
		captured["replacement_lease_ttl_seconds"] != float64(90) {
		t.Fatalf("recovery body = %#v", captured)
	}
}

func TestInteractionForceInterruptCarriesExpectedLeaseGenerationAndFailsClosed(t *testing.T) {
	var (
		calls    int
		captured map[string]any
	)
	client := newInteractionCommandTestClient(t, func(request *http.Request) *http.Response {
		calls++
		if request.Method != http.MethodPost ||
			request.URL.Path != "/api/v1/WS/interaction/sessions/session-1/force-interrupt" {
			t.Fatalf("request = %s %s", request.Method, request.URL.Path)
		}
		if token := request.Header.Get("X-Agent-Lease-Token"); token != "" {
			t.Fatalf("force interrupt sent lease credential header %q", token)
		}
		if err := json.NewDecoder(request.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		return interactionCommandJSONResponse(
			t,
			http.StatusOK,
			InteractionSessionForceInterruptResult{},
		)
	})
	input := InteractionSessionForceInterruptInput{
		WorkspaceKey: "WS", SessionID: "session-1", AgentID: "agent-1",
		TerminalID: "terminal-1", ExpectedLeaseID: "lease-7",
		ExpectedLeaseFencingToken: 7, StreamRef: "terminal:WS/tab-1",
		TerminalTab: "tab-1", Reason: "operator stop",
	}
	if _, err := client.Interaction().ForceInterruptInteractionSession(
		t.Context(),
		input,
	); err != nil {
		t.Fatal(err)
	}
	if calls != 1 ||
		captured["expected_lease_id"] != "lease-7" ||
		captured["expected_lease_fencing_token"] != float64(7) ||
		captured["agent_id"] != "agent-1" ||
		captured["terminal_id"] != "terminal-1" {
		t.Fatalf("force interrupt request calls=%d body=%#v", calls, captured)
	}
	if _, ok := captured["lease_token"]; ok {
		t.Fatalf("force interrupt request exposed lease credential: %#v", captured)
	}

	missing := input
	missing.ExpectedLeaseID = ""
	missing.ExpectedLeaseFencingToken = 0
	if _, err := client.Interaction().ForceInterruptInteractionSession(
		t.Context(),
		missing,
	); !errors.Is(err, ErrInteractionInvalid) {
		t.Fatalf("missing expected generation error = %v, want invalid", err)
	}
	if calls != 1 {
		t.Fatalf("missing expected generation reached FleetDB; calls=%d", calls)
	}
}

func TestInteractionOwnedHeartbeatUsesHeaderOnlyCredential(t *testing.T) {
	var calls int
	client := newInteractionCommandTestClient(t, func(request *http.Request) *http.Response {
		calls++
		if request.Method != http.MethodPost ||
			request.URL.Path != "/api/v1/WS/interaction/sessions/session-1/heartbeat" {
			t.Fatalf("request = %s %s", request.Method, request.URL.Path)
		}
		if token := request.Header.Get("X-Agent-Lease-Token"); token != "raw-session-token" {
			t.Fatalf("lease token header = %q", token)
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(body, []byte("raw-session-token")) {
			t.Fatalf("raw token entered JSON body: %s", body)
		}
		if bytes.Contains(body, []byte(`"at"`)) {
			t.Fatalf("heartbeat sent caller-controlled time: %s", body)
		}
		return interactionCommandJSONResponse(t, http.StatusOK, InteractionSessionMutationResult{
			Session: &interaction.SessionRecord{
				WorkspaceKey: "WS", SessionID: "session-1", AgentID: "agent-1",
				NodeID: "node-1", Kind: interaction.SessionRecordKind("interactive"),
				Status: interaction.SessionRecordRunning,
			},
			Lease: &interaction.LeaseRecord{
				WorkspaceKey: "WS", LeaseID: "lease-1", SessionID: "session-1",
				AgentID: "agent-1", NodeID: "node-1", FencingToken: 7,
				Status: interaction.LeaseRecordActive, ExpiresAt: time.Now().Add(time.Minute),
			},
		})
	})
	proof := InteractionSessionAuthorityProof{
		WorkspaceKey: "WS", SessionID: "session-1", AgentID: "agent-1",
		NodeID: "node-1", LeaseID: "lease-1", LeaseToken: "raw-session-token",
		FencingToken: 7,
	}
	result, err := client.Interaction().HeartbeatInteractionSession(
		t.Context(),
		InteractionSessionHeartbeatInput{
			Proof: proof, LeaseTTL: time.Minute,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.Session == nil || result.Lease == nil || calls != 1 {
		t.Fatalf("result = %+v calls=%d", result, calls)
	}
}

func TestInteractionInboxCompletionBindsExactClaimAttempt(t *testing.T) {
	var captured map[string]any
	client := newInteractionCommandTestClient(t, func(request *http.Request) *http.Response {
		if request.Method != http.MethodPost ||
			request.URL.Path != "/api/v1/WS/interaction/inbox/message-1/complete" {
			t.Fatalf("request = %s %s", request.Method, request.URL.Path)
		}
		if token := request.Header.Get("X-Agent-Lease-Token"); token != "raw-session-token" {
			t.Fatalf("lease token header = %q", token)
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(body, []byte("raw-session-token")) {
			t.Fatalf("raw token entered JSON body: %s", body)
		}
		if err := json.Unmarshal(body, &captured); err != nil {
			t.Fatal(err)
		}
		return interactionCommandJSONResponse(t, http.StatusOK, &interaction.InboxRecord{
			WorkspaceKey: "WS", InboxMessageID: "message-1",
			SessionID: "session-1", Attempt: 3, Status: interaction.InboxRecordQueued,
		})
	})
	proof := InteractionSessionAuthorityProof{
		WorkspaceKey: "WS", SessionID: "session-1", AgentID: "agent-1",
		NodeID: "node-1", LeaseID: "lease-1", LeaseToken: "raw-session-token",
		FencingToken: 7,
	}
	message, err := client.Interaction().CompleteInteractionInbox(
		t.Context(),
		InteractionInboxCompleteInput{
			Proof: proof, InboxMessageID: "message-1", Attempt: 3,
			Status: string(interaction.InboxRecordQueued), ErrorClass: "delivery_pending",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if message == nil || message.Attempt != 3 || message.Status != interaction.InboxRecordQueued {
		t.Fatalf("message = %+v", message)
	}
	if captured["attempt"] != float64(3) || captured["status"] != "queued" {
		t.Fatalf("completion payload = %#v", captured)
	}
}

func TestInteractionCommandMapsStableConflictSentinels(t *testing.T) {
	for name, testCase := range map[string]struct {
		code string
		want error
	}{
		"already exists":     {code: "already_exists", want: ErrInteractionConflict},
		"invalid transition": {code: "invalid_transition", want: ErrInteractionInvalidTransition},
	} {
		t.Run(name, func(t *testing.T) {
			client := newInteractionCommandTestClient(t, func(*http.Request) *http.Response {
				return interactionCommandJSONResponse(
					t,
					http.StatusConflict,
					map[string]any{"error": map[string]string{
						"code": testCase.code, "message": name,
					}},
				)
			})
			_, err := client.Interaction().StartInteractionSession(
				t.Context(),
				InteractionSessionStartInput{
					WorkspaceKey: "WS", SessionID: "session-1", AgentID: "agent-1",
					NodeID: "node-1", Kind: "interactive", LeaseID: "lease-1",
					LeaseTTL: time.Minute,
				},
			)
			if err == nil || !strings.Contains(err.Error(), name) || !errors.Is(err, testCase.want) {
				t.Fatalf("error = %v, want %v", err, testCase.want)
			}
		})
	}
}

func TestInteractionCommandsNeverSendCallerControlledLifecycleTime(t *testing.T) {
	var calls int
	client := newInteractionCommandTestClient(t, func(request *http.Request) *http.Response {
		calls++
		body := []byte(nil)
		if request.Body != nil {
			var err error
			body, err = io.ReadAll(request.Body)
			if err != nil {
				t.Fatal(err)
			}
		}
		switch calls {
		case 1:
			if request.Method != http.MethodPost ||
				request.URL.Path != "/api/v1/WS/interaction/sessions/session-1/finish" {
				t.Fatalf("finish request = %s %s", request.Method, request.URL.Path)
			}
			if bytes.Contains(body, []byte("finished_at")) {
				t.Fatalf("finish sent caller-controlled time: %s", body)
			}
			return interactionCommandJSONResponse(t, http.StatusOK, InteractionSessionMutationResult{})
		case 2:
			if request.Method != http.MethodGet ||
				request.URL.Path != "/api/v1/WS/interaction/sessions/recoverable" ||
				request.URL.RawQuery != "" {
				t.Fatalf("recoverable request = %s %s?%s", request.Method, request.URL.Path, request.URL.RawQuery)
			}
			return interactionCommandJSONResponse(t, http.StatusOK, map[string]any{
				"agent_sessions": []any{}, "count": 0,
			})
		case 3:
			if request.Method != http.MethodPost ||
				request.URL.Path != "/api/v1/WS/interaction/sessions/session-1/interrupt-if-lease-missing" {
				t.Fatalf("interrupt request = %s %s", request.Method, request.URL.Path)
			}
			if strings.TrimSpace(string(body)) != "" {
				t.Fatalf("interrupt sent caller-controlled body: %s", body)
			}
			return interactionCommandJSONResponse(t, http.StatusOK, InteractionSessionInterruptResult{})
		case 4:
			if request.Method != http.MethodPatch ||
				request.URL.Path != "/api/v1/WS/interaction/terminals/terminal-1" {
				t.Fatalf("terminal request = %s %s", request.Method, request.URL.Path)
			}
			if bytes.Contains(body, []byte("last_seen_at")) {
				t.Fatalf("terminal update sent caller-controlled time: %s", body)
			}
			return interactionCommandJSONResponse(t, http.StatusOK, &interaction.TerminalRecord{
				WorkspaceKey: "WS", TerminalID: "terminal-1",
				SessionID: "session-1", AgentID: "agent-1", NodeID: "node-1",
			})
		default:
			t.Fatalf("unexpected request %d", calls)
			return nil
		}
	})
	proof := InteractionSessionAuthorityProof{
		WorkspaceKey: "WS", SessionID: "session-1", AgentID: "agent-1",
		NodeID: "node-1", LeaseID: "lease-1", LeaseToken: "raw-session-token",
		FencingToken: 7,
	}
	if _, err := client.Interaction().FinishInteractionSession(
		t.Context(),
		InteractionSessionFinishInput{Proof: proof, Status: "completed"},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Interaction().ListRecoverableInteractionSessions(t.Context(), "WS"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Interaction().InterruptInteractionSessionIfLeaseMissing(
		t.Context(),
		"WS",
		"session-1",
	); err != nil {
		t.Fatal(err)
	}
	terminalProof := proof
	terminalProof.TerminalID = "terminal-1"
	if _, err := client.Interaction().UpdateInteractionTerminal(
		t.Context(),
		InteractionTerminalUpdateInput{
			Proof: terminalProof, TerminalID: "terminal-1", Status: "running",
		},
	); err != nil {
		t.Fatal(err)
	}
	if calls != 4 {
		t.Fatalf("calls = %d, want 4", calls)
	}
}

func TestInteractionActivityUsesCanonicalGlobalMergeRoute(t *testing.T) {
	client := newInteractionCommandTestClient(t, func(request *http.Request) *http.Response {
		if request.Method != http.MethodGet ||
			request.URL.Path != "/api/v1/WS/interaction/activity" ||
			request.URL.Query().Get("agent_id") != "agent-1" ||
			request.URL.Query().Get("limit") != "7" {
			t.Fatalf("request = %s %s?%s", request.Method, request.URL.Path, request.URL.RawQuery)
		}
		return interactionCommandJSONResponse(t, http.StatusOK, map[string]any{
			"activity": []InteractionActivity{
				{
					WorkspaceKey: "WS", AgentID: "agent-1",
					Kind: "agent_session", SourceID: "session-1", TaskID: "TASK-1",
				},
				{
					WorkspaceKey: "WS", AgentID: "agent-1",
					Kind: "batch_run", SourceID: "run-1", TaskID: "TASK-2",
				},
			},
			"count": 2,
		})
	})
	values, err := client.Interaction().ListInteractionActivity(
		t.Context(),
		"WS",
		"agent-1",
		7,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 2 || values[0].TaskID != "TASK-1" || values[1].Kind != "batch_run" {
		t.Fatalf("activity = %+v", values)
	}
}

func newInteractionCommandTestClient(
	t *testing.T,
	respond func(*http.Request) *http.Response,
) *Client {
	t.Helper()
	client, err := New(Config{
		BaseURL: "http://fleet.invalid",
		HTTPClient: &http.Client{Transport: interactionRoundTripFunc(
			func(request *http.Request) (*http.Response, error) {
				return respond(request), nil
			},
		)},
	})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func interactionCommandJSONResponse(t *testing.T, status int, value any) *http.Response {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(body)),
	}
}

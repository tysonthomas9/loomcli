package leadclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type leadHTTPExpectation struct {
	op     string
	token  string
	body   any
	status int
	resp   any
}

func TestAgentSessionOps(t *testing.T) {
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	finished := now.Add(time.Minute)
	session := sessionResult{
		SessionID:     "lead-session",
		AgentID:       "nova",
		NodeID:        "placement-1",
		Kind:          domain.AgentSessionKindOrchestration,
		TerminalID:    "term-1",
		Status:        domain.AgentSessionRunning,
		LastHeartbeat: now,
		Metadata:      map[string]string{"a": "b"},
		CreatedAt:     &now,
		UpdatedAt:     &now,
	}
	client := newStepClient(t, []leadHTTPExpectation{
		{
			op:    "session-ensure",
			token: "tok-1",
			body:  map[string]any{"terminalId": "term-1", "metadata": map[string]string{"a": "b"}},
			resp:  sessionEnvelope{Session: &session},
		},
		{op: "session-get", token: "tok-1", body: map[string]any{}, resp: sessionEnvelope{Session: &session}},
		{op: "session-get", token: "tok-1", body: map[string]any{}, resp: sessionEnvelope{Session: &session}},
		{op: "session-get", token: "tok-1", body: map[string]any{}, resp: sessionEnvelope{Session: &session}},
		{
			op:    "session-update",
			token: "tok-1",
			body: map[string]any{
				"terminalId": "term-2",
				"status":     string(domain.AgentSessionCompleted),
				"finishedAt": finished,
				"metadata":   map[string]string{"done": "true"},
			},
			resp: sessionEnvelope{Session: &session},
		},
		{op: "heartbeat", token: "tok-1", body: map[string]any{}, resp: heartbeatEnvelope{Session: session}},
	})

	created, err := client.AgentSessions().Create(context.Background(), store.AgentSessionCreate{
		TerminalID: "term-1",
		Metadata:   map[string]string{"a": "b"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.SessionID != "lead-session" || created.WorkspaceKey != "WS" || created.Metadata["a"] != "b" {
		t.Fatalf("Create mapped session = %#v", created)
	}

	got, err := client.AgentSessions().Get(context.Background(), "WS", "ignored")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.AgentID != "nova" {
		t.Fatalf("Get agent = %q, want nova", got.AgentID)
	}

	list, err := client.AgentSessions().List(context.Background(), "WS", store.AgentSessionFilter{
		Kind:   domain.AgentSessionKindOrchestration,
		Status: domain.AgentSessionRunning,
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].SessionID != "lead-session" {
		t.Fatalf("List = %#v, want one session", list)
	}

	list, err = client.AgentSessions().List(context.Background(), "WS", store.AgentSessionFilter{
		Kind: domain.AgentSessionKindTask,
	})
	if err != nil {
		t.Fatalf("List filtered: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("filtered List length = %d, want 0", len(list))
	}

	status := domain.AgentSessionCompleted
	terminalID := "term-2"
	metadata := map[string]string{"done": "true"}
	finishedPtr := &finished
	updated, err := client.AgentSessions().Update(context.Background(), "WS", "ignored", store.AgentSessionUpdate{
		TerminalID: &terminalID,
		Status:     &status,
		FinishedAt: &finishedPtr,
		Metadata:   &metadata,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.SessionID != "lead-session" {
		t.Fatalf("Update session = %#v", updated)
	}

	heartbeat, err := client.AgentSessions().Heartbeat(context.Background(), "WS", "ignored")
	if err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if heartbeat.LastHeartbeat != now {
		t.Fatalf("heartbeat LastHeartbeat = %v, want %v", heartbeat.LastHeartbeat, now)
	}
}

func TestAgentSessionNullGetAndClaimNullMapToNotFound(t *testing.T) {
	client := newStepClient(t, []leadHTTPExpectation{
		{op: "session-get", token: "tok-1", body: map[string]any{}, resp: sessionEnvelope{Session: nil}},
		{op: "inbox-claim", token: "tok-1", body: map[string]any{"claimedBy": "nova", "leaseTtlSeconds": 120}, resp: inboxMessageEnvelope{Message: nil}},
	})

	if _, err := client.AgentSessions().Get(context.Background(), "WS", "ignored"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Get null err = %v, want ErrNotFound", err)
	}
	_, err := client.AgentInboxMessages().ClaimNext(context.Background(), store.AgentInboxMessageClaim{
		ClaimedBy: "nova",
		LeaseTTL:  2 * time.Minute,
	})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("ClaimNext null err = %v, want ErrNotFound", err)
	}
}

func TestAgentAndInboxOps(t *testing.T) {
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	agent := agentResult{
		Name:      "nova",
		RoleName:  "lead",
		Backend:   "codex",
		Parent:    "EPIC-1",
		UpdatedAt: &now,
	}
	message := inboxMessageResult{
		InboxMessageID: "msg-1",
		TargetAgentID:  "nova",
		SessionID:      "lead-session",
		Body:           "hello",
		Status:         domain.AgentInboxMessageQueued,
		CreatedAt:      &now,
		UpdatedAt:      &now,
	}
	client := newStepClient(t, []leadHTTPExpectation{
		{op: "agent-get", token: "tok-1", body: map[string]any{}, resp: agentEnvelope{Agent: &agent}},
		{op: "inbox-list", token: "tok-1", body: map[string]any{"status": string(domain.AgentInboxMessageQueued), "limit": 10}, resp: inboxListEnvelope{Messages: []inboxMessageResult{message}}},
		{op: "inbox-claim", token: "tok-1", body: map[string]any{"claimedBy": "drain:lead-session", "leaseTtlSeconds": 120}, resp: inboxMessageEnvelope{Message: &message}},
		{op: "inbox-complete", token: "tok-1", body: map[string]any{"inboxMessageId": "msg-1", "outcome": "delivered", "deliveredThreadId": "thread-1"}, resp: inboxMessageEnvelope{Message: &message}},
	})

	gotAgent, err := client.Agents().Get(context.Background(), "WS", "ignored")
	if err != nil {
		t.Fatalf("Agents.Get: %v", err)
	}
	if gotAgent.WorkspaceKey != "WS" || gotAgent.Name != "nova" || gotAgent.Parent != "EPIC-1" {
		t.Fatalf("mapped agent = %#v", gotAgent)
	}

	list, err := client.AgentInboxMessages().List(context.Background(), "WS", store.AgentInboxMessageFilter{
		Status: domain.AgentInboxMessageQueued,
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("Inbox.List: %v", err)
	}
	if len(list) != 1 || list[0].InboxMessageID != "msg-1" || list[0].WorkspaceKey != "WS" {
		t.Fatalf("mapped inbox list = %#v", list)
	}

	claimed, err := client.AgentInboxMessages().ClaimNext(context.Background(), store.AgentInboxMessageClaim{
		ClaimedBy: "drain:lead-session",
		LeaseTTL:  2 * time.Minute,
	})
	if err != nil {
		t.Fatalf("Inbox.ClaimNext: %v", err)
	}
	if claimed.Body != "hello" {
		t.Fatalf("claimed message = %#v", claimed)
	}

	completed, err := client.AgentInboxMessages().Complete(context.Background(), "WS", "msg-1", store.AgentInboxMessageComplete{
		Outcome:           "delivered",
		DeliveredThreadID: "thread-1",
	})
	if err != nil {
		t.Fatalf("Inbox.Complete: %v", err)
	}
	if completed.InboxMessageID != "msg-1" {
		t.Fatalf("completed message = %#v", completed)
	}
}

func TestUnsupportedBackedStoreMethods(t *testing.T) {
	client := newStepClient(t, nil)

	if _, err := client.AgentInboxMessages().Get(context.Background(), "WS", "msg"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Inbox.Get err = %v, want ErrUnsupported", err)
	}
	if _, err := client.AgentInboxMessages().Create(context.Background(), store.AgentInboxMessageCreate{}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Inbox.Create err = %v, want ErrUnsupported", err)
	}
	if _, err := client.Agents().List(context.Background(), "WS"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Agents.List err = %v, want ErrUnsupported", err)
	}
}

func TestErrorMapping(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		code        string
		retryable   bool
		want        error
		notWant     error
		messagePart string
	}{
		{name: "not found code", status: http.StatusNotFound, code: "not_found", want: domain.ErrNotFound},
		{name: "not found status", status: http.StatusNotFound, code: "unknown_op", want: domain.ErrNotFound},
		{name: "invalid code", status: http.StatusBadRequest, code: "invalid", want: domain.ErrInvalid},
		{name: "not owner", status: http.StatusForbidden, code: "not_owner", want: domain.ErrNotOwner},
		{name: "cap denied", status: http.StatusForbidden, code: "cap_denied", want: ErrCapabilityDenied},
		{name: "auth loud", status: http.StatusUnauthorized, code: "placement_absent", want: ErrAuth, notWant: domain.ErrNotFound},
		{name: "server retryable", status: http.StatusInternalServerError, code: "internal", want: ErrRetryable},
		{name: "envelope retryable", status: http.StatusGatewayTimeout, code: "timeout", retryable: true, want: ErrRetryable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newStepClient(t, []leadHTTPExpectation{{
				op:     "agent-get",
				token:  "tok-1",
				body:   map[string]any{},
				status: tt.status,
				resp: map[string]any{"error": opError{
					Code:      tt.code,
					Message:   "mapped failure",
					Retryable: tt.retryable,
				}},
			}})
			_, err := client.Agents().Get(context.Background(), "WS", "ignored")
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want errors.Is(%v)", err, tt.want)
			}
			if tt.notWant != nil && errors.Is(err, tt.notWant) {
				t.Fatalf("err = %v, should not match %v", err, tt.notWant)
			}
		})
	}
}

func TestTokenRotationUsesFreshTokenOnNextRequest(t *testing.T) {
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	client := newStepClient(t, []leadHTTPExpectation{
		{
			op:    "heartbeat",
			token: "tok-old",
			body:  map[string]any{},
			resp:  heartbeatEnvelope{Session: sessionResult{SessionID: "lead-session", AgentID: "nova", LastHeartbeat: now}, OccupantToken: "tok-new"},
		},
		{
			op:    "agent-get",
			token: "tok-new",
			body:  map[string]any{},
			resp:  agentEnvelope{Agent: &agentResult{Name: "nova", RoleName: "lead"}},
		},
	}, withInitialToken("tok-old"))

	if _, err := client.AgentSessions().Heartbeat(context.Background(), "WS", "ignored"); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if _, err := client.Agents().Get(context.Background(), "WS", "ignored"); err != nil {
		t.Fatalf("Agents.Get: %v", err)
	}
}

type clientOption func(*Config)

func withInitialToken(token string) clientOption {
	return func(cfg *Config) {
		cfg.OccupantToken = token
	}
}

func newStepClient(t *testing.T, steps []leadHTTPExpectation, opts ...clientOption) *Client {
	t.Helper()
	step := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if step >= len(steps) {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		expect := steps[step]
		step++
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		wantPath := "/api/workspaces/WS/lead/" + expect.op
		if r.URL.Path != wantPath {
			t.Fatalf("path = %q, want %q", r.URL.Path, wantPath)
		}
		wantAuth := "Bearer " + expect.token
		if got := r.Header.Get("Authorization"); got != wantAuth {
			t.Fatalf("Authorization header mismatch for op %s", expect.op)
		}
		var gotBody map[string]any
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if want := canonicalMap(t, expect.body); !reflect.DeepEqual(gotBody, want) {
			t.Fatalf("body = %#v, want %#v", gotBody, want)
		}
		status := expect.status
		if status == 0 {
			status = http.StatusOK
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if err := json.NewEncoder(w).Encode(expect.resp); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	}))
	t.Cleanup(func() {
		srv.Close()
		if step != len(steps) {
			t.Fatalf("handled %d requests, want %d", step, len(steps))
		}
	})

	cfg := Config{
		BaseURL:       strings.TrimRight(srv.URL, "/") + "/",
		WorkspaceKey:  "WS",
		OccupantToken: "tok-1",
		HTTPClient:    srv.Client(),
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	client, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return client
}

func canonicalMap(t *testing.T, value any) map[string]any {
	t.Helper()
	if value == nil {
		return nil
	}
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal canonical value: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("unmarshal canonical value: %v", err)
	}
	return out
}

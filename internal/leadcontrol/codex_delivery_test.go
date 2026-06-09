package leadcontrol

import (
	"context"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestDeliverCurrentAssignmentToCodexStartsTurnWhenIdle(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	createAssignedLeadSession(t, st, "idle", nil)

	fake := installFakeCodexClient(t, CodexThreadStatus{Type: "idle"})
	setCodexRuntimeMetadata(t, st, "WS", "lead-session", "ws://codex.test", "thread-1")

	result, err := DeliverCurrentAssignmentToCodex(ctx, st, "WS", "nova")
	if err != nil {
		t.Fatalf("DeliverCurrentAssignmentToCodex() error = %v", err)
	}
	if result.State != DeliveryStateDelivered {
		t.Fatalf("delivery state = %q, want delivered (reason: %s)", result.State, result.Reason)
	}
	text := fake.turnText
	if !strings.Contains(text, "assigned_epic: EPIC-1") {
		t.Fatalf("turn text did not include assignment context: %s", text)
	}

	session, err := st.AgentSessions().Get(ctx, "WS", "lead-session")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if got := session.Metadata[MetadataDeliveryVersion]; got == "" {
		t.Fatalf("delivered version was not recorded: %#v", session.Metadata)
	}
	if got := session.Metadata[MetadataDeliveryEpic]; got != "EPIC-1" {
		t.Fatalf("delivered epic = %q, want EPIC-1", got)
	}
}

func TestDeliverCurrentAssignmentToCodexLeavesBusyThreadPending(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	createAssignedLeadSession(t, st, "busy", nil)

	fake := installFakeCodexClient(t, CodexThreadStatus{Type: "active", ActiveFlags: []string{"waitingOnUserInput"}})
	setCodexRuntimeMetadata(t, st, "WS", "lead-session", "ws://codex.test", "thread-1")

	result, err := DeliverCurrentAssignmentToCodex(ctx, st, "WS", "nova")
	if err != nil {
		t.Fatalf("DeliverCurrentAssignmentToCodex() error = %v", err)
	}
	if result.State != DeliveryStatePending {
		t.Fatalf("delivery state = %q, want pending", result.State)
	}
	if !strings.Contains(result.Reason, RuntimeStatusWaitingUserInput) {
		t.Fatalf("reason = %q, want waiting-on-user-input detail", result.Reason)
	}
	if fake.turnText != "" {
		t.Fatalf("unexpected turn/start while busy: %s", fake.turnText)
	}
}

func TestDeliverCurrentAssignmentToCodexLeavesStartingRuntimePending(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	createAssignedLeadSession(t, st, "starting", map[string]string{
		"actor": "test",
	})

	result, err := DeliverCurrentAssignmentToCodex(ctx, st, "WS", "nova")
	if err != nil {
		t.Fatalf("DeliverCurrentAssignmentToCodex() error = %v", err)
	}
	if result.State != DeliveryStatePending {
		t.Fatalf("delivery state = %q, want pending", result.State)
	}
	if !strings.Contains(result.Reason, "not ready") {
		t.Fatalf("reason = %q, want runtime not ready", result.Reason)
	}
	session, err := st.AgentSessions().Get(ctx, "WS", "lead-session")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if session.Metadata[MetadataDeliveryAttemptedAt] == "" {
		t.Fatalf("pending runtime should record a delivery attempt: %#v", session.Metadata)
	}
}

func TestDeliverCurrentAssignmentToCodexDoesNotMarkUnsupportedRuntimeFailed(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	createAssignedLeadSession(t, st, "unsupported", map[string]string{
		MetadataRuntimeProvider: "claude",
	})

	result, err := DeliverCurrentAssignmentToCodex(ctx, st, "WS", "nova")
	if err != nil {
		t.Fatalf("DeliverCurrentAssignmentToCodex() error = %v", err)
	}
	if result.State != DeliveryStateUnsupported {
		t.Fatalf("delivery state = %q, want unsupported", result.State)
	}
	session, err := st.AgentSessions().Get(ctx, "WS", "lead-session")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if _, ok := session.Metadata[MetadataDeliveryError]; ok {
		t.Fatalf("unsupported non-codex runtime should not record codex delivery error: %#v", session.Metadata)
	}
}

func TestDeliverLeadMessageToCodexStartsTurnWhenIdle(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	createAssignedLeadSession(t, st, "message", nil)

	fake := installFakeCodexClient(t, CodexThreadStatus{Type: "idle"})
	setCodexRuntimeMetadata(t, st, "WS", "lead-session", "ws://codex.test", "thread-1")

	const message = "Task TASK-1 completed under the active epic-runner workflow."
	result, err := DeliverLeadMessageToCodex(ctx, st, "WS", "nova", message)
	if err != nil {
		t.Fatalf("DeliverLeadMessageToCodex() error = %v", err)
	}
	if result.State != DeliveryStateDelivered {
		t.Fatalf("delivery state = %q, want delivered (reason: %s)", result.State, result.Reason)
	}
	if fake.turnText != message {
		t.Fatalf("turn text = %q, want %q", fake.turnText, message)
	}

	session, err := st.AgentSessions().Get(ctx, "WS", "lead-session")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if got := session.Metadata[MetadataDeliveryVersion]; got != "" {
		t.Fatalf("message delivery should not mark assignment delivered version: %#v", session.Metadata)
	}
}

func createAssignedLeadSession(t *testing.T, st store.Store, label string, metadata map[string]string) {
	t.Helper()
	ctx := context.Background()
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: "WS",
		Name:         "nova",
		RoleName:     "lead",
		Backend:      "codex",
		Parent:       "EPIC-1",
	}); err != nil {
		t.Fatalf("%s: create lead: %v", label, err)
	}
	if metadata == nil {
		metadata = map[string]string{"actor": "test"}
	}
	if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "WS",
		SessionID:    "lead-session",
		AgentID:      "nova",
		Kind:         domain.AgentSessionKindOrchestration,
		Status:       domain.AgentSessionRunning,
		Metadata:     metadata,
	}); err != nil {
		t.Fatalf("%s: create session: %v", label, err)
	}
}

func setCodexRuntimeMetadata(t *testing.T, st store.Store, workspace, sessionID, endpoint, threadID string) {
	t.Helper()
	ctx := context.Background()
	if err := UpdateCodexRuntimeMetadata(ctx, st, workspace, sessionID, CodexRuntimeMetadata{
		Endpoint:   endpoint,
		ThreadID:   threadID,
		Status:     RuntimeStatusIdle,
		Controlled: true,
	}); err != nil {
		t.Fatalf("set runtime metadata: %v", err)
	}
}

type fakeCodexClient struct {
	status   CodexThreadStatus
	turnText string
}

func installFakeCodexClient(t *testing.T, status CodexThreadStatus) *fakeCodexClient {
	t.Helper()
	fake := &fakeCodexClient{status: status}
	orig := dialCodexAppServerClient
	dialCodexAppServerClient = func(context.Context, string) (codexAppServerClient, error) {
		return fake, nil
	}
	t.Cleanup(func() { dialCodexAppServerClient = orig })
	return fake
}

func (f *fakeCodexClient) Close(string) error { return nil }

func (f *fakeCodexClient) ListThreads(context.Context, string, int) ([]CodexThread, error) {
	return []CodexThread{{ID: "thread-1", Cwd: "/repo", UpdatedAt: 1, Status: f.status}}, nil
}

func (f *fakeCodexClient) ReadThread(context.Context, string) (*CodexThread, error) {
	return &CodexThread{ID: "thread-1", Cwd: "/repo", Status: f.status}, nil
}

func (f *fakeCodexClient) StartTurn(_ context.Context, _ string, text string) error {
	f.turnText = text
	return nil
}

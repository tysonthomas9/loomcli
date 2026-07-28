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
	if result.InboxMessageID == "" {
		t.Fatalf("delivery result should expose queued inbox message: %#v", result)
	}
	queued := queuedInboxMessagesForTest(t, st, "WS", "nova", "lead-session")
	if len(queued) != 1 {
		t.Fatalf("queued inbox messages = %#v, want one assignment message", queued)
	}
	if queued[0].SourceKind != assignmentInboxSourceKind {
		t.Fatalf("queued source kind = %q, want %q", queued[0].SourceKind, assignmentInboxSourceKind)
	}
	const sourcePrefix = "lead-assignment://EPIC-1/"
	if !strings.HasPrefix(queued[0].SourceRef, sourcePrefix) {
		t.Fatalf("queued source ref = %q", queued[0].SourceRef)
	}
	assignmentVersion := strings.TrimPrefix(queued[0].SourceRef, sourcePrefix)
	if assignmentVersion == "" {
		t.Fatalf("queued source ref did not include assignment version: %q", queued[0].SourceRef)
	}
	if !strings.Contains(queued[0].Body, "assigned_epic: EPIC-1") {
		t.Fatalf("queued body did not include assignment context: %s", queued[0].Body)
	}

	if _, err := DeliverCurrentAssignmentToCodex(ctx, st, "WS", "nova"); err != nil {
		t.Fatalf("retry DeliverCurrentAssignmentToCodex() error = %v", err)
	}
	queued = queuedInboxMessagesForTest(t, st, "WS", "nova", "lead-session")
	if len(queued) != 1 {
		t.Fatalf("queued inbox after duplicate retry = %#v, want no duplicate", queued)
	}

	fake.status = CodexThreadStatus{Type: "idle"}
	result, err = DeliverPendingLeadMessagesToCodex(ctx, st, "WS", "nova")
	if err != nil {
		t.Fatalf("DeliverPendingLeadMessagesToCodex() error = %v", err)
	}
	if result.State != DeliveryStateDelivered {
		t.Fatalf("delivery state = %q, want delivered (reason: %s)", result.State, result.Reason)
	}
	if !strings.Contains(fake.turnText, "assigned_epic: EPIC-1") {
		t.Fatalf("turn text did not include queued assignment context: %s", fake.turnText)
	}
	session, err := st.AgentSessions().Get(ctx, "WS", "lead-session")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if got := session.Metadata[MetadataDeliveryVersion]; got != assignmentVersion {
		t.Fatalf("delivered version = %q, want assignment version %q", got, assignmentVersion)
	}
	if got := queuedInboxMessagesForTest(t, st, "WS", "nova", "lead-session"); len(got) != 0 {
		t.Fatalf("queued inbox messages after delivery = %#v, want empty", got)
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

func TestDeliverLeadMessageToCodexQueuesBusyThreadAndDrainsWhenIdle(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	createAssignedLeadSession(t, st, "queued-message", nil)

	fake := installFakeCodexClient(t, CodexThreadStatus{Type: "active"})
	setCodexRuntimeMetadata(t, st, "WS", "lead-session", "ws://codex.test", "thread-1")

	const message = "Task TASK-1 completed under the active epic-runner workflow."
	result, err := DeliverLeadMessageToCodex(ctx, st, "WS", "nova", message)
	if err != nil {
		t.Fatalf("DeliverLeadMessageToCodex() error = %v", err)
	}
	if result.State != DeliveryStatePending {
		t.Fatalf("delivery state = %q, want pending", result.State)
	}
	if fake.turnText != "" {
		t.Fatalf("unexpected turn/start while busy: %s", fake.turnText)
	}

	queued := queuedInboxMessagesForTest(t, st, "WS", "nova", "lead-session")
	if len(queued) != 1 || queued[0].Body != message {
		t.Fatalf("queued inbox messages = %#v, want one queued completion message", queued)
	}

	if _, err := DeliverLeadMessageToCodex(ctx, st, "WS", "nova", message); err != nil {
		t.Fatalf("retry DeliverLeadMessageToCodex() error = %v", err)
	}
	queued = queuedInboxMessagesForTest(t, st, "WS", "nova", "lead-session")
	if len(queued) != 1 || queued[0].Body != message {
		t.Fatalf("queued inbox after duplicate retry = %#v, want no duplicate", queued)
	}

	fake.status = CodexThreadStatus{Type: "idle"}
	result, err = DeliverLeadMessageToCodex(ctx, st, "WS", "nova", message)
	if err != nil {
		t.Fatalf("idle DeliverLeadMessageToCodex() error = %v", err)
	}
	if result.State != DeliveryStateDelivered {
		t.Fatalf("delivery state = %q, want delivered (reason: %s)", result.State, result.Reason)
	}
	if result.Runtime.Status != RuntimeStatusIdle {
		t.Fatalf("result runtime status = %q, want idle after reading idle thread", result.Runtime.Status)
	}
	if fake.turnText != message {
		t.Fatalf("turn text = %q, want queued message %q", fake.turnText, message)
	}
	if got := queuedInboxMessagesForTest(t, st, "WS", "nova", "lead-session"); len(got) != 0 {
		t.Fatalf("queued inbox messages after delivery = %#v, want empty", got)
	}
}

func TestDeliverLeadMessageToCodexQueuesBeforeSessionAndDrainsLater(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: "WS",
		Name:         "nova",
		RoleName:     "lead",
		Backend:      "codex",
		Parent:       "EPIC-1",
	}); err != nil {
		t.Fatalf("create lead: %v", err)
	}

	const message = "Task TASK-1 completed before the lead runtime was ready."
	result, err := DeliverLeadMessageToCodex(ctx, st, "WS", "nova", message)
	if err != nil {
		t.Fatalf("DeliverLeadMessageToCodex() error = %v", err)
	}
	if result.State != DeliveryStatePending || result.InboxMessageID == "" {
		t.Fatalf("delivery result = %#v, want pending with inbox message", result)
	}
	queued := queuedInboxMessagesForTest(t, st, "WS", "nova", "")
	if len(queued) != 1 || queued[0].Body != message || queued[0].SessionID != "" {
		t.Fatalf("queued inbox messages = %#v, want one sessionless message", queued)
	}

	if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "WS",
		SessionID:    "lead-session",
		AgentID:      "nova",
		Kind:         domain.AgentSessionKindOrchestration,
		Status:       domain.AgentSessionRunning,
		Metadata:     map[string]string{"actor": "test"},
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	fake := installFakeCodexClient(t, CodexThreadStatus{Type: "idle"})
	setCodexRuntimeMetadata(t, st, "WS", "lead-session", "ws://codex.test", "thread-1")

	result, err = DeliverPendingLeadMessagesToCodex(ctx, st, "WS", "nova")
	if err != nil {
		t.Fatalf("DeliverPendingLeadMessagesToCodex() error = %v", err)
	}
	if result.State != DeliveryStateDelivered {
		t.Fatalf("delivery state = %q, want delivered (reason: %s)", result.State, result.Reason)
	}
	if fake.turnText != message {
		t.Fatalf("turn text = %q, want queued message %q", fake.turnText, message)
	}
	if got := queuedInboxMessagesForTest(t, st, "WS", "nova", "lead-session"); len(got) != 0 {
		t.Fatalf("queued inbox messages after delivery = %#v, want empty", got)
	}
}

func TestDeliverLeadMessageToCodexDoesNotDuplicateSessionlessMessageAfterSessionStarts(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: "WS",
		Name:         "nova",
		RoleName:     "lead",
		Backend:      "codex",
		Parent:       "EPIC-1",
	}); err != nil {
		t.Fatalf("create lead: %v", err)
	}

	const message = "Task TASK-1 completed before the lead runtime was ready."
	result, err := DeliverLeadMessageToCodex(ctx, st, "WS", "nova", message)
	if err != nil {
		t.Fatalf("sessionless DeliverLeadMessageToCodex() error = %v", err)
	}
	if result.State != DeliveryStatePending || result.InboxMessageID == "" {
		t.Fatalf("delivery result = %#v, want pending with inbox message", result)
	}

	if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "WS",
		SessionID:    "lead-session",
		AgentID:      "nova",
		Kind:         domain.AgentSessionKindOrchestration,
		Status:       domain.AgentSessionRunning,
		Metadata:     map[string]string{"actor": "test"},
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	fake := installFakeCodexClient(t, CodexThreadStatus{Type: "active"})
	setCodexRuntimeMetadata(t, st, "WS", "lead-session", "ws://codex.test", "thread-1")

	result, err = DeliverLeadMessageToCodex(ctx, st, "WS", "nova", message)
	if err != nil {
		t.Fatalf("retry DeliverLeadMessageToCodex() error = %v", err)
	}
	if result.State != DeliveryStatePending {
		t.Fatalf("delivery state = %q, want pending", result.State)
	}
	if fake.turnText != "" {
		t.Fatalf("unexpected turn/start while busy: %s", fake.turnText)
	}
	queued := queuedInboxMessagesForTest(t, st, "WS", "nova", "lead-session")
	if len(queued) != 1 || queued[0].Body != message {
		t.Fatalf("queued inbox messages after session retry = %#v, want one deduped message", queued)
	}
}

func TestDeliverPendingLeadMessagesToCodexDrainsQueueWithoutNewMessage(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	createAssignedLeadSession(t, st, "pending-queue-drain", nil)

	fake := installFakeCodexClient(t, CodexThreadStatus{Type: "active"})
	setCodexRuntimeMetadata(t, st, "WS", "lead-session", "ws://codex.test", "thread-1")

	const message = "Task TASK-2 completed after the workflow exited."
	result, err := DeliverLeadMessageToCodex(ctx, st, "WS", "nova", message)
	if err != nil {
		t.Fatalf("queue DeliverLeadMessageToCodex() error = %v", err)
	}
	if result.State != DeliveryStatePending {
		t.Fatalf("delivery state = %q, want pending", result.State)
	}

	fake.status = CodexThreadStatus{Type: "idle"}
	result, err = DeliverPendingLeadMessagesToCodex(ctx, st, "WS", "nova")
	if err != nil {
		t.Fatalf("DeliverPendingLeadMessagesToCodex() error = %v", err)
	}
	if result.State != DeliveryStateDelivered {
		t.Fatalf("delivery state = %q, want delivered (reason: %s)", result.State, result.Reason)
	}
	if fake.turnText != message {
		t.Fatalf("turn text = %q, want queued message %q", fake.turnText, message)
	}
	if got := queuedInboxMessagesForTest(t, st, "WS", "nova", "lead-session"); len(got) != 0 {
		t.Fatalf("queued inbox messages after pending drain = %#v, want empty", got)
	}
}

func queuedInboxMessagesForTest(t *testing.T, st store.Store, workspace, leadName, sessionID string) []*domain.AgentInboxMessage {
	t.Helper()
	items, err := st.AgentInboxMessages().List(context.Background(), workspace, store.AgentInboxMessageFilter{
		TargetAgentID: leadName,
		SessionID:     sessionID,
		Status:        domain.AgentInboxMessageQueued,
	})
	if err != nil {
		t.Fatalf("list queued inbox messages: %v", err)
	}
	return items
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

func (f *fakeCodexClient) ReadThreadWithTurns(context.Context, string) (*CodexThread, error) {
	return &CodexThread{ID: "thread-1", Cwd: "/repo", Status: f.status}, nil
}

func (f *fakeCodexClient) ReadThreadTranscript(context.Context, string) (*CodexThread, error) {
	return &CodexThread{ID: "thread-1", Cwd: "/repo", Status: f.status}, nil
}

func (f *fakeCodexClient) StartTurn(_ context.Context, _ string, text string) error {
	f.turnText = text
	return nil
}

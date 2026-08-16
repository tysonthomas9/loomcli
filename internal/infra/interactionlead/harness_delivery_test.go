package leadcontrol

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/interaction"

	"github.com/olesho/harness-wrapper/pkg/chat"
	"github.com/olesho/harness-wrapper/pkg/wrapper"

	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
)

func TestDeliverLeadMessageToHarnessLeadDeliversWhenQuiet(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	createHarnessLeadSession(t, st)
	setHarnessRuntimeMetadata(t, st, RuntimeStatusIdle)
	fake := newFakeHarnessConversation()
	installFakeHarnessConversation(t, "lead-session", fake)

	const message = "Task TASK-1 completed under the active epic-runner workflow."
	result, err := deliverLeadMessageOwned(ctx, st, testSessionRuntime(st), "WS", "nova", message)
	if err != nil {
		t.Fatalf("DeliverLeadMessage() error = %v", err)
	}
	if result.State != DeliveryStateDelivered {
		t.Fatalf("delivery state = %q, want delivered (reason: %s)", result.State, result.Reason)
	}
	if result.Provider != "claude" {
		t.Fatalf("result provider = %q, want claude", result.Provider)
	}
	if got := string(fake.stdinBytes()); got != message {
		t.Fatalf("staged stdin = %q, want message text", got)
	}
	if got := fake.sentTexts(); len(got) != 1 || got[0] != "" {
		t.Fatalf("sent texts = %#v, want one empty submit Send", got)
	}
	session := getLeadSession(t, st)
	if got := session.Metadata[MetadataRuntimeStatus]; got != RuntimeStatusActive {
		t.Fatalf("runtime status after delivery = %q, want active", got)
	}
	if got := queuedInboxMessagesForTest(t, st, "WS", "nova", "lead-session"); len(got) != 0 {
		t.Fatalf("queued inbox messages after delivery = %#v, want empty", got)
	}
}

func TestDeliverCurrentAssignmentToHarnessLeadUsesBracketedPaste(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	createAssignedLeadSessionWithBackend(t, st, "claude")
	setHarnessRuntimeMetadata(t, st, RuntimeStatusIdle)
	fake := newFakeHarnessConversation()
	installFakeHarnessConversation(t, "lead-session", fake)

	result, err := deliverCurrentAssignmentOwned(ctx, st, testSessionRuntime(st), "WS", "nova")
	if err != nil {
		t.Fatalf("DeliverCurrentAssignment() error = %v", err)
	}
	if result.State != DeliveryStateDelivered {
		t.Fatalf("delivery state = %q, want delivered (reason: %s)", result.State, result.Reason)
	}
	stdin := string(fake.stdinBytes())
	if !strings.HasPrefix(stdin, "\x1b[200~") || !strings.HasSuffix(stdin, "\x1b[201~") {
		t.Fatalf("multi-line assignment was not bracketed-pasted: %q", stdin)
	}
	if !strings.Contains(stdin, "assigned_epic: EPIC-1") {
		t.Fatalf("pasted assignment missing context: %q", stdin)
	}
	if got := fake.sentTexts(); len(got) != 1 || got[0] != "" {
		t.Fatalf("sent texts = %#v, want one empty submit Send", got)
	}
	session := getLeadSession(t, st)
	if got := session.Metadata[MetadataDeliveryVersion]; got == "" {
		t.Fatalf("delivered version was not recorded: %#v", session.Metadata)
	}
}

func TestHarnessDeliveryRegistryMissStaysPending(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	createHarnessLeadSession(t, st)
	setHarnessRuntimeMetadata(t, st, RuntimeStatusIdle)

	const message = "Task TASK-1 completed."
	result, err := deliverLeadMessageOwned(ctx, st, testSessionRuntime(st), "WS", "nova", message)
	if err != nil {
		t.Fatalf("DeliverLeadMessage() error = %v", err)
	}
	if result.State != DeliveryStatePending {
		t.Fatalf("delivery state = %q, want pending", result.State)
	}
	if result.Reason != harnessRegistryMissReason {
		t.Fatalf("reason = %q, want registry-miss reason", result.Reason)
	}
	queued := queuedInboxMessagesForTest(t, st, "WS", "nova", "lead-session")
	if len(queued) != 1 || queued[0].Body != message {
		t.Fatalf("queued inbox messages = %#v, want message kept for in-runtime drain", queued)
	}
}

func TestHarnessDeliveryWaitsForTurnInFlightThenDrains(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	createHarnessLeadSession(t, st)
	setHarnessRuntimeMetadata(t, st, RuntimeStatusActive)
	fake := newFakeHarnessConversation()
	handle := installFakeHarnessConversation(t, "lead-session", fake)
	handle.markTurnStarted()

	const message = "Task TASK-1 completed."
	result, err := deliverLeadMessageOwned(ctx, st, testSessionRuntime(st), "WS", "nova", message)
	if err != nil {
		t.Fatalf("DeliverLeadMessage() error = %v", err)
	}
	if result.State != DeliveryStatePending {
		t.Fatalf("delivery state = %q, want pending", result.State)
	}
	if !strings.Contains(result.Reason, "turn is in flight") {
		t.Fatalf("reason = %q, want turn-in-flight detail", result.Reason)
	}
	if got := fake.sentTexts(); len(got) != 0 {
		t.Fatalf("unexpected send while turn in flight: %#v", got)
	}

	handle.markTurnDone()
	result, err = deliverPendingLeadMessagesOwned(ctx, st, testSessionRuntime(st), "WS", "nova")
	if err != nil {
		t.Fatalf("DeliverPendingLeadMessages() error = %v", err)
	}
	if result.State != DeliveryStateDelivered {
		t.Fatalf("delivery state = %q, want delivered (reason: %s)", result.State, result.Reason)
	}
	if got := string(fake.stdinBytes()); got != message {
		t.Fatalf("staged stdin after drain = %q, want queued message", got)
	}
}

func TestHarnessDeliveryQuietGateHoldsRecentOutput(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	createHarnessLeadSession(t, st)
	setHarnessRuntimeMetadata(t, st, RuntimeStatusActive)
	fake := newFakeHarnessConversation()
	fake.setSnapshot(wrapper.Snapshot{LastOutputAt: time.Now()})
	installFakeHarnessConversation(t, "lead-session", fake)

	result, err := deliverLeadMessageOwned(ctx, st, testSessionRuntime(st), "WS", "nova", "Task TASK-1 completed.")
	if err != nil {
		t.Fatalf("DeliverLeadMessage() error = %v", err)
	}
	if result.State != DeliveryStatePending {
		t.Fatalf("delivery state = %q, want pending", result.State)
	}
	if !strings.Contains(result.Reason, "not settled") {
		t.Fatalf("reason = %q, want output-not-settled detail", result.Reason)
	}
	if got := fake.sentTexts(); len(got) != 0 {
		t.Fatalf("unexpected send during quiet window: %#v", got)
	}
}

func TestHarnessDeliveryFailedSnapshotStaysPending(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	createHarnessLeadSession(t, st)
	setHarnessRuntimeMetadata(t, st, RuntimeStatusActive)
	fake := newFakeHarnessConversation()
	fake.setSnapshot(wrapper.Snapshot{Status: wrapper.StatusBlockedByCost, LastOutputAt: time.Now().Add(-time.Minute)})
	installFakeHarnessConversation(t, "lead-session", fake)

	result, err := deliverLeadMessageOwned(ctx, st, testSessionRuntime(st), "WS", "nova", "Task TASK-1 completed.")
	if err != nil {
		t.Fatalf("DeliverLeadMessage() error = %v", err)
	}
	if result.State != DeliveryStatePending {
		t.Fatalf("delivery state = %q, want pending", result.State)
	}
	if !strings.Contains(result.Reason, "failed") {
		t.Fatalf("reason = %q, want failed detail", result.Reason)
	}
	session := getLeadSession(t, st)
	if got := session.Metadata[MetadataRuntimeStatus]; got != RuntimeStatusFailed {
		t.Fatalf("runtime status = %q, want failed persisted from snapshot", got)
	}
}

func TestHarnessDeliveryUncontrolledSessionUnsupported(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	createHarnessLeadSession(t, st)
	// Provider set but controlled flag absent: not a controlled runtime.
	session := getLeadSession(t, st)
	metadata := cloneMetadata(session.Metadata)
	metadata[MetadataRuntimeProvider] = "claude"
	if _, err := st.AgentSessions().Update(ctx, "WS", "lead-session", interaction.AgentSessionUpdate{Metadata: &metadata}); err != nil {
		t.Fatalf("seed metadata: %v", err)
	}

	result, err := deliverLeadMessageOwned(ctx, st, testSessionRuntime(st), "WS", "nova", "Task TASK-1 completed.")
	if err != nil {
		t.Fatalf("DeliverLeadMessage() error = %v", err)
	}
	if result.State != DeliveryStateUnsupported {
		t.Fatalf("delivery state = %q, want unsupported", result.State)
	}
}

func TestHarnessRuntimeStatusMapping(t *testing.T) {
	cases := []struct {
		status wrapper.Status
		want   string
	}{
		{wrapper.StatusWaitingForInput, RuntimeStatusWaitingUserInput},
		{wrapper.StatusIdle, RuntimeStatusIdle},
		{wrapper.StatusStale, RuntimeStatusIdle},
		{wrapper.StatusInterrupted, RuntimeStatusWaitingUserInput},
		{wrapper.StatusFailed, RuntimeStatusFailed},
		{wrapper.StatusBlockedByCost, RuntimeStatusFailed},
		{wrapper.StatusRetryLater, RuntimeStatusFailed},
		{wrapper.StatusAPIError, RuntimeStatusFailed},
		{wrapper.StatusBinaryNotFound, RuntimeStatusFailed},
		{wrapper.Status(""), RuntimeStatusActive},
	}
	for _, tc := range cases {
		if got := harnessRuntimeStatus(wrapper.Snapshot{Status: tc.status}); got != tc.want {
			t.Errorf("harnessRuntimeStatus(%q) = %q, want %q", tc.status, got, tc.want)
		}
	}
}

// TestSendHarnessTurnBoundedWhenNotReady locks the anti-wedge contract: the
// wrapper's Send readiness heuristic only matches claude's boot screen, so
// once output scrolls it away Send blocks forever. The send must (a) be
// bounded by the send timeout instead of parking the drain goroutine
// indefinitely, and (b) fall back to submitting the staged text directly with
// the Enter key event — delivery was already quiet-window-gated, so the
// composer is live.
func TestSendHarnessTurnBoundedWhenNotReady(t *testing.T) {
	origTimeout := harnessSendTimeout
	harnessSendTimeout = 50 * time.Millisecond
	t.Cleanup(func() { harnessSendTimeout = origTimeout })

	fake := newFakeHarnessConversation()
	fake.setSendBlocksUntilCancel(true)
	handle := &leadConversationHandle{conv: fake}

	start := time.Now()
	if err := sendHarnessTurn(context.Background(), handle, "hello reviewer"); err != nil {
		t.Fatalf("sendHarnessTurn error = %v, want fallback delivery", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("send attempt took %v — the timeout is not bounding the wrapper wait", elapsed)
	}
	// Staged exactly once, submitted via the direct Enter keystroke.
	if got := string(fake.stdinBytes()); got != "hello reviewer"+harnessEnterKeystroke {
		t.Fatalf("staged stdin = %q, want message + direct Enter", got)
	}
	if handle.staged() != "" {
		t.Fatalf("staged bookkeeping not cleared after fallback delivery: %q", handle.staged())
	}

	// A non-timeout Send failure must NOT trigger the fallback and must keep
	// the staged text remembered so the retry does not duplicate it.
	fake2 := newFakeHarnessConversation()
	fake2.sendErr = chat.ErrTurnInFlight
	handle2 := &leadConversationHandle{conv: fake2}
	if err := sendHarnessTurn(context.Background(), handle2, "hello again"); !errors.Is(err, chat.ErrTurnInFlight) {
		t.Fatalf("sendHarnessTurn error = %v, want ErrTurnInFlight", err)
	}
	if got := string(fake2.stdinBytes()); got != "hello again" {
		t.Fatalf("stdin = %q, want staged text without a submit keystroke", got)
	}
	if handle2.staged() != "hello again" {
		t.Fatalf("staged bookkeeping = %q, want the pending message", handle2.staged())
	}
	// Retry after the turn clears: submit without re-staging.
	fake2.mu.Lock()
	fake2.sendErr = nil
	fake2.mu.Unlock()
	if err := sendHarnessTurn(context.Background(), handle2, "hello again"); err != nil {
		t.Fatalf("retry error = %v", err)
	}
	if got := string(fake2.stdinBytes()); got != "hello again" {
		t.Fatalf("stdin after retry = %q, want no duplicate staging", got)
	}
	if got := fake2.sentTexts(); len(got) != 1 || got[0] != "" {
		t.Fatalf("sends = %#v, want exactly one wrapper submit", got)
	}
}

func TestSendHarnessTurnFraming(t *testing.T) {
	ctx := context.Background()

	single := newFakeHarnessConversation()
	if err := sendHarnessTurn(ctx, &leadConversationHandle{conv: single}, "one line"); err != nil {
		t.Fatalf("sendHarnessTurn(single) error = %v", err)
	}
	if got := string(single.stdinBytes()); got != "one line" {
		t.Fatalf("single-line staged stdin = %q", got)
	}
	if got := single.sentTexts(); len(got) != 1 || got[0] != "" {
		t.Fatalf("single-line submit sends = %#v, want one empty Send", got)
	}

	multi := newFakeHarnessConversation()
	if err := sendHarnessTurn(ctx, &leadConversationHandle{conv: multi}, "line one\nline two"); err != nil {
		t.Fatalf("sendHarnessTurn(multi) error = %v", err)
	}
	if got := string(multi.stdinBytes()); got != "\x1b[200~line one\nline two\x1b[201~" {
		t.Fatalf("multi-line paste = %q", got)
	}
	if got := multi.sentTexts(); len(got) != 1 || got[0] != "" {
		t.Fatalf("multi-line submit sends = %#v, want one empty Send", got)
	}
}

func TestLeadConversationHandleInFlightOverride(t *testing.T) {
	fake := newFakeHarnessConversation()
	fake.setSnapshot(wrapper.Snapshot{LastOutputAt: time.Now().Add(-2 * harnessInFlightOverrideWindow)})
	handle := &leadConversationHandle{conv: fake}
	handle.markTurnStarted()
	if handle.turnInFlight() {
		t.Fatalf("turnInFlight() = true, want missed-marker override after long quiet")
	}
}

// --- helpers and fakes ---

func createHarnessLeadSession(t *testing.T, st *memstore.Store) {
	t.Helper()
	createAssignedLeadSessionWithBackend(t, st, "claude")
}

func createAssignedLeadSessionWithBackend(t *testing.T, st *memstore.Store, backend string) {
	t.Helper()
	ctx := context.Background()
	seedAssignedLeadIdentity(t, st)
	if _, err := st.AgentSessions().Create(ctx, interaction.AgentSessionCreate{
		WorkspaceKey: "WS",
		SessionID:    "lead-session",
		AgentID:      "nova",
		Kind:         interaction.SessionRecordInteractive,
		Status:       interaction.SessionRecordRunning,
		Metadata:     map[string]string{"actor": "test"},
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}
}

func setHarnessRuntimeMetadata(t *testing.T, st sessionRuntimeFixtureStore, status string) {
	t.Helper()
	if err := UpdateHarnessRuntimeMetadata(context.Background(), testSessionRuntime(st), "WS", "lead-session", HarnessRuntimeMetadata{
		Provider:      "claude",
		HarnessName:   HarnessNameClaudeCode,
		ChatSessionID: "chat-1",
		PID:           42,
		Status:        status,
		Controlled:    true,
	}); err != nil {
		t.Fatalf("set harness runtime metadata: %v", err)
	}
}

func getLeadSession(t *testing.T, st *memstore.Store) *interaction.SessionRecord {
	t.Helper()
	session, err := st.AgentSessions().Get(context.Background(), "WS", "lead-session")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	return session
}

func installFakeHarnessConversation(t *testing.T, sessionID string, fake *fakeHarnessConversation) *leadConversationHandle {
	t.Helper()
	handle, unregister := registerLeadConversation(sessionID, fake)
	t.Cleanup(unregister)
	return handle
}

type fakeHarnessConversation struct {
	mu                    sync.Mutex
	sends                 []string
	stdin                 []byte
	snapshot              wrapper.Snapshot
	sendErr               error
	sendBlocksUntilCancel bool
	harnessSessionID      string
	history               []chat.Turn
	historyErr            error
	historyCalls          int
	boundedHistory        boundedHarnessHistory
	boundedHistoryErr     error
	boundedHistoryCalls   int
	boundedHistorySession string
	detachOutput          []byte
	attachedOutputCount   int
	detachedOutputCount   int
	events                chan chat.TurnEvent
	waitCh                chan struct{}
	waitResult            wrapper.Result
	closed                bool
	closeErr              error
	closeLeavesEventsOpen bool
	closeOnce             sync.Once
}

func newFakeHarnessConversation() *fakeHarnessConversation {
	return &fakeHarnessConversation{
		// Default: quiet long ago, unclassified — deliverable.
		snapshot: wrapper.Snapshot{LastOutputAt: time.Now().Add(-time.Minute)},
		events:   make(chan chat.TurnEvent, 8),
		waitCh:   make(chan struct{}),
	}
}

func (f *fakeHarnessConversation) setSnapshot(snap wrapper.Snapshot) {
	f.mu.Lock()
	f.snapshot = snap
	f.mu.Unlock()
}

func (f *fakeHarnessConversation) sentTexts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.sends...)
}

func (f *fakeHarnessConversation) stdinBytes() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]byte{}, f.stdin...)
}

func (f *fakeHarnessConversation) AcquireControl(context.Context) (func(), error) {
	return func() {}, nil
}

func (f *fakeHarnessConversation) Send(ctx context.Context, text string) (string, error) {
	f.mu.Lock()
	blocked := f.sendBlocksUntilCancel
	f.mu.Unlock()
	if blocked {
		// Models the wrapper's waitReadyForSend never matching the screen:
		// Send parks until the caller's context expires.
		<-ctx.Done()
		return "", ctx.Err()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sendErr != nil {
		return "", f.sendErr
	}
	f.sends = append(f.sends, text)
	return "turn-1", nil
}

func (f *fakeHarnessConversation) setSendBlocksUntilCancel(v bool) {
	f.mu.Lock()
	f.sendBlocksUntilCancel = v
	f.mu.Unlock()
}

func (f *fakeHarnessConversation) WriteStdin(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stdin = append(f.stdin, p...)
	return len(p), nil
}

func (f *fakeHarnessConversation) AttachOutput(w io.Writer) func() {
	f.mu.Lock()
	f.attachedOutputCount++
	f.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			f.mu.Lock()
			pending := append([]byte{}, f.detachOutput...)
			f.mu.Unlock()
			if len(pending) > 0 {
				_, _ = w.Write(pending)
			}
			f.mu.Lock()
			f.detachedOutputCount++
			f.mu.Unlock()
		})
	}
}

func (f *fakeHarnessConversation) Resize(uint16, uint16) error { return nil }

func (f *fakeHarnessConversation) Snapshot() wrapper.Snapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.snapshot
}

func (f *fakeHarnessConversation) PID() int { return 42 }

func (f *fakeHarnessConversation) ChatSessionID() string { return "chat-1" }

func (f *fakeHarnessConversation) HarnessSessionID() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.harnessSessionID
}

func (f *fakeHarnessConversation) History(context.Context) ([]chat.Turn, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.historyCalls++
	return append([]chat.Turn{}, f.history...), f.historyErr
}

func (f *fakeHarnessConversation) HistoryWithinRawLimit(
	_ context.Context,
	harnessSessionID string,
	_ int,
) (boundedHarnessHistory, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.boundedHistoryCalls++
	f.boundedHistorySession = harnessSessionID
	result := f.boundedHistory
	result.turns = append([]chat.Turn{}, result.turns...)
	return result, f.boundedHistoryErr
}

func (f *fakeHarnessConversation) Events() <-chan chat.TurnEvent { return f.events }

func (f *fakeHarnessConversation) Wait() (wrapper.Result, error) {
	<-f.waitCh
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.waitResult, nil
}

func (f *fakeHarnessConversation) Close(context.Context) error {
	f.closeOnce.Do(func() {
		f.mu.Lock()
		f.closed = true
		events := f.events
		leaveEventsOpen := f.closeLeavesEventsOpen
		f.mu.Unlock()
		if !leaveEventsOpen {
			close(events)
		}
	})
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closeErr
}

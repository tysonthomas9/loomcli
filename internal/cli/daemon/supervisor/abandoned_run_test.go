package supervisor

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// newAbandonedRunSupervisor wires a control-plane-backed supervisor with the
// given mock issue backend, following the package idiom
// (newControlPlaneTestSupervisor + clitest.MockIssueBackend).
func newAbandonedRunSupervisor(t *testing.T, mock *clitest.MockIssueBackend) (*Supervisor, *memstore.Store) {
	t.Helper()
	st := memstore.New()
	s := newControlPlaneTestSupervisor(st)
	s.IssueBackend = mock
	return s, st
}

// seedSession creates an unfinished (or finished) task session row directly in
// the control plane, the way a previous daemon process would have left it.
func seedSession(t *testing.T, st store.Store, sessionID, agentID, taskID string, status domain.AgentSessionStatus) *domain.AgentSession {
	t.Helper()
	sess, err := st.AgentSessions().Create(context.Background(), store.AgentSessionCreate{
		WorkspaceKey: "WS",
		SessionID:    sessionID,
		AgentID:      agentID,
		NodeID:       "node-1",
		Kind:         domain.AgentSessionKindTask,
		TaskID:       taskID,
		Status:       status,
	})
	if err != nil {
		t.Fatalf("seed session %s: %v", sessionID, err)
	}
	return sess
}

// finishSession marks a row terminal the way the normal exit path does.
func finishSession(t *testing.T, st store.Store, sessionID string) {
	t.Helper()
	status := domain.AgentSessionCompleted
	finished := time.Now().UTC()
	finishedPtr := &finished
	if _, err := st.AgentSessions().Update(context.Background(), "WS", sessionID, store.AgentSessionUpdate{
		Status:     &status,
		FinishedAt: &finishedPtr,
	}); err != nil {
		t.Fatalf("finish session %s: %v", sessionID, err)
	}
}

// unfinishSession re-opens a latched row, simulating a crash between the
// comment write and the latch.
func unfinishSession(t *testing.T, st store.Store, sessionID string) {
	t.Helper()
	status := domain.AgentSessionRunning
	var nilTime *time.Time
	if _, err := st.AgentSessions().Update(context.Background(), "WS", sessionID, store.AgentSessionUpdate{
		Status:     &status,
		FinishedAt: &nilTime,
	}); err != nil {
		t.Fatalf("unfinish session %s: %v", sessionID, err)
	}
}

func getSession(t *testing.T, st store.Store, sessionID string) *domain.AgentSession {
	t.Helper()
	sess, err := st.AgentSessions().Get(context.Background(), "WS", sessionID)
	if err != nil {
		t.Fatalf("get session %s: %v", sessionID, err)
	}
	return sess
}

// ownedAgent is an AgentProcess that holds the ownership lease (entry point 1's
// precondition).
func ownedAgent(worktree string) *AgentProcess {
	ap := &AgentProcess{Entry: cfgpkg.AgentEntry{Worktree: worktree, Role: "task"}}
	ap.OwnershipLeaseToken = "token-" + worktree
	return ap
}

// issueWithLabels is the mock's GET result for a live (open) task.
func issueWithLabels(id string, labels ...string) *backend.IssueDetailData {
	issue := &backend.IssueDetailData{}
	issue.ID = id
	issue.Status = "open"
	issue.Labels = labels
	return issue
}

// commentTexts returns the Text of every AddComment call recorded by the mock.
func commentTexts(mock *clitest.MockIssueBackend) []string {
	var out []string
	for _, c := range mock.Calls {
		if c.Method != "AddComment" || len(c.Args) == 0 {
			continue
		}
		if params, ok := c.Args[0].(backend.CommentAddParams); ok {
			out = append(out, params.Text)
		}
	}
	return out
}

// addedLabels returns every label passed to AddLabel.
func addedLabels(mock *clitest.MockIssueBackend) []string {
	var out []string
	for _, c := range mock.Calls {
		if c.Method != "AddLabel" || len(c.Args) < 2 {
			continue
		}
		if label, ok := c.Args[1].(string); ok {
			out = append(out, label)
		}
	}
	return out
}

func removedLabels(mock *clitest.MockIssueBackend) []string {
	var out []string
	for _, c := range mock.Calls {
		if c.Method != "RemoveLabel" || len(c.Args) < 2 {
			continue
		}
		if label, ok := c.Args[1].(string); ok {
			out = append(out, label)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// 1. label grammar
// ---------------------------------------------------------------------------

func TestAttemptLabelRoundTrip(t *testing.T) {
	label := attemptLabel("integrator", 3)
	if label != "loom:attempt:integrator=3" {
		t.Fatalf("attemptLabel = %q, want loom:attempt:integrator=3", label)
	}
	if got := parseAttemptCounter("integrator", label); got != 3 {
		t.Fatalf("parseAttemptCounter(%q) = %d, want 3", label, got)
	}
}

func TestParseAttemptCounterIsStrictAndPerAgent(t *testing.T) {
	cases := []struct {
		agent string
		label string
		want  int
	}{
		{"integrator", "loom:attempt:integrator=2", 2},
		{"integrator", "loom:attempt:coder=2", 0}, // another agent's counter never leaks in
		{"integrator", "loom:attempt:integrator=1.5", 0},
		{"integrator", "loom:attempt:integrator=0", 0},
		{"integrator", "loom:attempt:integrator=-1", 0},
		{"integrator", "loom:attempt:integrator=", 0},
		{"integrator", "review-cycle=2", 0},
		{"", "loom:attempt:=2", 0},
	}
	for _, tc := range cases {
		if got := parseAttemptCounter(tc.agent, tc.label); got != tc.want {
			t.Errorf("parseAttemptCounter(%q, %q) = %d, want %d", tc.agent, tc.label, got, tc.want)
		}
	}
}

func TestRecordedAttemptsTakesMaxNotSum(t *testing.T) {
	labels := []string{
		"loom:attempt:integrator=1",
		"loom:attempt:integrator=3",
		"loom:attempt:integrator=2",
		"loom:attempt:coder=9",
		"review-cycle=4",
	}
	if got := recordedAttempts("integrator", labels); got != 3 {
		t.Fatalf("recordedAttempts = %d, want 3 (max, not sum)", got)
	}
	if got := recordedAttempts("tester", labels); got != 0 {
		t.Fatalf("recordedAttempts for an agent with no counters = %d, want 0", got)
	}
}

// ---------------------------------------------------------------------------
// 2. selection predicate
// ---------------------------------------------------------------------------

func TestUnfinishedTaskSessionsSelection(t *testing.T) {
	mock := clitest.NewMockIssueBackend()
	s, st := newAbandonedRunSupervisor(t, mock)

	seedSession(t, st, "starting-row", "integrator", "T-1", domain.AgentSessionStarting)
	seedSession(t, st, "running-row", "integrator", "T-2", domain.AgentSessionRunning)
	seedSession(t, st, "finished-row", "integrator", "T-3", domain.AgentSessionRunning)
	finishSession(t, st, "finished-row")
	seedSession(t, st, "completed-row", "integrator", "T-4", domain.AgentSessionCompleted)
	seedSession(t, st, "live-row", "integrator", "T-5", domain.AgentSessionRunning)
	seedSession(t, st, "other-agent-row", "coder", "T-6", domain.AgentSessionRunning)

	// An orchestration-kind row must never be selected.
	if _, err := st.AgentSessions().Create(context.Background(), store.AgentSessionCreate{
		WorkspaceKey: "WS", SessionID: "orchestration-row", AgentID: "integrator",
		Kind: domain.AgentSessionKindOrchestration, TaskID: "T-7", Status: domain.AgentSessionRunning,
	}); err != nil {
		t.Fatalf("seed orchestration session: %v", err)
	}

	live := &AgentProcess{Entry: cfgpkg.AgentEntry{Worktree: "integrator"}}
	live.AgentSessionID = "live-row"
	s.Agents = []*AgentProcess{live}

	got := s.unfinishedTaskSessions(context.Background(), store.AgentSessionFilter{
		AgentID: "integrator",
		Kind:    domain.AgentSessionKindTask,
	})

	var ids []string
	for _, sess := range got {
		ids = append(ids, sess.SessionID)
	}
	if len(ids) != 2 {
		t.Fatalf("selected %v, want exactly [starting-row running-row]", ids)
	}
	for _, want := range []string{"starting-row", "running-row"} {
		found := false
		for _, id := range ids {
			if id == want {
				found = true
			}
		}
		if !found {
			t.Errorf("selection %v is missing %s", ids, want)
		}
	}
}

func TestUnfinishedTaskSessionsIsOldestFirstAndCapped(t *testing.T) {
	mock := clitest.NewMockIssueBackend()
	s, st := newAbandonedRunSupervisor(t, mock)

	// The store lists newest-first; the recorder must invert that so counters
	// advance in the order the runs actually happened.
	for i := 0; i < maxAbandonedPerPass+5; i++ {
		seedSession(t, st, fmt.Sprintf("row-%02d", i), "integrator", "T-1", domain.AgentSessionRunning)
	}
	got := s.unfinishedTaskSessions(context.Background(), store.AgentSessionFilter{
		AgentID: "integrator",
		Kind:    domain.AgentSessionKindTask,
	})
	if len(got) != maxAbandonedPerPass {
		t.Fatalf("selected %d rows, want the cap %d", len(got), maxAbandonedPerPass)
	}
	for i := 1; i < len(got); i++ {
		if sessionStartOrder(got[i]).Before(sessionStartOrder(got[i-1])) {
			t.Fatalf("selection is not oldest-first at index %d", i)
		}
	}
}

// ---------------------------------------------------------------------------
// 3. happy path
// ---------------------------------------------------------------------------

func TestRecordAbandonedRunsForAgentWritesEvidenceAndLatches(t *testing.T) {
	mock := clitest.NewMockIssueBackend()
	mock.GetResult = issueWithLabels("T-1")
	s, st := newAbandonedRunSupervisor(t, mock)
	seedSession(t, st, "sess-1", "integrator", "T-1", domain.AgentSessionRunning)

	s.recordAbandonedRunsForAgent(ownedAgent("integrator"))

	texts := commentTexts(mock)
	if len(texts) != 1 {
		t.Fatalf("AddComment called %d times, want 1", len(texts))
	}
	if !strings.Contains(texts[0], abandonedRunMarker+"sess-1") {
		t.Errorf("comment does not embed the dedupe marker:\n%s", texts[0])
	}
	if !strings.Contains(texts[0], "Run ended without reporting an outcome") {
		t.Errorf("comment is missing its headline:\n%s", texts[0])
	}
	if labels := addedLabels(mock); len(labels) != 1 || labels[0] != "loom:attempt:integrator=1" {
		t.Errorf("AddLabel calls = %v, want [loom:attempt:integrator=1]", labels)
	}

	latched := getSession(t, st, "sess-1")
	if latched.Status != domain.AgentSessionFailed {
		t.Errorf("session status = %q, want failed", latched.Status)
	}
	if latched.ErrorClass != abandonedRunErrorClass {
		t.Errorf("session error class = %q, want %q", latched.ErrorClass, abandonedRunErrorClass)
	}
	if latched.FinishedAt == nil {
		t.Error("session FinishedAt is nil, want it stamped")
	}
	if latched.ExitCode == nil || *latched.ExitCode != -1 {
		t.Errorf("session exit code = %v, want -1", latched.ExitCode)
	}
}

// ---------------------------------------------------------------------------
// 4. idempotence — the load-bearing test
// ---------------------------------------------------------------------------

func TestRecordAbandonedRunIsIdempotentAcrossACrashedLatch(t *testing.T) {
	mock := clitest.NewMockIssueBackend()
	mock.GetResult = issueWithLabels("T-1")
	// The task's comment list reflects whatever the recorder wrote.
	var stored []backend.CommentData
	mock.ListCommentsFn = func(context.Context, string) ([]backend.CommentData, error) {
		return stored, nil
	}
	mock.AddCommentFn = func(_ context.Context, params backend.CommentAddParams) (*backend.CommentData, error) {
		stored = append(stored, backend.CommentData{IssueID: params.IssueID, Text: params.Text})
		return &backend.CommentData{IssueID: params.IssueID, Text: params.Text}, nil
	}
	s, st := newAbandonedRunSupervisor(t, mock)
	seedSession(t, st, "sess-1", "integrator", "T-1", domain.AgentSessionRunning)

	s.recordAbandonedRunsForTask(ownedAgent("integrator"), "T-1")
	// Simulate a crash after the comment but before the latch stuck.
	unfinishSession(t, st, "sess-1")
	s.recordAbandonedRunsForTask(ownedAgent("integrator"), "T-1")

	if len(stored) != 1 {
		t.Fatalf("wrote %d comments across two passes, want exactly 1", len(stored))
	}
	if labels := addedLabels(mock); len(labels) != 1 || labels[0] != "loom:attempt:integrator=1" {
		t.Fatalf("AddLabel calls = %v, want a single [loom:attempt:integrator=1]", labels)
	}
	if getSession(t, st, "sess-1").FinishedAt == nil {
		t.Error("second pass did not latch the row")
	}
}

// ---------------------------------------------------------------------------
// 5. counter advance
// ---------------------------------------------------------------------------

func TestRecordAbandonedRunAdvancesTheCounterAndDropsTheOldOne(t *testing.T) {
	mock := clitest.NewMockIssueBackend()
	mock.GetResult = issueWithLabels("T-1", "loom:attempt:integrator=1", "review-cycle=2")
	s, st := newAbandonedRunSupervisor(t, mock)
	seedSession(t, st, "sess-2", "integrator", "T-1", domain.AgentSessionRunning)

	s.recordAbandonedRunsForAgent(ownedAgent("integrator"))

	if labels := addedLabels(mock); len(labels) != 1 || labels[0] != "loom:attempt:integrator=2" {
		t.Fatalf("AddLabel calls = %v, want [loom:attempt:integrator=2]", labels)
	}
	if removed := removedLabels(mock); len(removed) != 1 || removed[0] != "loom:attempt:integrator=1" {
		t.Fatalf("RemoveLabel calls = %v, want [loom:attempt:integrator=1]", removed)
	}
}

// ---------------------------------------------------------------------------
// 6 & 7. terminal issue, empty task id
// ---------------------------------------------------------------------------

func TestRecordAbandonedRunLatchesWithoutWritingOnTerminalIssue(t *testing.T) {
	mock := clitest.NewMockIssueBackend()
	closed := issueWithLabels("T-1")
	closed.Status = "closed"
	mock.GetResult = closed
	s, st := newAbandonedRunSupervisor(t, mock)
	seedSession(t, st, "sess-1", "integrator", "T-1", domain.AgentSessionRunning)

	s.recordAbandonedRunsForAgent(ownedAgent("integrator"))

	if mock.Called("AddComment") || mock.Called("AddLabel") {
		t.Error("terminal issue received evidence writes; want none")
	}
	if getSession(t, st, "sess-1").FinishedAt == nil {
		t.Error("terminal-issue row was not latched")
	}
}

func TestRecordAbandonedRunLatchesRowWithNoTaskWithoutIssueWrites(t *testing.T) {
	mock := clitest.NewMockIssueBackend()
	s, st := newAbandonedRunSupervisor(t, mock)
	seedSession(t, st, "sess-1", "integrator", "", domain.AgentSessionRunning)

	s.recordAbandonedRunsForAgent(ownedAgent("integrator"))

	if len(mock.Calls) != 0 {
		t.Errorf("issue backend saw %v, want no calls for a row with no task", mock.Calls)
	}
	if getSession(t, st, "sess-1").FinishedAt == nil {
		t.Error("row with no task was not latched")
	}
}

// ---------------------------------------------------------------------------
// 8. write failure does not latch
// ---------------------------------------------------------------------------

func TestRecordAbandonedRunDoesNotLatchWhenTheCommentFails(t *testing.T) {
	mock := clitest.NewMockIssueBackend()
	mock.GetResult = issueWithLabels("T-1")
	failing := true
	written := 0
	mock.AddCommentFn = func(_ context.Context, params backend.CommentAddParams) (*backend.CommentData, error) {
		if failing {
			return nil, errors.New("backend down")
		}
		written++
		return &backend.CommentData{IssueID: params.IssueID, Text: params.Text}, nil
	}
	s, st := newAbandonedRunSupervisor(t, mock)
	seedSession(t, st, "sess-1", "integrator", "T-1", domain.AgentSessionRunning)

	s.recordAbandonedRunsForTask(ownedAgent("integrator"), "T-1")

	if getSession(t, st, "sess-1").FinishedAt != nil {
		t.Fatal("row was latched despite a failed comment write")
	}

	// Second pass, backend healthy: the evidence lands and the row latches.
	failing = false
	s.recordAbandonedRunsForTask(ownedAgent("integrator"), "T-1")
	if written != 1 {
		t.Fatalf("retry wrote %d comments, want 1", written)
	}
	if getSession(t, st, "sess-1").FinishedAt == nil {
		t.Error("retry did not latch the row")
	}
}

// ---------------------------------------------------------------------------
// 9 & 10. guards
// ---------------------------------------------------------------------------

func TestRecordAbandonedRunsSkipsWithoutControlPlaneOrOwnership(t *testing.T) {
	t.Run("no control plane", func(t *testing.T) {
		mock := clitest.NewMockIssueBackend()
		s := &Supervisor{ConfigSnapshot: func() *cfgpkg.DaemonConfig { return &cfgpkg.DaemonConfig{} }, IssueBackend: mock}
		s.recordAbandonedRunsForAgent(ownedAgent("integrator"))
		s.recordAbandonedRunsForTask(ownedAgent("integrator"), "T-1")
		if len(mock.Calls) != 0 {
			t.Errorf("issue backend saw %v, want no calls without a control plane", mock.Calls)
		}
	})

	t.Run("no ownership lease", func(t *testing.T) {
		mock := clitest.NewMockIssueBackend()
		mock.GetResult = issueWithLabels("T-1")
		s, st := newAbandonedRunSupervisor(t, mock)
		seedSession(t, st, "sess-1", "integrator", "T-1", domain.AgentSessionRunning)

		ap := &AgentProcess{Entry: cfgpkg.AgentEntry{Worktree: "integrator"}} // no lease token
		s.recordAbandonedRunsForAgent(ap)

		if len(mock.Calls) != 0 {
			t.Errorf("issue backend saw %v, want no calls without the ownership lease", mock.Calls)
		}
		if getSession(t, st, "sess-1").FinishedAt != nil {
			t.Error("row was latched without proof of exclusivity")
		}
	})
}

// countingAgentSessionStore counts List calls so the once-per-process guard can
// be asserted directly.
type countingAgentSessionStore struct {
	store.AgentSessionStore
	lists atomic.Int64
}

func (c *countingAgentSessionStore) List(ctx context.Context, ws string, filter store.AgentSessionFilter) ([]*domain.AgentSession, error) {
	c.lists.Add(1)
	return c.AgentSessionStore.List(ctx, ws, filter)
}

func TestRecordAbandonedRunsForAgentRunsOncePerProcess(t *testing.T) {
	mock := clitest.NewMockIssueBackend()
	mock.GetResult = issueWithLabels("T-1")
	base := memstore.New()
	s := newControlPlaneTestSupervisor(base)
	s.IssueBackend = mock
	counting := &countingAgentSessionStore{AgentSessionStore: base.AgentSessions()}
	s.ControlStore = &controlPlaneStoreOverrides{Store: base, sessions: counting}
	seedSession(t, base, "sess-1", "integrator", "T-1", domain.AgentSessionRunning)

	ap := ownedAgent("integrator")
	s.recordAbandonedRunsForAgent(ap)
	s.recordAbandonedRunsForAgent(ap) // second supervise cycle

	// Two List calls (one per non-terminal status), from the single reconcile.
	if got := counting.lists.Load(); got != 2 {
		t.Fatalf("agent-session List called %d times, want 2 (one reconcile)", got)
	}
	if len(commentTexts(mock)) != 1 {
		t.Fatalf("wrote %d comments across two cycles, want 1", len(commentTexts(mock)))
	}
}

// ---------------------------------------------------------------------------
// 11. regression guard: the common path stays silent
// ---------------------------------------------------------------------------

func TestRecordAbandonedRunsIsSilentForRunsThatFinishedNormally(t *testing.T) {
	mock := clitest.NewMockIssueBackend()
	mock.GetResult = issueWithLabels("T-1")
	s, st := newAbandonedRunSupervisor(t, mock)
	seedSession(t, st, "sess-1", "integrator", "T-1", domain.AgentSessionRunning)
	finishSession(t, st, "sess-1")

	s.recordAbandonedRunsForAgent(ownedAgent("integrator"))
	s.recordAbandonedRunsForTask(ownedAgent("integrator"), "T-1")

	if len(mock.Calls) != 0 {
		t.Errorf("a normally finished run produced issue writes %v, want none", mock.Calls)
	}
}

func TestFormatAbandonedRunCommentIsASCIIAndCarriesTheMarker(t *testing.T) {
	sess := &domain.AgentSession{
		SessionID: "617ef65c-1111-2222-3333-444444444444",
		AgentID:   "integrator",
		NodeID:    "loom-supervisor-host-4711",
		TaskID:    "PUPPET-20",
		Status:    domain.AgentSessionRunning,
		StartedAt: time.Date(2026, 8, 15, 10, 40, 58, 0, time.UTC),
	}
	body := formatAbandonedRunComment(sess, 2)
	for _, want := range []string{
		"PUPPET-20",
		"| attempt | 2 |",
		"loom:attempt:integrator=2",
		abandonedRunMarker + sess.SessionID,
		"2026-08-15T10:40:58Z",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("comment body is missing %q:\n%s", want, body)
		}
	}
	for _, r := range body {
		if r > 127 {
			t.Fatalf("comment body contains non-ASCII rune %q", r)
		}
	}
}

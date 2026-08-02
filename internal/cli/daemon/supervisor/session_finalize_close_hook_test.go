package supervisor

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
	"github.com/tysonthomas9/loomcli/internal/domain"
)

// closeRecordingBackend models fleet-db's close endpoint closely enough to
// pin the supervisor's side of the contract: the first close transitions the
// issue, and a SECOND close of the same issue succeeds rather than
// conflicting. That is not mock convenience — fleet-db's handler swallows its
// own service.ErrAlreadyClosed and replies 200 with the current issue
// (internal/api/issues.go, closeIssue), which is exactly what
// FleetBackend.Close sees on the wire for both the fleetdb and fleet backends.
type closeRecordingBackend struct {
	*clitest.MockIssueBackend
	mu       sync.Mutex
	sequence []string
	closed   map[string]bool
	params   []backend.CloseParams
	// closeErr, when set, is returned instead — used for the conflicts that
	// must still fail (open blockers, dependencies).
	closeErr error
}

func newCloseRecordingBackend(closeErr error) *closeRecordingBackend {
	cb := &closeRecordingBackend{
		MockIssueBackend: clitest.NewMockIssueBackend(),
		closed:           map[string]bool{},
		closeErr:         closeErr,
	}
	cb.AddLabelFn = func(_ context.Context, _ string, label string) error {
		cb.mu.Lock()
		defer cb.mu.Unlock()
		cb.sequence = append(cb.sequence, "label:"+label)
		return nil
	}
	cb.CloseFn = func(_ context.Context, id string, p backend.CloseParams) (*backend.CloseResult, error) {
		cb.mu.Lock()
		defer cb.mu.Unlock()
		cb.sequence = append(cb.sequence, "close")
		cb.params = append(cb.params, p)
		if cb.closeErr != nil {
			return nil, cb.closeErr
		}
		// Already closed: fleet-db still answers 200 with the current issue.
		closed := backend.IssueData{ID: id, Status: "closed"}
		cb.closed[id] = true
		return &backend.CloseResult{Closed: &closed, Unblocked: []backend.IssueData{}}, nil
	}
	return cb
}

func (c *closeRecordingBackend) seq() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.sequence...)
}

func closePipeline() *domain.AgentHooks {
	return &domain.AgentHooks{OnComplete: []domain.AgentHookAction{
		{Type: domain.AgentHookActionAddLabel, Value: "reviewed"},
		{Type: domain.AgentHookActionClose},
	}}
}

// TestRunCompletionHooks_ClosesLastWithReasonAndSession pins that the close is
// the terminal action and carries the supervisor's attribution.
func TestRunCompletionHooks_ClosesLastWithReasonAndSession(t *testing.T) {
	cb := newCloseRecordingBackend(nil)
	s := &Supervisor{IssueBackend: cb}
	ap := newHookAgentProcess(t, "T-1", closePipeline())
	ap.AgentSessionID = "sess-1"

	if got := s.runCompletionHooks(ap, 0); got != 0 {
		t.Fatalf("exit code = %d, want the clean exit preserved", got)
	}
	want := []string{"label:reviewed", "close"}
	if got := cb.seq(); !equalStrings(got, want) {
		t.Fatalf("write order = %v, want %v", got, want)
	}
	cb.mu.Lock()
	defer cb.mu.Unlock()
	if len(cb.params) != 1 {
		t.Fatalf("Close calls = %d, want 1", len(cb.params))
	}
	if cb.params[0].Reason != "completed by agent critic" {
		t.Errorf("close reason = %q, want the agent attribution", cb.params[0].Reason)
	}
	if cb.params[0].Session != "sess-1" {
		t.Errorf("close session = %q, want sess-1", cb.params[0].Session)
	}
}

// TestRunCompletionHooks_ClosingAnAlreadyClosedTaskIsNotAFailure is the
// idempotency pin: a task the agent's own prompt (or a human) already closed
// must not demote an otherwise clean run. fleet-db makes the close endpoint
// idempotent server-side, so the supervisor needs no client-side tolerance —
// this test fails if either side of that contract is dropped.
func TestRunCompletionHooks_ClosingAnAlreadyClosedTaskIsNotAFailure(t *testing.T) {
	cb := newCloseRecordingBackend(nil)
	s := &Supervisor{IssueBackend: cb}

	// First run closes the task.
	first := newHookAgentProcess(t, "T-1", closePipeline())
	first.AgentSessionID = "sess-1"
	if got := s.runCompletionHooks(first, 0); got != 0 {
		t.Fatalf("first run: exit code = %d, want 0", got)
	}
	if !cb.closed["T-1"] {
		t.Fatal("first run did not close the task; test setup is wrong")
	}

	// Second run over the same, now-closed task.
	second := newHookAgentProcess(t, "T-1", closePipeline())
	second.AgentSessionID = "sess-2"
	if got := s.runCompletionHooks(second, 0); got != 0 {
		t.Fatalf("closing an already-closed task demoted the run (exit %d)", got)
	}
	if second.LastError != nil {
		t.Fatalf("LastError = %+v, want nil for an already-closed task", second.LastError)
	}
}

// TestRunCompletionHooks_RealCloseConflictStillFails is the other half: only
// "already closed" is benign. A close refused for open blockers or
// dependencies is a genuine failure and must demote the run.
func TestRunCompletionHooks_RealCloseConflictStillFails(t *testing.T) {
	cb := newCloseRecordingBackend(backend.ErrConflict("Close", "issue has open blockers"))
	s := &Supervisor{IssueBackend: cb}
	ap := newHookAgentProcess(t, "T-1", closePipeline())
	ap.AgentSessionID = "sess-1"

	if got := s.runCompletionHooks(ap, 0); got != -1 {
		t.Fatalf("exit code = %d, want -1 for a blocked close", got)
	}
	if ap.LastError == nil ||
		ap.LastError.Class != agenterr.OutcomeFromDomain(agenterr.CompletionHookFailureOutcome) {
		t.Fatalf("LastError = %+v, want CompletionHookFailure", ap.LastError)
	}
	if !strings.Contains(ap.LastError.Message, "open blockers") {
		t.Errorf("LastError.Message = %q, want it to carry the backend conflict", ap.LastError.Message)
	}
	// The label landed before the close was attempted: the hand-off artifact is
	// never withheld because the terminal transition failed.
	want := []string{"label:reviewed", "close"}
	if got := cb.seq(); !equalStrings(got, want) {
		t.Fatalf("write order = %v, want %v", got, want)
	}
}

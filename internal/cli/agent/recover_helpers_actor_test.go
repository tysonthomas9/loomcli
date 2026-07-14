package agent

import (
	"context"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
)

// actorReleaseMockBackend wraps the standard mock backend with the optional
// ReleaseIssueAsActor method so resetTask's actor-scoped release path can be
// exercised.
type actorReleaseMockBackend struct {
	*clitest.MockIssueBackend

	releaseErr   error
	releaseCalls []struct{ id, actor string }
}

func (m *actorReleaseMockBackend) ReleaseIssueAsActor(_ context.Context, id, actor string) error {
	m.releaseCalls = append(m.releaseCalls, struct{ id, actor string }{id, actor})
	return m.releaseErr
}

// countCalls returns how many recorded calls match the given method name.
func countCalls(mb *clitest.MockIssueBackend, method string) int {
	n := 0
	for _, c := range mb.Calls {
		if c.Method == method {
			n++
		}
	}
	return n
}

func TestResetTask_ActorRelease_Success_NoUpdate(t *testing.T) {
	deps, _, _, _, mb := NewTestDeps(t)
	wrapped := &actorReleaseMockBackend{MockIssueBackend: mb}
	deps.IssueBackend = wrapped

	resetTask(deps, "task-1", "worker-a")

	if len(wrapped.releaseCalls) != 1 {
		t.Fatalf("expected 1 ReleaseIssueAsActor call, got %+v", wrapped.releaseCalls)
	}
	if wrapped.releaseCalls[0].id != "task-1" || wrapped.releaseCalls[0].actor != "worker-a" {
		t.Errorf("unexpected release args: %+v", wrapped.releaseCalls[0])
	}
	if n := countCalls(mb, "Update"); n != 0 {
		t.Errorf("expected no Update calls after successful actor release, got %d", n)
	}
}

func TestResetTask_ActorRelease_Conflict_LeavesTaskClaimed(t *testing.T) {
	deps, _, _, _, mb := NewTestDeps(t)
	wrapped := &actorReleaseMockBackend{
		MockIssueBackend: mb,
		releaseErr:       backend.ErrConflict("ReleaseIssue", "lock held by another worker"),
	}
	deps.IssueBackend = wrapped

	resetTask(deps, "task-1", "worker-a")

	if len(wrapped.releaseCalls) != 1 {
		t.Fatalf("expected 1 ReleaseIssueAsActor call, got %+v", wrapped.releaseCalls)
	}
	if n := countCalls(mb, "Update"); n != 0 {
		t.Errorf("conflict must leave the task alone; got %d Update calls", n)
	}
}

func TestResetTask_ActorRelease_TransientError_LeavesTaskClaimed(t *testing.T) {
	deps, _, _, _, mb := NewTestDeps(t)
	wrapped := &actorReleaseMockBackend{
		MockIssueBackend: mb,
		releaseErr:       backend.ErrInternal("ReleaseIssue", "boom", nil),
	}
	deps.IssueBackend = wrapped

	resetTask(deps, "task-1", "worker-a")

	if len(wrapped.releaseCalls) != 1 {
		t.Fatalf("expected 1 ReleaseIssueAsActor call, got %+v", wrapped.releaseCalls)
	}
	// Transient (non-conflict, non-unsupported) errors must NOT fall back to
	// the unscoped update — that could un-claim a live sibling's lock.
	if n := countCalls(mb, "Update"); n != 0 {
		t.Fatalf("expected no fallback Update on transient error, got %d", n)
	}
}

func TestResetTask_ActorRelease_NotImplemented_FallsBackToUpdate(t *testing.T) {
	deps, _, _, _, mb := NewTestDeps(t)
	wrapped := &actorReleaseMockBackend{
		MockIssueBackend: mb,
		releaseErr:       backend.ErrNotImplemented("ReleaseIssue", "backend does not support actor-scoped release"),
	}
	deps.IssueBackend = wrapped

	resetTask(deps, "task-1", "worker-a")

	if len(wrapped.releaseCalls) != 1 {
		t.Fatalf("expected 1 ReleaseIssueAsActor call, got %+v", wrapped.releaseCalls)
	}
	if n := countCalls(mb, "Update"); n != 1 {
		t.Fatalf("expected fallback Update call, got %d", n)
	}
	// The fallback Update must reset status to open and clear the assignee.
	for _, c := range mb.Calls {
		if c.Method != "Update" {
			continue
		}
		if id, _ := c.Args[0].(string); id != "task-1" {
			t.Errorf("Update id = %v, want task-1", c.Args[0])
		}
		params, ok := c.Args[1].(backend.UpdateParams)
		if !ok {
			t.Fatalf("Update args[1] is %T, want backend.UpdateParams", c.Args[1])
		}
		if params.Status == nil || *params.Status != "open" {
			t.Errorf("Update status = %v, want open", params.Status)
		}
		if params.Assignee == nil || *params.Assignee != "" {
			t.Errorf("Update assignee = %v, want empty", params.Assignee)
		}
	}
}

func TestResetTask_EmptyActor_UsesUpdateEvenWhenActorCapable(t *testing.T) {
	deps, _, _, _, mb := NewTestDeps(t)
	wrapped := &actorReleaseMockBackend{MockIssueBackend: mb}
	deps.IssueBackend = wrapped

	resetTask(deps, "task-1", "")

	if len(wrapped.releaseCalls) != 0 {
		t.Errorf("ReleaseIssueAsActor should not be called without an actor, got %+v", wrapped.releaseCalls)
	}
	if n := countCalls(mb, "Update"); n != 1 {
		t.Errorf("expected 1 Update call, got %d", n)
	}
}

func TestResetTask_ActorWithPlainBackend_UsesUpdate(t *testing.T) {
	deps, _, _, _, mb := NewTestDeps(t)

	resetTask(deps, "task-1", "worker-a")

	if n := countCalls(mb, "Update"); n != 1 {
		t.Errorf("expected 1 Update call on plain backend, got %d", n)
	}
}

package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
)

// actorReleasingBackend is a MockIssueBackend that can also scope a release to
// an actor — the shape every backend has once the serve client implements
// backend.ActorReleaser. The bare mock stands in for the backends that cannot.
type actorReleasingBackend struct {
	*clitest.MockIssueBackend
	releases []struct{ id, actor string }
	relErr   error
}

func (b *actorReleasingBackend) ReleaseIssueAsActor(_ context.Context, id, actor string) error {
	b.releases = append(b.releases, struct{ id, actor string }{id, actor})
	return b.relErr
}

func depsWithActorRelease(t *testing.T) (*cli.Deps, *actorReleasingBackend) {
	t.Helper()
	deps, _, _, _, tracker := NewTestDeps(t)
	be := &actorReleasingBackend{MockIssueBackend: tracker}
	deps.IssueBackend = be
	return deps, be
}

func inProgress(id string) *backend.IssueDetailData {
	return &backend.IssueDetailData{IssueData: backend.IssueData{ID: id, Status: "in_progress"}}
}

// The claim is held in fleet-db under the agent's identity, and fleet-db
// arbitrates by actor. Resetting through the unscoped status transition
// therefore frees whatever lock exists regardless of who holds it — which is
// how stopping one duplicate worker un-claimed the sibling still running.
func TestResetTask_ReleasesUnderTheAgentIdentity(t *testing.T) {
	deps, be := depsWithActorRelease(t)
	be.GetResult = inProgress("task-1")

	resetTask(deps, "task-1", "worker-2")

	if len(be.releases) != 1 {
		t.Fatalf("actor-scoped releases = %d, want 1", len(be.releases))
	}
	if be.releases[0].actor != "worker-2" {
		t.Errorf("actor = %q, want worker-2", be.releases[0].actor)
	}
	if be.Called("Update") {
		t.Error("fell through to the unscoped status transition despite an actor-scoped release succeeding")
	}
}

// A conflict means a live sibling holds the lock. Leaving the task claimed is
// the whole point: the alternative un-claims a worker that is still running.
func TestResetTask_ConflictLeavesTheTaskClaimed(t *testing.T) {
	deps, be := depsWithActorRelease(t)
	be.GetResult = inProgress("task-1")
	be.relErr = backend.ErrConflict("ReleaseIssue", "lock held by worker-9")

	resetTask(deps, "task-1", "worker-2")

	if be.Called("Update") {
		t.Error("un-claimed a task whose lock another worker holds")
	}
}

// A server with no release route can still be reset the old way: this is the
// pre-existing behavior and is no worse than it was.
func TestResetTask_NotImplementedDegradesToStatusUpdate(t *testing.T) {
	deps, be := depsWithActorRelease(t)
	be.GetResult = inProgress("task-1")
	be.relErr = backend.ErrNotImplemented("ReleaseIssue", "no release route")

	resetTask(deps, "task-1", "worker-2")

	if !be.Called("Update") {
		t.Error("task was not reset at all; not-implemented must degrade, not abort")
	}
}

// A transient failure is the one case where the unscoped transition is unsafe:
// the lock may well be held by someone else and we simply could not ask. Leave
// the task claimed and let recovery run again.
func TestResetTask_TransientFailureLeavesTheTaskClaimed(t *testing.T) {
	deps, be := depsWithActorRelease(t)
	be.GetResult = inProgress("task-1")
	be.relErr = backend.ErrUnavailable("ReleaseIssue", "connection refused", errors.New("dial"))

	resetTask(deps, "task-1", "worker-2")

	if be.Called("Update") {
		t.Error("fell back to an unscoped release after a transient failure; that can free a live sibling's lock")
	}
}

// Backends that cannot scope a release keep the legacy path, so nothing
// regresses for them.
func TestResetTask_IncapableBackendUsesStatusUpdate(t *testing.T) {
	deps, _, _, _, tracker := NewTestDeps(t)
	tracker.GetResult = inProgress("task-1")

	resetTask(deps, "task-1", "worker-2")

	if !tracker.Called("Update") {
		t.Error("task was not reset on a backend without actor-scoped release")
	}
}

// An empty actor is the legacy caller. It must not reach the actor-scoped
// release, which would be an unscoped release under an empty identity.
func TestResetTask_EmptyActorSkipsTheActorScopedRelease(t *testing.T) {
	deps, be := depsWithActorRelease(t)
	be.GetResult = inProgress("task-1")

	resetTask(deps, "task-1", "")

	if len(be.releases) != 0 {
		t.Fatalf("actor-scoped releases = %d, want 0", len(be.releases))
	}
	if !be.Called("Update") {
		t.Error("task was not reset")
	}
}

// The status guard runs first either way: a task the agent already moved on
// must not be released back to open.
func TestResetTask_AlreadyReviewIsNotReleased(t *testing.T) {
	deps, be := depsWithActorRelease(t)
	be.GetResult = &backend.IssueDetailData{IssueData: backend.IssueData{ID: "task-1", Status: "review"}}

	resetTask(deps, "task-1", "worker-2")

	if len(be.releases) != 0 {
		t.Errorf("released a task already in review: %v", be.releases)
	}
	if be.Called("Update") {
		t.Error("reset a task already in review")
	}
}

// handleOrphanedTask carries the identity down to resetTask; without it the
// threading stops at the first hop and every orphan is reset unscoped.
func TestHandleOrphanedTask_ThreadsTheActorToReset(t *testing.T) {
	deps, be := depsWithActorRelease(t)
	be.GetResult = inProgress("task-1")

	handleOrphanedTask(deps, t.TempDir(), "task-1", "worker-2", false)

	if len(be.releases) != 1 || be.releases[0].actor != "worker-2" {
		t.Fatalf("releases = %v, want one release as worker-2", be.releases)
	}
}

// releaseFleetIssueLock is the call recovery makes on every exit path. With
// LOOM_SERVER_URL set it went through ReleaseIssueLock, which the serve client
// answers with not-implemented — so the release was a silent no-op and the
// claim sat at in_progress until its TTL expired. Prefer the actor-scoped
// release, which that client can now actually perform.
func TestReleaseFleetIssueLock_PrefersTheActorScopedRelease(t *testing.T) {
	deps, be := depsWithActorRelease(t)

	releaseFleetIssueLock(deps, "worker-2", "task-1")

	if len(be.releases) != 1 {
		t.Fatalf("actor-scoped releases = %d, want 1", len(be.releases))
	}
	if be.releases[0].id != "task-1" || be.releases[0].actor != "worker-2" {
		t.Errorf("release = %v, want (task-1, worker-2)", be.releases[0])
	}
	if be.Called("ReleaseIssueLock") {
		t.Error("used the lock-only release, which the serve client cannot perform")
	}
}

// Backends without the capability keep the old call, so local mode is
// unaffected.
func TestReleaseFleetIssueLock_FallsBackToLockOnlyRelease(t *testing.T) {
	deps, _, _, _, tracker := NewTestDeps(t)

	releaseFleetIssueLock(deps, "worker-2", "task-1")

	if !tracker.Called("ReleaseIssueLock") {
		t.Error("ReleaseIssueLock was not called on a backend without actor-scoped release")
	}
}

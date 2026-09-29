package service

import (
	"context"
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// releasingBackend implements only IssueBackend — the shape a serve instance
// has when its client cannot scope a release to an actor.
type releasingBackend struct {
	backend.IssueBackend
	updates []backend.UpdateParams
	updated []string
	updErr  error
}

func (b *releasingBackend) Update(_ context.Context, id string, params backend.UpdateParams) error {
	b.updated = append(b.updated, id)
	b.updates = append(b.updates, params)
	return b.updErr
}

func (b *releasingBackend) BackendName() string { return "releasing" }

// actorReleasingBackend also implements the actor-scoped release capability.
type actorReleasingBackend struct {
	releasingBackend
	gotID    string
	gotActor string
	relErr   error
}

func (b *actorReleasingBackend) ReleaseIssueAsActor(_ context.Context, id, actor string) error {
	b.gotID, b.gotActor = id, actor
	return b.relErr
}

func newServiceWithBackend(be backend.IssueBackend) *issueServiceImpl {
	return &issueServiceImpl{
		backendFn: func(context.Context) backend.IssueBackend { return be },
	}
}

// The selection rule mirrors the claim side: with an actor AND a capable
// backend, the release must be scoped to the worker that holds the lock.
// Releasing unscoped is how a stopped duplicate un-claimed a live sibling.
func TestReleaseIssue_PrefersTheActorScopedCall(t *testing.T) {
	be := &actorReleasingBackend{}
	svc := newServiceWithBackend(be)

	if err := svc.ReleaseIssue(context.Background(), ReleaseIssueParams{IssueID: "T-1", Actor: "worker-7"}); err != nil {
		t.Fatalf("ReleaseIssue: %v", err)
	}
	if be.gotID != "T-1" || be.gotActor != "worker-7" {
		t.Fatalf("ReleaseIssueAsActor(%q, %q), want (T-1, worker-7)", be.gotID, be.gotActor)
	}
	if len(be.updated) != 0 {
		t.Fatalf("unscoped status transition was used despite the capability: %v", be.updated)
	}
}

// A lock held by another worker is a conflict, not a success: the caller must
// be able to leave the task claimed.
func TestReleaseIssue_ConflictIsNotSwallowed(t *testing.T) {
	be := &actorReleasingBackend{relErr: backend.ErrConflict("ReleaseIssue", "locked by worker-1")}
	svc := newServiceWithBackend(be)

	err := svc.ReleaseIssue(context.Background(), ReleaseIssueParams{IssueID: "T-1", Actor: "worker-7"})
	var svcErr *ServiceError
	if !errors.As(err, &svcErr) || svcErr.Kind != KindConflict {
		t.Fatalf("err = %v, want a conflict ServiceError", err)
	}
	if len(be.updated) != 0 {
		t.Fatalf("fell back to the unscoped transition after a conflict: %v", be.updated)
	}
}

// Legacy paths keep working: no actor supplied (the web UI acting as itself,
// or an older client) releases via the status transition.
func TestReleaseIssue_NoActorUsesStatusTransition(t *testing.T) {
	be := &releasingBackend{}
	svc := newServiceWithBackend(be)

	if err := svc.ReleaseIssue(context.Background(), ReleaseIssueParams{IssueID: "T-1"}); err != nil {
		t.Fatalf("ReleaseIssue: %v", err)
	}
	if len(be.updates) != 1 {
		t.Fatalf("updates = %d, want 1", len(be.updates))
	}
	got := be.updates[0]
	if got.Status == nil || *got.Status != "open" {
		t.Errorf("Status = %v, want open", got.Status)
	}
	if got.Assignee == nil || *got.Assignee != "" {
		t.Errorf("Assignee = %v, want cleared", got.Assignee)
	}
}

// An actor supplied to a backend that cannot scope a release still has to
// release something — an older backend must not turn the request into a no-op
// — but it is a downgrade, so it takes the legacy path rather than failing.
func TestReleaseIssue_ActorOnIncapableBackendFallsBack(t *testing.T) {
	be := &releasingBackend{}
	svc := newServiceWithBackend(be)

	if err := svc.ReleaseIssue(context.Background(), ReleaseIssueParams{IssueID: "T-1", Actor: "worker-7"}); err != nil {
		t.Fatalf("ReleaseIssue: %v", err)
	}
	if len(be.updated) != 1 {
		t.Fatalf("updates = %d, want 1 — the release must not be dropped", len(be.updated))
	}
}

func TestReleaseIssue_RequiresIssueID(t *testing.T) {
	svc := newServiceWithBackend(&actorReleasingBackend{})

	err := svc.ReleaseIssue(context.Background(), ReleaseIssueParams{IssueID: "  ", Actor: "worker-7"})
	var svcErr *ServiceError
	if !errors.As(err, &svcErr) || svcErr.Kind != KindValidation {
		t.Fatalf("err = %v, want a validation ServiceError", err)
	}
}

func TestReleaseIssue_NoBackendIsUnavailable(t *testing.T) {
	svc := &issueServiceImpl{}

	err := svc.ReleaseIssue(context.Background(), ReleaseIssueParams{IssueID: "T-1"})
	var svcErr *ServiceError
	if !errors.As(err, &svcErr) || svcErr.Kind != KindUnavailable {
		t.Fatalf("err = %v, want an unavailable ServiceError", err)
	}
}

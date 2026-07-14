package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// actorFakeBackend extends fakeIssueBackend with the optional actor-scoped
// claim/release methods so service tests can exercise the actor-capable
// path. The base fake (without this wrapper) covers the fallback path.
type actorFakeBackend struct {
	*fakeIssueBackend

	mu sync.Mutex

	claimAsActorErr   error
	claimAsActorCalls []actorCall

	releaseAsActorErr   error
	releaseAsActorCalls []actorCall
}

type actorCall struct {
	id    string
	actor string
}

func (f *actorFakeBackend) ClaimIssueAsActor(_ context.Context, id string, _ time.Duration, actor string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.claimAsActorCalls = append(f.claimAsActorCalls, actorCall{id: id, actor: actor})
	return f.claimAsActorErr
}

func (f *actorFakeBackend) ReleaseIssueAsActor(_ context.Context, id, actor string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.releaseAsActorCalls = append(f.releaseAsActorCalls, actorCall{id: id, actor: actor})
	return f.releaseAsActorErr
}

// newServiceWithBackend mirrors newServiceWithFake but accepts any backend so
// the actor-capable wrapper can be injected.
func newServiceWithBackend(be backend.IssueBackend) IssueService {
	return NewIssueServiceWithBackend(nil, nil, nil, func(_ context.Context) backend.IssueBackend { return be })
}

func claimableDetail() *backend.IssueDetailData {
	now := time.Now().UTC()
	return &backend.IssueDetailData{
		IssueData: backend.IssueData{
			ID: "i-1", Title: "T", Status: "open", Priority: 1, CreatedAt: now, UpdatedAt: now,
		},
	}
}

// --- ClaimIssue actor threading ---

func TestClaimIssue_ActorAndActorCapableBackend_UsesClaimIssueAsActor(t *testing.T) {
	fb := &actorFakeBackend{fakeIssueBackend: &fakeIssueBackend{getResult: claimableDetail()}}
	svc := newServiceWithBackend(fb)

	if _, err := svc.ClaimIssue(context.Background(), ClaimIssueParams{IssueID: "i-1", Actor: "worker-a"}); err != nil {
		t.Fatalf("ClaimIssue: %v", err)
	}
	if len(fb.claimAsActorCalls) != 1 {
		t.Fatalf("expected 1 ClaimIssueAsActor call, got %+v", fb.claimAsActorCalls)
	}
	if fb.claimAsActorCalls[0] != (actorCall{id: "i-1", actor: "worker-a"}) {
		t.Errorf("unexpected ClaimIssueAsActor args: %+v", fb.claimAsActorCalls[0])
	}
	if len(fb.claimCalls) != 0 {
		t.Errorf("plain ClaimIssue should not be called, got %+v", fb.claimCalls)
	}
}

func TestClaimIssue_ActorWithPlainBackend_FallsBackToClaimIssue(t *testing.T) {
	fb := &fakeIssueBackend{getResult: claimableDetail()}
	svc := newServiceWithFake(fb)

	if _, err := svc.ClaimIssue(context.Background(), ClaimIssueParams{IssueID: "i-1", Actor: "worker-a"}); err != nil {
		t.Fatalf("ClaimIssue: %v", err)
	}
	if len(fb.claimCalls) != 1 || fb.claimCalls[0].id != "i-1" {
		t.Fatalf("expected fallback ClaimIssue call for i-1, got %+v", fb.claimCalls)
	}
}

func TestClaimIssue_EmptyActor_UsesLegacyClaimEvenWhenActorCapable(t *testing.T) {
	fb := &actorFakeBackend{fakeIssueBackend: &fakeIssueBackend{getResult: claimableDetail()}}
	svc := newServiceWithBackend(fb)

	if _, err := svc.ClaimIssue(context.Background(), ClaimIssueParams{IssueID: "i-1"}); err != nil {
		t.Fatalf("ClaimIssue: %v", err)
	}
	if len(fb.claimAsActorCalls) != 0 {
		t.Errorf("ClaimIssueAsActor should not be called without an actor, got %+v", fb.claimAsActorCalls)
	}
	if len(fb.claimCalls) != 1 {
		t.Errorf("expected 1 legacy ClaimIssue call, got %+v", fb.claimCalls)
	}
}

// --- ReleaseIssue ---

func TestReleaseIssue_ActorAndActorCapableBackend_UsesReleaseIssueAsActor(t *testing.T) {
	fb := &actorFakeBackend{fakeIssueBackend: &fakeIssueBackend{}}
	svc := newServiceWithBackend(fb)

	if err := svc.ReleaseIssue(context.Background(), ReleaseIssueParams{IssueID: "i-1", Actor: "worker-a"}); err != nil {
		t.Fatalf("ReleaseIssue: %v", err)
	}
	if len(fb.releaseAsActorCalls) != 1 {
		t.Fatalf("expected 1 ReleaseIssueAsActor call, got %+v", fb.releaseAsActorCalls)
	}
	if fb.releaseAsActorCalls[0] != (actorCall{id: "i-1", actor: "worker-a"}) {
		t.Errorf("unexpected ReleaseIssueAsActor args: %+v", fb.releaseAsActorCalls[0])
	}
	if len(fb.updateCalls) != 0 {
		t.Errorf("legacy Update should not be called, got %+v", fb.updateCalls)
	}
}

func TestReleaseIssue_ActorCapableBackend_ConflictMapsTo409(t *testing.T) {
	fb := &actorFakeBackend{
		fakeIssueBackend:  &fakeIssueBackend{},
		releaseAsActorErr: backend.ErrConflict("ReleaseIssue", "lock held by other-worker"),
	}
	svc := newServiceWithBackend(fb)

	err := svc.ReleaseIssue(context.Background(), ReleaseIssueParams{IssueID: "i-1", Actor: "worker-a"})
	var sErr *ServiceError
	if !errors.As(err, &sErr) || sErr.Kind != KindConflict {
		t.Fatalf("expected ConflictError, got %v", err)
	}
	if len(fb.updateCalls) != 0 {
		t.Errorf("conflict must not fall back to Update, got %+v", fb.updateCalls)
	}
}

func TestReleaseIssue_ActorWithPlainBackend_FallsBackToStatusUpdate(t *testing.T) {
	fb := &fakeIssueBackend{}
	svc := newServiceWithFake(fb)

	if err := svc.ReleaseIssue(context.Background(), ReleaseIssueParams{IssueID: "i-1", Actor: "worker-a"}); err != nil {
		t.Fatalf("ReleaseIssue: %v", err)
	}
	if len(fb.updateCalls) != 1 {
		t.Fatalf("expected 1 Update call, got %+v", fb.updateCalls)
	}
	u := fb.updateCalls[0]
	if u.id != "i-1" || u.params.Status == nil || *u.params.Status != "open" ||
		u.params.Assignee == nil || *u.params.Assignee != "" {
		t.Errorf("unexpected Update call: %+v", u)
	}
}

func TestReleaseIssue_EmptyActor_LegacyStatusUpdateEvenWhenActorCapable(t *testing.T) {
	fb := &actorFakeBackend{fakeIssueBackend: &fakeIssueBackend{}}
	svc := newServiceWithBackend(fb)

	if err := svc.ReleaseIssue(context.Background(), ReleaseIssueParams{IssueID: "i-1"}); err != nil {
		t.Fatalf("ReleaseIssue: %v", err)
	}
	if len(fb.releaseAsActorCalls) != 0 {
		t.Errorf("ReleaseIssueAsActor should not be called without an actor, got %+v", fb.releaseAsActorCalls)
	}
	if len(fb.updateCalls) != 1 {
		t.Fatalf("expected 1 legacy Update call, got %+v", fb.updateCalls)
	}
}

func TestReleaseIssue_EmptyID_Validation(t *testing.T) {
	svc := newServiceWithFake(&fakeIssueBackend{})
	err := svc.ReleaseIssue(context.Background(), ReleaseIssueParams{IssueID: "  "})
	var sErr *ServiceError
	if !errors.As(err, &sErr) || sErr.Kind != KindValidation {
		t.Fatalf("expected ValidationError, got %v", err)
	}
}

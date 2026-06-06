package driver

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

func TestClaimReadyTaskClaimsFirstAvailableAsActor(t *testing.T) {
	ctx := context.Background()
	fake := &fakeReadyIssueBackend{
		ready: []backend.IssueData{
			{ID: "TEST-1", Title: "already claimed", Parent: "EPIC-1"},
			{ID: "TEST-2", Title: "available", Priority: 2, Parent: "EPIC-1", Labels: []string{"repo:core"}},
		},
		claimErrs: map[string]error{"TEST-1": backend.ErrConflict("ClaimIssue", "already claimed")},
	}

	claimed, err := ClaimReadyTask(ctx, fake, TaskClaimOptions{
		EpicID:  "EPIC-1",
		Actor:   "driver-run:run-1",
		Limit:   10,
		LockTTL: time.Minute,
	})
	if err != nil {
		t.Fatalf("ClaimReadyTask: %v", err)
	}
	if claimed == nil || claimed.ID != "TEST-2" || claimed.Status != "in_progress" || claimed.Parent != "EPIC-1" || claimed.ClaimedBy != "driver-run:run-1" {
		t.Fatalf("claimed = %+v, want TEST-2 claimed by driver-run", claimed)
	}
	if len(fake.readyCalls) != 1 || fake.readyCalls[0].ParentID != "EPIC-1" || fake.readyCalls[0].Limit != 10 {
		t.Fatalf("ready calls = %+v, want parent EPIC-1 limit 10", fake.readyCalls)
	}
	if len(fake.actorClaims) != 2 {
		t.Fatalf("actor claims = %+v, want two attempts", fake.actorClaims)
	}
	if fake.actorClaims[1].id != "TEST-2" || fake.actorClaims[1].actor != "driver-run:run-1" || fake.actorClaims[1].ttl != time.Minute {
		t.Fatalf("second actor claim = %+v, want TEST-2 actor/ttl", fake.actorClaims[1])
	}
}

func TestClaimReadyTaskReturnsNilWhenNoReadyTasks(t *testing.T) {
	claimed, err := ClaimReadyTask(context.Background(), &fakeReadyIssueBackend{}, TaskClaimOptions{EpicID: "EPIC-1"})
	if err != nil {
		t.Fatalf("ClaimReadyTask no ready: %v", err)
	}
	if claimed != nil {
		t.Fatalf("claimed = %+v, want nil", claimed)
	}
}

func TestClaimReadyTaskReturnsNonConflictClaimError(t *testing.T) {
	fake := &fakeReadyIssueBackend{
		ready:     []backend.IssueData{{ID: "TEST-1"}},
		claimErrs: map[string]error{"TEST-1": errors.New("network down")},
	}

	if _, err := ClaimReadyTask(context.Background(), fake, TaskClaimOptions{}); err == nil {
		t.Fatal("ClaimReadyTask swallowed non-conflict claim error")
	}
}

func TestCompleteTaskClosesIssue(t *testing.T) {
	fake := &fakeReadyIssueBackend{}

	result, err := CompleteTask(context.Background(), fake, TaskCompleteOptions{
		TaskID:  "TEST-1",
		Reason:  "patch accepted",
		Session: "session-1",
		Force:   true,
	})
	if err != nil {
		t.Fatalf("CompleteTask: %v", err)
	}
	if result.ID != "TEST-1" || result.Status != "closed" || result.Reason != "patch accepted" {
		t.Fatalf("result = %+v, want closed TEST-1", result)
	}
	if len(fake.closeCalls) != 1 || fake.closeCalls[0].id != "TEST-1" || fake.closeCalls[0].params.Reason != "patch accepted" || !fake.closeCalls[0].params.Force {
		t.Fatalf("close calls = %+v, want TEST-1 reason force", fake.closeCalls)
	}
}

func TestReleaseTaskUsesActorScopedRelease(t *testing.T) {
	fake := &fakeReadyIssueBackend{}

	result, err := ReleaseTask(context.Background(), fake, TaskReleaseOptions{TaskID: "TEST-1", Actor: "driver-run:run-1"})
	if err != nil {
		t.Fatalf("ReleaseTask: %v", err)
	}
	if result.ID != "TEST-1" || !result.Released {
		t.Fatalf("result = %+v, want released TEST-1", result)
	}
	if len(fake.actorReleases) != 1 || fake.actorReleases[0].id != "TEST-1" || fake.actorReleases[0].actor != "driver-run:run-1" {
		t.Fatalf("actor releases = %+v, want TEST-1 driver actor", fake.actorReleases)
	}
	if len(fake.releases) != 0 {
		t.Fatalf("plain releases = %+v, want none when actor release is supported", fake.releases)
	}
}

type fakeReadyIssueBackend struct {
	backend.IssueBackend
	ready         []backend.IssueData
	readyCalls    []backend.ReadyOpts
	claims        []claimCall
	actorClaims   []claimCall
	closeCalls    []closeCall
	releases      []releaseCall
	actorReleases []releaseCall
	claimErrs     map[string]error
}

type claimCall struct {
	id    string
	ttl   time.Duration
	actor string
}

type closeCall struct {
	id     string
	params backend.CloseParams
}

type releaseCall struct {
	id    string
	actor string
}

func (f *fakeReadyIssueBackend) Ready(_ context.Context, opts backend.ReadyOpts) ([]backend.IssueData, error) {
	f.readyCalls = append(f.readyCalls, opts)
	return append([]backend.IssueData(nil), f.ready...), nil
}

func (f *fakeReadyIssueBackend) ClaimIssue(_ context.Context, id string, ttl time.Duration) error {
	f.claims = append(f.claims, claimCall{id: id, ttl: ttl})
	return f.claimErrs[id]
}

func (f *fakeReadyIssueBackend) ClaimIssueAsActor(_ context.Context, id string, ttl time.Duration, actor string) error {
	f.actorClaims = append(f.actorClaims, claimCall{id: id, ttl: ttl, actor: actor})
	return f.claimErrs[id]
}

func (f *fakeReadyIssueBackend) Close(_ context.Context, id string, params backend.CloseParams) (*backend.CloseResult, error) {
	f.closeCalls = append(f.closeCalls, closeCall{id: id, params: params})
	return &backend.CloseResult{Closed: &backend.IssueData{ID: id, Status: "closed"}}, nil
}

func (f *fakeReadyIssueBackend) ReleaseIssueLock(_ context.Context, id, actor string) error {
	f.releases = append(f.releases, releaseCall{id: id, actor: actor})
	return nil
}

func (f *fakeReadyIssueBackend) ReleaseIssueAsActor(_ context.Context, id, actor string) error {
	f.actorReleases = append(f.actorReleases, releaseCall{id: id, actor: actor})
	return nil
}

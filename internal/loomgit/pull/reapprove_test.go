package pull

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit/apply"
	"github.com/tysonthomas9/loomcli/internal/loomgit/review"
)

func approveC1(t *testing.T, f *fixture) int64 {
	t.Helper()
	v, err := review.SubmitForLead(context.Background(), f.store, "W", "C1", 1, f.source, "approve", "",
		review.Actor{Kind: "human", ID: "reviewer"}, "L")
	if err != nil {
		t.Fatal(err)
	}
	return v.ID
}

func unapplyC1(t *testing.T, f *fixture, requestID string) {
	t.Helper()
	got, err := f.service.Restack(context.Background(), RestackRequest{Workspace: "W", Lead: "L", Repo: "repo",
		BaseSHA: f.base, RemoveChange: "C1", RequestID: requestID})
	if err != nil || got.HeadSHA != f.base || f.git(t, "rev-parse", "HEAD") != f.base {
		t.Fatalf("Unapply: %+v, %v", got, err)
	}
}

// Real Unapply after the follow was marked applied: a re-approval re-arms it.
func TestReapprovalAfterRealUnapplyIsFollowed(t *testing.T) {
	f := newFixture(t)
	preparePull(t, f)
	ctx := context.Background()
	first := approveC1(t, f)
	if _, err := f.applier.Apply(ctx, apply.Request{Workspace: "W", Lead: "L", Change: "C1", Revision: 1, RequestID: fmt.Sprintf("approval:%d", first)}); err != nil {
		t.Fatal(err)
	}
	pending, err := f.store.PendingApprovals(ctx, "W", "L")
	if err != nil || len(pending) != 1 {
		t.Fatalf("initial follow: %+v, %v", pending, err)
	}
	if err := f.store.SetApprovalFollow(ctx, pending[0], "applied", nil); err != nil {
		t.Fatal(err)
	}
	unapplyC1(t, f, "unapply-c1")
	second := approveC1(t, f)
	pending, err = f.store.PendingApprovals(ctx, "W", "L")
	if err != nil || len(pending) != 1 || int64(pending[0].VerdictID) != second {
		t.Fatalf("re-approval not queued: %+v, %v", pending, err)
	}
	if _, err := f.applier.Apply(ctx, apply.Request{Workspace: "W", Lead: "L", Change: "C1", Revision: 1, RequestID: fmt.Sprintf("approval:%d", second)}); err != nil ||
		f.git(t, "rev-parse", "HEAD") != f.source {
		t.Fatalf("re-approval did not apply: %v", err)
	}
}

// Crash window from the verifier: Unapply lands after Apply but before the
// follow is marked applied. The newer verdict must take over the follow, and
// the old request must settle as spent instead of being retried forever.
func TestUnapplyBeforeFollowStatusThenReapproveFollowsNewestVerdict(t *testing.T) {
	f := newFixture(t)
	preparePull(t, f)
	ctx := context.Background()
	first := approveC1(t, f)
	firstRequest := fmt.Sprintf("approval:%d", first)
	if _, err := f.applier.Apply(ctx, apply.Request{Workspace: "W", Lead: "L", Change: "C1", Revision: 1, RequestID: firstRequest}); err != nil {
		t.Fatal(err)
	}
	unapplyC1(t, f, "unapply-before-follow")
	if _, err := f.applier.Apply(ctx, apply.Request{Workspace: "W", Lead: "L", Change: "C1", Revision: 1, RequestID: firstRequest}); !errors.Is(err, apply.ErrRequestSpent) {
		t.Fatalf("old request after Unapply: %v", err)
	}
	second := approveC1(t, f)
	pending, err := f.store.PendingApprovals(ctx, "W", "L")
	if err != nil || len(pending) != 1 || int64(pending[0].VerdictID) != second {
		t.Fatalf("follow kept the old verdict: %+v, %v", pending, err)
	}
	if _, err := f.applier.Apply(ctx, apply.Request{Workspace: "W", Lead: "L", Change: "C1", Revision: 1, RequestID: fmt.Sprintf("approval:%d", second)}); err != nil ||
		f.git(t, "rev-parse", "HEAD") != f.source {
		t.Fatalf("re-approval did not apply: %v", err)
	}
}

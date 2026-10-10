package publish

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/landing"
)

// X1: A was squash-merged outside Loom with B's file changed on trunk. The
// provider retargets B's PR to trunk but cannot rebuild it, and reports it
// dirty. Loom replays nothing and pushes nothing; it records the conflict on
// the stack and keeps the offer open for a later provider rebuild.
func TestLandingReconcileRecordsNativeProviderConflict(t *testing.T) {
	fixture, base, second := landingStackFixture(t, true, "native")
	ctx := context.Background()
	base.prs[1].Base, base.prs[1].HeadSHA = "develop", second.HeadSHA
	forge := &mergeForgeFake{fakeForge: base, mergeStates: map[int]string{base.prs[1].Number: "dirty"}}
	before := git(t, fixture.repo, "rev-parse", "HEAD")
	publication, _, err := fixture.store.Publication(ctx, "W", "B")
	if err != nil {
		t.Fatal(err)
	}
	options := landing.Options{Restack: RestackOffer}
	if err := landing.ReconcileWithOptions(ctx, fixture.store, forge, options); err == nil {
		t.Fatal("native provider conflict reconciled without error")
	}
	state, err := fixture.store.StackState(ctx, "W", "feature-1")
	if err != nil || state.Status != "restack_conflict" || len(state.Paths) != 1 || state.Paths[0] != "B" {
		t.Fatalf("native provider conflict state = %+v, %v", state, err)
	}
	if got := git(t, fixture.repo, "rev-parse", "HEAD"); got != before {
		t.Fatalf("native conflict changed working area from %s to %s", before, got)
	}
	if got := git(t, fixture.remote, "rev-parse", "refs/heads/"+publication.Branch); got != second.HeadSHA {
		t.Fatalf("native conflict pushed B: %s", got)
	}
	offers, err := fixture.store.OpenRestackOffers(ctx)
	if err != nil || len(offers) != 1 {
		t.Fatalf("native conflict lost offer: %+v, %v", offers, err)
	}
}

// Without a conflicting provider signal the provider may still rebuild, so
// Loom keeps waiting and records nothing.
func TestLandingReconcileWaitsForNativeRebuildWithoutConflictSignal(t *testing.T) {
	fixture, base, second := landingStackFixture(t, true, "native")
	ctx := context.Background()
	base.prs[1].Base, base.prs[1].HeadSHA = "develop", second.HeadSHA
	forge := &mergeForgeFake{fakeForge: base}
	_ = landing.ReconcileWithOptions(ctx, fixture.store, forge, landing.Options{Restack: RestackOffer})
	state, err := fixture.store.StackState(ctx, "W", "feature-1")
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
	if state.Status == "restack_conflict" {
		t.Fatalf("mergeable native PR recorded a conflict: %+v", state)
	}
	if offers, err := fixture.store.OpenRestackOffers(ctx); err != nil || len(offers) != 1 {
		t.Fatalf("native wait lost offer: %+v, %v", offers, err)
	}
}

// A rebuild that conflicts can never carry the approval: the approval asks
// again, and the stack keeps its resolve attention.
func TestApproveMergeAsksAgainWhenTheRebuildConflicts(t *testing.T) {
	item, forge, heads := mergeApprovalFixture(t, "loom")
	ctx := context.Background()
	if _, err := approveMerge(ctx, item.store, forge, "W", "L", "C", heads[2], tyson); err != nil {
		t.Fatal(err)
	}
	offer := journal.RestackOffer{Workspace: "W", Change: "C", Predecessor: "B", Task: "task-C", Repo: item.repo,
		Revision: 1, TrunkSHA: heads[1]}
	if err := item.store.OfferRestack(ctx, offer); err != nil {
		t.Fatal(err)
	}
	if err := item.store.RecordStackAttention(ctx, offer, "feature", "restack_conflict", []string{"c.txt"}); err != nil {
		t.Fatal(err)
	}
	reconcileApprovals(t, item, forge)
	got := approval(t, item, "C")
	if got.Status != MergeApprovalReapproval || got.Reason != "the rebuild after the PRs below merged was not clean; approve again" {
		t.Fatalf("C after a conflicting rebuild = %+v", got)
	}
	if state, err := item.store.StackState(ctx, "W", "feature"); err != nil || state.Status != "restack_conflict" {
		t.Fatalf("stack attention after reapproval = %+v, %v", state, err)
	}
	if _, err := item.store.LoomMerge(ctx, "W", "feature"); !errors.Is(err, sql.ErrNoRows) || forge.merged != 0 {
		t.Fatalf("conflicting rebuild merged: %v, merged = %d", err, forge.merged)
	}
}

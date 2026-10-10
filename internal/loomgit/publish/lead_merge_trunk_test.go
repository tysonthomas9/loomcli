package publish

import (
	"context"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/review"
	"github.com/tysonthomas9/loomcli/internal/stackpublish"
)

// trunkLeadFixture is PR per task: each change has its own PR to trunk, none
// stacked on another. PR numbers are 10, 11, ... in change order.
func trunkLeadFixture(t *testing.T, changes ...string) (fixture, *mergeForgeFake) {
	t.Helper()
	item := newFixture(t)
	ctx := context.Background()
	if err := item.store.SetDeliveryMode(ctx, "W", "trunk"); err != nil {
		t.Fatal(err)
	}
	forge := &mergeForgeFake{fakeForge: &fakeForge{}, checks: "passing", reviews: map[int]string{}}
	for index, change := range changes {
		revision := stackRevision(t, item, change, 1, item.base)
		publication := journal.Publication{Workspace: "W", Change: change, Repo: item.repo, Branch: "loom/" + change,
			Trunk: "develop", Slug: "owner/repo", Head: revision.HeadSHA}
		if err := item.store.BeginPublication(ctx, publication); err != nil {
			t.Fatal(err)
		}
		publication.Phase, publication.PRNumber = "done", 10+index
		if err := item.store.AdvancePublication(ctx, publication); err != nil {
			t.Fatal(err)
		}
		forge.prs = append(forge.prs, stackpublish.PR{Number: publication.PRNumber, Head: publication.Branch, Base: "develop",
			HeadSHA: revision.HeadSHA, State: "open"})
		forge.reviews[publication.PRNumber] = "none"
	}
	return item, forge
}

func reconcileLeadTrunk(t *testing.T, item fixture, forge *mergeForgeFake) {
	t.Helper()
	ctx := context.Background()
	if err := ReconcileMergeApprovalsAt(ctx, item.storePath, forge); err != nil {
		t.Fatal(err)
	}
	if err := ReconcileLeadMergesAt(ctx, item.storePath, forge); err != nil {
		t.Fatal(err)
	}
}

func requireNoApproval(t *testing.T, item fixture, change string) {
	t.Helper()
	if got, found, err := item.store.MergeApproval(context.Background(), "W", change); err != nil || found {
		t.Fatalf("%s merge approval = %+v, %v; want none", change, got, err)
	}
}

func landTrunkPR(t *testing.T, item fixture, change string) {
	t.Helper()
	ctx := context.Background()
	if err := item.store.MarkMerged(ctx, "W", change); err != nil {
		t.Fatal(err)
	}
	if err := item.store.MarkLanded(ctx, "W", change, "merge_commit"); err != nil {
		t.Fatal(err)
	}
}

// S6/S8: each green PR to trunk merges by itself under the human's setting;
// a red one stays open and holds back nothing, then merges once it is green.
func TestWhenGreenMergesEachGreenTrunkPRAndLeavesRedOneOpen(t *testing.T) {
	item, forge := trunkLeadFixture(t, "A", "B", "C")
	forge.prChecks = map[int]string{11: "failing"}
	setLeadMayMerge(t, item, "when_green")
	reconcileLeadTrunk(t, item, forge)
	if forge.merged != 2 || !forge.prs[0].Merged || forge.prs[1].Merged || !forge.prs[2].Merged {
		t.Fatalf("merged = %d, prs = %+v; want A and C only", forge.merged, forge.prs)
	}
	for _, change := range []string{"A", "C"} {
		got := approval(t, item, change)
		if got.ActorKind != "lead" || got.ActorID != "lead under setting set by tyson" || got.Status != MergeApprovalMerging {
			t.Fatalf("%s lead merge = %+v", change, got)
		}
	}
	requireNoApproval(t, item, "B")
	landTrunkPR(t, item, "A")
	landTrunkPR(t, item, "C")
	reconcileLeadTrunk(t, item, forge)
	if got := approval(t, item, "A"); got.Status != MergeApprovalMerged || forge.merged != 2 {
		t.Fatalf("A after landing = %+v, merged = %d", got, forge.merged)
	}
	requireNoApproval(t, item, "B")
	forge.prChecks = nil
	reconcileLeadTrunk(t, item, forge)
	if !forge.prs[1].Merged || forge.merged != 3 {
		t.Fatalf("B once green: merged = %d, prs = %+v", forge.merged, forge.prs)
	}
}

func TestWhenGreenOffNeverMergesTrunkPRs(t *testing.T) {
	item, forge := trunkLeadFixture(t, "A")
	reconcileLeadTrunk(t, item, forge)
	setLeadMayMerge(t, item, "when_green")
	setLeadMayMerge(t, item, "off")
	reconcileLeadTrunk(t, item, forge)
	if forge.merged != 0 {
		t.Fatalf("merged %d PRs with Lead may merge off", forge.merged)
	}
	requireNoApproval(t, item, "A")
	ctx := context.Background()
	if prs, err := item.store.LeadMergeTrunkPRs(ctx); err != nil || len(prs) != 0 {
		t.Fatalf("trunk PRs listed for the lead with the setting off = %+v, %v", prs, err)
	}
	setLeadMayMerge(t, item, "when_green")
	if prs, err := item.store.LeadMergeTrunkPRs(ctx); err != nil || len(prs) != 1 || prs[0].Change != "A" || prs[0].SetBy != "tyson" {
		t.Fatalf("trunk PRs listed for the lead with the setting on = %+v, %v", prs, err)
	}
	landTrunkPR(t, item, "A")
	if prs, err := item.store.LeadMergeTrunkPRs(ctx); err != nil || len(prs) != 0 {
		t.Fatalf("landed trunk PR listed for the lead = %+v, %v", prs, err)
	}
}

// The lead merges only a PR head Loom approved: a head whose latest verdict
// is a rejection is never green.
func TestWhenGreenTrunkNeedsLoomApprovalOfThePRHead(t *testing.T) {
	item, forge := trunkLeadFixture(t, "A")
	ctx := context.Background()
	if _, err := review.Submit(ctx, item.store, "W", "A", 1, forge.prs[0].HeadSHA, "reject", "not yet",
		review.Actor{Kind: "human", ID: "reviewer"}); err != nil {
		t.Fatal(err)
	}
	setLeadMayMerge(t, item, "when_green")
	reconcileLeadTrunk(t, item, forge)
	if forge.merged != 0 {
		t.Fatalf("merged a PR head Loom rejected")
	}
	requireNoApproval(t, item, "A")
}

// A dependent's PR to trunk merges only after its blocker in the same repo has
// landed, even when the dependent is green first.
func TestWhenGreenTrunkWaitsForSameRepoBlockerToLand(t *testing.T) {
	item, forge := trunkLeadFixture(t, "A", "B")
	ctx := context.Background()
	for task, change := range map[string]string{"T1": "A", "T2": "B"} {
		if _, err := item.store.DriverChange(ctx, "W", task, "app", change); err != nil {
			t.Fatal(err)
		}
	}
	previous := mergePredecessors
	mergePredecessors = func(_ context.Context, workspace, task string) ([]string, error) {
		if workspace == "W" && task == "T2" {
			return []string{"T1"}, nil
		}
		return nil, nil
	}
	t.Cleanup(func() { mergePredecessors = previous })
	forge.prChecks = map[int]string{10: "failing"}
	setLeadMayMerge(t, item, "when_green")
	reconcileLeadTrunk(t, item, forge)
	if forge.merged != 0 {
		t.Fatalf("merged %d PRs before the blocker landed: %+v", forge.merged, forge.prs)
	}
	requireNoApproval(t, item, "B")
	forge.prChecks = nil
	reconcileLeadTrunk(t, item, forge)
	if !forge.prs[0].Merged || forge.prs[1].Merged {
		t.Fatalf("blocker green, not landed: prs = %+v", forge.prs)
	}
	landTrunkPR(t, item, "A")
	reconcileLeadTrunk(t, item, forge)
	if !forge.prs[1].Merged || forge.merged != 2 {
		t.Fatalf("dependent after its blocker landed: merged = %d, prs = %+v", forge.merged, forge.prs)
	}
}

// A queued lead merge is re-checked before dispatch: turning the setting off
// cancels it, and turning it back on starts a fresh one.
func TestWhenGreenTrunkQueuedMergeIsCancelledWhenTheSettingGoesOff(t *testing.T) {
	item, forge := trunkLeadFixture(t, "A")
	ctx := context.Background()
	forge.prChecks = map[int]string{10: "pending"}
	setLeadMayMerge(t, item, "when_green")
	queued := journal.MergeApproval{Workspace: "W", Change: "A", Head: forge.prs[0].HeadSHA, ActorKind: "lead",
		ActorID: leadMergeActor("tyson"), Status: MergeApprovalBlocked, Reason: "waiting for required checks", CreatedAt: 1}
	if _, err := item.store.RecordMergeApproval(ctx, queued); err != nil {
		t.Fatal(err)
	}
	setLeadMayMerge(t, item, "off")
	forge.prChecks = nil
	reconcileLeadTrunk(t, item, forge)
	if got := approval(t, item, "A"); got.Status != MergeApprovalCancelled || forge.merged != 0 {
		t.Fatalf("queued lead merge after setting off = %+v, merged = %d", got, forge.merged)
	}
	setLeadMayMerge(t, item, "when_green")
	reconcileLeadTrunk(t, item, forge)
	if got := approval(t, item, "A"); got.Status != MergeApprovalMerging || forge.merged != 1 {
		t.Fatalf("lead merge after setting back on = %+v, merged = %d", got, forge.merged)
	}
}

// A human's Cancel auto-merge of the lead's merge stands for that PR head.
func TestWhenGreenTrunkRespectsAHumanCancel(t *testing.T) {
	item, forge := trunkLeadFixture(t, "A")
	ctx := context.Background()
	setLeadMayMerge(t, item, "when_green")
	queued := journal.MergeApproval{Workspace: "W", Change: "A", Head: forge.prs[0].HeadSHA, ActorKind: "lead",
		ActorID: leadMergeActor("tyson"), Status: MergeApprovalBlocked, Reason: "waiting for required checks", CreatedAt: 1}
	if _, err := item.store.RecordMergeApproval(ctx, queued); err != nil {
		t.Fatal(err)
	}
	if cancelled, err := CancelMergeApproval(ctx, item.store, "W", "A", "auto-merge cancelled by tyson"); err != nil || !cancelled {
		t.Fatalf("cancel = %v, %v", cancelled, err)
	}
	reconcileLeadTrunk(t, item, forge)
	if got := approval(t, item, "A"); got.Status != MergeApprovalCancelled || forge.merged != 0 {
		t.Fatalf("lead merge after a human cancel = %+v, merged = %d", got, forge.merged)
	}
}

// A green PR to trunk whose base was changed away from the trunk Loom
// published it to never merges: neither the lead pass nor a queued merge.
func TestWhenGreenTrunkNeverMergesARetargetedPR(t *testing.T) {
	item, forge := trunkLeadFixture(t, "A")
	forge.prs[0].Base = "other-branch"
	setLeadMayMerge(t, item, "when_green")
	reconcileLeadTrunk(t, item, forge)
	if forge.merged != 0 {
		t.Fatalf("merged a PR retargeted to %s", forge.prs[0].Base)
	}
	requireNoApproval(t, item, "A")
}

func queueLeadMerge(t *testing.T, item fixture, head, status string) {
	t.Helper()
	queued := journal.MergeApproval{Workspace: "W", Change: "A", Head: head, ActorKind: "lead",
		ActorID: leadMergeActor("tyson"), Status: status, CreatedAt: 1}
	if _, err := item.store.RecordMergeApproval(context.Background(), queued); err != nil {
		t.Fatal(err)
	}
}

func TestWhenGreenTrunkHoldsAQueuedMergeOfARetargetedPR(t *testing.T) {
	item, forge := trunkLeadFixture(t, "A")
	setLeadMayMerge(t, item, "when_green")
	queueLeadMerge(t, item, forge.prs[0].HeadSHA, MergeApprovalBlocked)
	forge.prs[0].Base = "other-branch"
	reconcileLeadTrunk(t, item, forge)
	got := approval(t, item, "A")
	if forge.merged != 0 || got.Status != MergeApprovalBlocked || got.Attempt != 0 || got.Reason != "the PR's base was changed away from develop; change it back to merge" {
		t.Fatalf("queued merge of a retargeted PR = %+v, merged = %d", got, forge.merged)
	}
	forge.prs[0].Base = "develop"
	reconcileLeadTrunk(t, item, forge)
	if forge.merged != 1 {
		t.Fatalf("merge after the base was changed back: merged = %d, %+v", forge.merged, approval(t, item, "A"))
	}
}

// A merge resumed after a crash (already moved to merging, not yet sent)
// re-reads the PR base before it dispatches.
func TestWhenGreenTrunkResumedDispatchChecksTheBase(t *testing.T) {
	item, forge := trunkLeadFixture(t, "A")
	setLeadMayMerge(t, item, "when_green")
	queueLeadMerge(t, item, forge.prs[0].HeadSHA, MergeApprovalMerging)
	forge.prs[0].Base = "other-branch"
	reconcileLeadTrunk(t, item, forge)
	if got := approval(t, item, "A"); forge.merged != 0 || got.Status != MergeApprovalBlocked {
		t.Fatalf("resumed dispatch of a retargeted PR = %+v, merged = %d", got, forge.merged)
	}
}

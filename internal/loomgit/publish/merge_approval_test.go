package publish

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/stackpublish"
)

// mergeApprovalFixture is a published four-layer stack A-B-C-D, green on the
// provider, on the given backend.
func mergeApprovalFixture(t *testing.T, backend string) (fixture, *mergeForgeFake, []string) {
	t.Helper()
	item, forge, heads := fourLayerMergeEntryFixture(t, backend)
	forge.reviews = map[int]string{}
	for _, pr := range forge.prs {
		forge.reviews[pr.Number] = "approved"
	}
	return item, forge, heads
}

func approval(t *testing.T, item fixture, change string) journal.MergeApproval {
	t.Helper()
	recorded, found, err := item.store.MergeApproval(context.Background(), "W", change)
	if err != nil || !found {
		t.Fatalf("approval %s: found=%v err=%v", change, found, err)
	}
	return recorded
}

func reconcileApprovals(t *testing.T, item fixture, forge mergeApprovalForge) {
	t.Helper()
	if err := ReconcileMergeApprovalsAt(context.Background(), item.storePath, forge); err != nil {
		t.Fatal(err)
	}
}

func reconcileLoom(t *testing.T, item fixture, forge *mergeForgeFake) {
	t.Helper()
	if err := ReconcileLoomMergesAt(context.Background(), item.storePath, forge); err != nil {
		t.Fatal(err)
	}
}

// landBottom runs the Loom merge machine for the merging bottom layer through
// landing and the restack of next (when there is one), like Reconcile does.
func landBottom(t *testing.T, item fixture, forge *mergeForgeFake, change, next string) {
	t.Helper()
	ctx := context.Background()
	reconcileLoom(t, item, forge)
	landed := squashMergeLayer(t, item, change)
	if err := item.store.MarkLanded(ctx, "W", change, "merge_commit"); err != nil {
		t.Fatal(err)
	}
	reconcileLoom(t, item, forge)
	reconcileLoom(t, item, forge)
	if next != "" {
		restackAfterMerge(t, item, forge, change, next, landed)
		// restackAfterMerge follows A-C; the fixture's fourth layer moves too.
		publication, _, err := item.store.Publication(ctx, "W", "D")
		if err != nil {
			t.Fatal(err)
		}
		forge.prs[3].HeadSHA = publication.Head
	}
	reconcileLoom(t, item, forge)
}

func TestApproveMergeBottomPRMergesNow(t *testing.T) {
	item, forge, heads := mergeApprovalFixture(t, "loom")
	ctx := context.Background()
	view, err := approveMerge(ctx, item.store, forge, "W", "L", "A", heads[0], tyson)
	if err != nil || view.Status != MergeApprovalMerging || len(view.MergeAfter) != 0 {
		t.Fatalf("approve A = %+v, %v", view, err)
	}
	merge := leadMerge(t, item)
	if merge.Target != "A" || merge.Authority != humanApprovalAuthority || merge.RequestID != "approval-merge:W:A:1" {
		t.Fatalf("merge = %+v", merge)
	}
	landBottom(t, item, forge, "A", "B")
	if forge.merged != 1 || !forge.prs[0].Merged || forge.prs[1].Merged {
		t.Fatalf("merged = %d, prs = %+v", forge.merged, forge.prs)
	}
	if merge = leadMerge(t, item); merge.Phase != "done" || merge.Layers[0].MergedBy != "Approve and merge by Tyson" {
		t.Fatalf("finished merge = %+v", merge)
	}
	reconcileApprovals(t, item, forge)
	if got := approval(t, item, "A"); got.Status != MergeApprovalMerged {
		t.Fatalf("approval = %+v", got)
	}
}

func TestApproveMergeAboveBottomWaitsThenMergesAfterCleanRestacks(t *testing.T) {
	item, forge, heads := mergeApprovalFixture(t, "loom")
	ctx := context.Background()
	view, err := approveMerge(ctx, item.store, forge, "W", "L", "C", heads[2], tyson)
	if err != nil || view.Status != MergeApprovalWaiting || view.Reason != "merges after #1, #2" {
		t.Fatalf("approve C = %+v, %v", view, err)
	}
	if _, err := approveMerge(ctx, item.store, forge, "W", "L", "B", heads[1], tyson); err != nil {
		t.Fatal(err)
	}
	if _, err := item.store.LoomMerge(ctx, "W", "feature"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("a non-bottom approval started a merge: %v", err)
	}
	if _, err := approveMerge(ctx, item.store, forge, "W", "L", "A", heads[0], tyson); err != nil {
		t.Fatal(err)
	}
	landBottom(t, item, forge, "A", "B")
	reconcileApprovals(t, item, forge)
	if got := approval(t, item, "B"); got.Status != MergeApprovalMerging || got.Head == heads[1] {
		t.Fatalf("B after A landed and restacked = %+v", got)
	}
	if got := approval(t, item, "C"); got.Status != MergeApprovalWaiting || got.Reason != "merges after #2" {
		t.Fatalf("C while B merges = %+v", got)
	}
	landBottom(t, item, forge, "B", "C")
	reconcileApprovals(t, item, forge)
	got := approval(t, item, "C")
	if got.Status != MergeApprovalMerging || got.ApprovedHead != heads[2] || got.Head == heads[2] {
		t.Fatalf("C after B landed = %+v", got)
	}
	reconcileLoom(t, item, forge)
	if forge.merged != 3 || !forge.prs[2].Merged || forge.prs[3].Merged {
		t.Fatalf("merged = %d, prs = %+v", forge.merged, forge.prs)
	}
}

// rebuildRevision records a newer revision of change, as a restack would,
// with a verdict carried from its approved head or none at all.
func rebuildRevision(t *testing.T, item fixture, change, from string, carried bool) loomgit.Revision {
	t.Helper()
	ctx := context.Background()
	source, err := item.store.RevisionByHead(ctx, "W", change, from)
	if err != nil {
		t.Fatal(err)
	}
	head := git(t, item.repo, "commit-tree", "-p", source.HeadSHA, "-m", "rebuilt "+change, source.HeadSHA+"^{tree}")
	revision, err := item.store.ReserveRevision(ctx, loomgit.Revision{Workspace: "W", Change: change,
		RequestID: "restack-" + change + head[:7], Kind: "derived", Operation: "restack", Outcome: "completed",
		BaseSHA: source.HeadSHA, TreeHash: head, SourceHeadSHA: head,
		DerivedFromChange: change, DerivedFromNumber: source.Number})
	if err != nil {
		t.Fatal(err)
	}
	revision.HeadSHA = head
	if err := item.store.FinishRevision(ctx, revision); err != nil {
		t.Fatal(err)
	}
	if carried {
		prior, err := item.store.LatestVerdict(ctx, source)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := item.store.RecordVerdict(ctx, loomgit.Verdict{Workspace: "W", Change: change, Number: revision.Number,
			HeadSHA: head, Kind: "carried", ActorKind: prior.ActorKind, ActorID: prior.ActorID,
			Reason: "patch_equivalent", SourceVerdictID: prior.ID}); err != nil {
			t.Fatal(err)
		}
	}
	return revision
}

func TestApproveMergeAsksAgainAfterARebuildThatIsNotClean(t *testing.T) {
	item, forge, heads := mergeApprovalFixture(t, "loom")
	ctx := context.Background()
	if _, err := approveMerge(ctx, item.store, forge, "W", "L", "C", heads[2], tyson); err != nil {
		t.Fatal(err)
	}
	rebuildRevision(t, item, "C", heads[2], false)
	reconcileApprovals(t, item, forge)
	got := approval(t, item, "C")
	if got.Status != MergeApprovalReapproval || got.Reason != "the rebuild after the PRs below merged was not clean; approve again" {
		t.Fatalf("C after an unclean rebuild = %+v", got)
	}
	if _, err := item.store.LoomMerge(ctx, "W", "feature"); !errors.Is(err, sql.ErrNoRows) || forge.merged != 0 {
		t.Fatalf("unclean rebuild merged: %v, merged = %d", err, forge.merged)
	}
}

func TestApproveMergeCarriesThroughACleanRebuildAndWaitsForThePR(t *testing.T) {
	item, forge, heads := mergeApprovalFixture(t, "loom")
	ctx := context.Background()
	if _, err := approveMerge(ctx, item.store, forge, "W", "L", "C", heads[2], tyson); err != nil {
		t.Fatal(err)
	}
	rebuilt := rebuildRevision(t, item, "C", heads[2], true)
	reconcileApprovals(t, item, forge)
	got := approval(t, item, "C")
	if got.Status != MergeApprovalWaiting || got.Head != rebuilt.HeadSHA || got.ApprovedHead != heads[2] ||
		got.Reason != "waiting for the PR to update to the approved version" {
		t.Fatalf("C after a clean rebuild = %+v", got)
	}
}

func TestApproveMergeBlockedByChecksRetriesWhenTheyPass(t *testing.T) {
	item, forge, heads := mergeApprovalFixture(t, "loom")
	ctx := context.Background()
	forge.prChecks = map[int]string{forge.prs[0].Number: "failing"}
	view, err := approveMerge(ctx, item.store, forge, "W", "L", "A", heads[0], tyson)
	if err != nil || view.Status != MergeApprovalBlocked || view.Reason != "required checks are failing" {
		t.Fatalf("approve with failing checks = %+v, %v", view, err)
	}
	forge.reviews[forge.prs[0].Number] = "changes_requested"
	forge.prChecks[forge.prs[0].Number] = "passing"
	reconcileApprovals(t, item, forge)
	if got := approval(t, item, "A"); got.Status != MergeApprovalBlocked || got.Reason != "a reviewer requested changes" {
		t.Fatalf("approval with changes requested = %+v", got)
	}
	if _, err := item.store.LoomMerge(ctx, "W", "feature"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("blocked approval started a merge: %v", err)
	}
	forge.reviews[forge.prs[0].Number] = "approved"
	reconcileApprovals(t, item, forge)
	if got := approval(t, item, "A"); got.Status != MergeApprovalMerging {
		t.Fatalf("approval after checks pass = %+v", got)
	}
	reconcileLoom(t, item, forge)
	if forge.merged != 1 {
		t.Fatalf("merged = %d", forge.merged)
	}
}

func TestApproveMergeRetriesAfterTheMergeMachineBlocks(t *testing.T) {
	item, forge, heads := mergeApprovalFixture(t, "loom")
	ctx := context.Background()
	forge.rejectPut = true
	if _, err := approveMerge(ctx, item.store, forge, "W", "L", "A", heads[0], tyson); err != nil {
		t.Fatal(err)
	}
	if err := ReconcileLoomMergesAt(ctx, item.storePath, forge); err == nil {
		t.Fatal("rejected merge did not block")
	}
	reconcileApprovals(t, item, forge)
	if got := approval(t, item, "A"); got.Status != MergeApprovalBlocked || got.Reason == "" {
		t.Fatalf("approval after rejected merge = %+v", got)
	}
	forge.rejectPut = false
	reconcileApprovals(t, item, forge)
	got := approval(t, item, "A")
	if got.Status != MergeApprovalMerging || got.Attempt != 2 || leadMerge(t, item).RequestID != "approval-merge:W:A:2" {
		t.Fatalf("retried approval = %+v, merge = %+v", got, leadMerge(t, item))
	}
	reconcileLoom(t, item, forge)
	if !forge.prs[0].Merged {
		t.Fatal("retried merge did not merge")
	}
}

func TestApproveMergeRefusesAPushedPRAsStaleSubject(t *testing.T) {
	item, forge, heads := mergeApprovalFixture(t, "loom")
	ctx := context.Background()
	forge.prs[0].HeadSHA = "f00d"
	view, err := approveMerge(ctx, item.store, forge, "W", "L", "A", heads[0], tyson)
	if err != nil || view.Status != MergeApprovalStale {
		t.Fatalf("approve pushed PR = %+v, %v", view, err)
	}
	if _, err := item.store.LoomMerge(ctx, "W", "feature"); !errors.Is(err, sql.ErrNoRows) || forge.merged != 0 {
		t.Fatalf("stale subject merged: %v, merged = %d", err, forge.merged)
	}
	reconcileApprovals(t, item, forge)
	if got := approval(t, item, "A"); got.Status != MergeApprovalStale {
		t.Fatalf("stale approval rearmed: %+v", got)
	}
	_, err = approveMerge(ctx, item.store, forge, "W", "L", "A", "someone-else", tyson)
	var coded *loomgit.Error
	if !errors.As(err, &coded) || coded.Kind != loomgit.Stale {
		t.Fatalf("approve an unseen head = %v", err)
	}
}

func TestApproveMergeIsHumanOnly(t *testing.T) {
	item, forge, heads := mergeApprovalFixture(t, "loom")
	ctx := context.Background()
	for name, actor := range map[string]MergeActor{
		"lead":            {Kind: "lead", ID: "L"},
		"agent":           {Kind: "agent", ID: "task-1"},
		"human as lead":   {Kind: "human", ID: "L"},
		"anonymous human": {Kind: "human"},
	} {
		_, err := approveMerge(ctx, item.store, forge, "W", "L", "A", heads[0], actor)
		var coded *loomgit.Error
		if !errors.As(err, &coded) || coded.Kind != loomgit.MergeNotAuthorized {
			t.Fatalf("%s approve = %v", name, err)
		}
	}
	if _, found, err := item.store.MergeApproval(ctx, "W", "A"); err != nil || found {
		t.Fatalf("refused approval recorded: found=%v err=%v", found, err)
	}
}

func TestApproveMergeCrashBetweenApprovalAndMergeMergesOnce(t *testing.T) {
	item, forge, heads := mergeApprovalFixture(t, "loom")
	ctx := context.Background()
	recorded, err := item.store.RecordMergeApproval(ctx, journal.MergeApproval{Workspace: "W", Change: "A", Lead: "L",
		StackID: "feature", Head: heads[0], ActorKind: "human", ActorID: "Tyson", Status: MergeApprovalWaiting, CreatedAt: 1})
	if err != nil {
		t.Fatal(err)
	}
	// Crash after the approval moved to merging, before the backend merge began.
	after := recorded
	after.Status, after.Attempt, after.MergeRequestID = MergeApprovalMerging, 1, "approval-merge:W:A:1"
	if err := item.store.AdvanceMergeApproval(ctx, recorded, after); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		reconcileApprovals(t, item, forge)
		reconcileLoom(t, item, forge)
	}
	if merge := leadMerge(t, item); merge.RequestID != "approval-merge:W:A:1" || forge.merged != 1 {
		t.Fatalf("recovered merge = %+v, merged = %d", merge, forge.merged)
	}
	if got := approval(t, item, "A"); got.Attempt != 1 || got.Status != MergeApprovalMerging {
		t.Fatalf("recovered approval = %+v", got)
	}
}

func TestCancelMergeApprovalStopsAWaitingMerge(t *testing.T) {
	item, forge, heads := mergeApprovalFixture(t, "loom")
	ctx := context.Background()
	if _, err := approveMerge(ctx, item.store, forge, "W", "L", "B", heads[1], tyson); err != nil {
		t.Fatal(err)
	}
	cancelled, err := CancelMergeApproval(ctx, item.store, "W", "B", "feedback fix-up changed the PR")
	if err != nil || !cancelled {
		t.Fatalf("cancel = %v, %v", cancelled, err)
	}
	if again, err := CancelMergeApproval(ctx, item.store, "W", "B", "again"); err != nil || again {
		t.Fatalf("second cancel = %v, %v", again, err)
	}
	if _, err := approveMerge(ctx, item.store, forge, "W", "L", "A", heads[0], tyson); err != nil {
		t.Fatal(err)
	}
	landBottom(t, item, forge, "A", "B")
	reconcileApprovals(t, item, forge)
	reconcileLoom(t, item, forge)
	if got := approval(t, item, "B"); got.Status != MergeApprovalCancelled || got.Reason != "feedback fix-up changed the PR" || forge.prs[1].Merged {
		t.Fatalf("cancelled B = %+v, merged = %v", got, forge.prs[1].Merged)
	}
	view, err := approveMerge(ctx, item.store, forge, "W", "L", "B", forge.prs[1].HeadSHA, tyson)
	if err != nil || view.Status != MergeApprovalMerging {
		t.Fatalf("re-approve after cancel = %+v, %v", view, err)
	}
}

func TestCancelMergeApprovalRefusesAMergeAlreadyStarted(t *testing.T) {
	item, forge, heads := mergeApprovalFixture(t, "loom")
	ctx := context.Background()
	if _, err := approveMerge(ctx, item.store, forge, "W", "L", "A", heads[0], tyson); err != nil {
		t.Fatal(err)
	}
	cancelled, err := CancelMergeApproval(ctx, item.store, "W", "A", "too late")
	var coded *loomgit.Error
	if cancelled || !errors.As(err, &coded) || coded.Kind != loomgit.Stale {
		t.Fatalf("cancel a started merge = %v, %v", cancelled, err)
	}
	if got := approval(t, item, "A"); got.Status != MergeApprovalMerging {
		t.Fatalf("approval after refused cancel = %+v", got)
	}
	reconcileLoom(t, item, forge)
	if forge.merged != 1 {
		t.Fatalf("started merge did not finish: merged = %d", forge.merged)
	}
}

func TestApproveMergeBeginsTheBackendMergeOnlyOnce(t *testing.T) {
	item, forge, heads := mergeApprovalFixture(t, "loom")
	ctx := context.Background()
	if _, err := approveMerge(ctx, item.store, forge, "W", "L", "A", heads[0], tyson); err != nil {
		t.Fatal(err)
	}
	reconcileLoom(t, item, forge)
	before := leadMerge(t, item)
	// A retried begin of the same attempt (a crash after the merge machine
	// recorded it) joins the recorded merge instead of refusing or redoing it.
	publication, _, err := item.store.Publication(ctx, "W", "A")
	if err != nil {
		t.Fatal(err)
	}
	request, err := approvalMergeRequest(ctx, item.store, forge, approval(t, item, "A"), publication)
	if err != nil {
		t.Fatal(err)
	}
	if err := beginLoomMerge(ctx, item.store, request, "A"); err != nil {
		t.Fatalf("retried begin = %v", err)
	}
	if after := leadMerge(t, item); after.Phase != before.Phase || after.RequestID != before.RequestID {
		t.Fatalf("retried begin reset the merge: before %+v, after %+v", before, after)
	}
	reconcileLoom(t, item, forge)
	if merge := leadMerge(t, item); merge.RequestID != "approval-merge:W:A:1" || forge.merged != 1 {
		t.Fatalf("merge = %+v, merged = %d", merge, forge.merged)
	}
}

func TestApproveMergeAuthorityRefusesAPRAboveTheBottom(t *testing.T) {
	item, _, heads := mergeApprovalFixture(t, "loom")
	ctx := context.Background()
	recorded, err := item.store.RecordMergeApproval(ctx, journal.MergeApproval{Workspace: "W", Change: "B", Lead: "L",
		StackID: "feature", Head: heads[1], ActorKind: "human", ActorID: "Tyson", Status: MergeApprovalWaiting, CreatedAt: 1})
	if err != nil {
		t.Fatal(err)
	}
	merging := recorded
	merging.Status, merging.Attempt, merging.MergeRequestID = MergeApprovalMerging, 1, "approval-merge:W:B:1"
	if err := item.store.AdvanceMergeApproval(ctx, recorded, merging); err != nil {
		t.Fatal(err)
	}
	authority := approvedMerge{Store: item.store, Approval: merging}
	if err := authority.AuthorizeMerge(ctx, StackRequest{Workspace: "W", StackID: "feature"}, "B"); err == nil {
		t.Fatal("authorized a merge of B while A is open below it")
	}
}

// approvalNativeForge is the GitHub provider with native stacks: the Loom
// merge calls plus native merge submission.
type approvalNativeForge struct {
	*mergeForgeFake
	native *fakeMergeForge
}

func (forge approvalNativeForge) MergeNativePull(ctx context.Context, owner, repo string, number int, head string) (stackpublish.NativeMergeResult, error) {
	forge.native.prs = forge.prs
	return forge.native.MergeNativePull(ctx, owner, repo, number, head)
}

func (forge approvalNativeForge) RecoverNativePull(ctx context.Context, owner, repo string, number int, head string) (stackpublish.NativeMergeResult, error) {
	return forge.native.RecoverNativePull(ctx, owner, repo, number, head)
}

func (forge approvalNativeForge) NativeMergeStatus(ctx context.Context, owner, repo string, number int, uuid string) (stackpublish.NativeMergeResult, error) {
	return forge.native.NativeMergeStatus(ctx, owner, repo, number, uuid)
}

func TestApproveMergeNativeBackendMergesOnlyTheBottomPR(t *testing.T) {
	item, loom, heads := mergeApprovalFixture(t, "native")
	forge := approvalNativeForge{mergeForgeFake: loom, native: &fakeMergeForge{fakeForge: loom.fakeForge}}
	ctx := context.Background()
	view, err := approveMerge(ctx, item.store, forge, "W", "L", "B", heads[1], tyson)
	if err != nil || view.Status != MergeApprovalWaiting || view.Reason != "merges after #1" {
		t.Fatalf("approve B = %+v, %v", view, err)
	}
	if view, err = approveMerge(ctx, item.store, forge, "W", "L", "A", heads[0], tyson); err != nil || view.Status != MergeApprovalMerging {
		t.Fatalf("approve A = %+v, %v", view, err)
	}
	merge, err := item.store.NativeMerge(ctx, "W", "feature")
	if err != nil || merge.Target != "A" || len(merge.Changes) != 1 || merge.Authority != humanApprovalAuthority || merge.SetBy != "Tyson" {
		t.Fatalf("native merge = %+v, %v", merge, err)
	}
	if len(forge.native.submitted) != 1 || forge.native.submitted[0] != forge.prs[0].Number {
		t.Fatalf("submitted = %v", forge.native.submitted)
	}
	reconcileApprovals(t, item, forge)
	if len(forge.native.submitted) != 1 {
		t.Fatalf("approval reconcile resubmitted: %v", forge.native.submitted)
	}
	forge.native.resultStatus = "merged"
	forge.prs[0].Merged, forge.prs[0].State = true, "closed"
	reconcileApprovals(t, item, forge)
	if err := item.store.MarkLanded(ctx, "W", "A", "merge_commit"); err != nil {
		t.Fatal(err)
	}
	// Reconcile's native pass finishes the provider merge once A has landed.
	if err := ReconcileNativeMerges(ctx, item.store, forge); err != nil {
		t.Fatal(err)
	}
	if merge, _ = item.store.NativeMerge(ctx, "W", "feature"); merge.Phase != "done" {
		t.Fatalf("native merge after A = %+v", merge)
	}
	// A finished merge gives way to B's approval, now the bottom. (This fixture
	// does not adopt GitHub's restack of B, so the provider step itself is not
	// asserted here; the AFT covers it.)
	reconcileApprovals(t, item, forge)
	if got := approval(t, item, "A"); got.Status != MergeApprovalMerged {
		t.Fatalf("A = %+v", got)
	}
	merge, err = item.store.NativeMerge(ctx, "W", "feature")
	if err != nil || merge.Target != "B" || merge.Authority != humanApprovalAuthority {
		t.Fatalf("B's native merge = %+v, %v", merge, err)
	}
}

// trunkApprovalFixture is one change's own PR to trunk (trunk mode).
func trunkApprovalFixture(t *testing.T) (fixture, *mergeForgeFake, string) {
	t.Helper()
	item := newFixture(t)
	ctx := context.Background()
	if err := item.store.SetDeliveryMode(ctx, "W", "trunk"); err != nil {
		t.Fatal(err)
	}
	revision := stackRevision(t, item, "A", 1, item.base)
	publication := journal.Publication{Workspace: "W", Change: "A", Repo: item.repo, Branch: "loom/A",
		Trunk: "develop", Slug: "owner/repo", Head: revision.HeadSHA}
	if err := item.store.BeginPublication(ctx, publication); err != nil {
		t.Fatal(err)
	}
	publication.Phase, publication.PRNumber = "done", 7
	if err := item.store.AdvancePublication(ctx, publication); err != nil {
		t.Fatal(err)
	}
	forge := &mergeForgeFake{fakeForge: &fakeForge{prs: []stackpublish.PR{{Number: 7, Head: "loom/A", Base: "develop",
		HeadSHA: revision.HeadSHA, State: "open"}}}, checks: "passing", reviews: map[int]string{7: "none"}}
	return item, forge, revision.HeadSHA
}

func TestApproveMergeTrunkModeMergesItsOwnPROnce(t *testing.T) {
	item, forge, head := trunkApprovalFixture(t)
	ctx := context.Background()
	forge.pending = true
	view, err := approveMerge(ctx, item.store, forge, "W", "L", "A", head, tyson)
	if err != nil || view.Status != MergeApprovalMerging || forge.merged != 1 {
		t.Fatalf("approve trunk PR = %+v, merged = %d, %v", view, forge.merged, err)
	}
	if got := approval(t, item, "A"); got.ProviderRequestID != "request-1" || got.DispatchHead != head {
		t.Fatalf("dispatch = %+v", got)
	}
	reconcileApprovals(t, item, forge)
	forge.pending = false
	forge.prs[0].Merged, forge.prs[0].State = true, "closed"
	reconcileApprovals(t, item, forge)
	if err := item.store.MarkLanded(ctx, "W", "A", "merge_commit"); err != nil {
		t.Fatal(err)
	}
	reconcileApprovals(t, item, forge)
	if got := approval(t, item, "A"); got.Status != MergeApprovalMerged || forge.merged != 1 {
		t.Fatalf("trunk approval = %+v, merged = %d", got, forge.merged)
	}
}

func TestApproveMergeTrunkModeReportsAnUnknownOutcomeInsteadOfMergingBlind(t *testing.T) {
	item, forge, head := trunkApprovalFixture(t)
	ctx := context.Background()
	forge.unknownAlways = true
	if _, err := approveMerge(ctx, item.store, forge, "W", "L", "A", head, tyson); err != nil {
		t.Fatal(err)
	}
	for range 4 {
		_ = ReconcileMergeApprovalsAt(ctx, item.storePath, forge)
	}
	got := approval(t, item, "A")
	if forge.merged != 2 || got.Status != MergeApprovalBlocked || !got.Attention ||
		got.Reason != "the merge outcome is unknown after two attempts; check the PR" {
		t.Fatalf("unknown outcome = %+v, merged = %d", got, forge.merged)
	}
	if cancelled, err := CancelMergeApproval(ctx, item.store, "W", "A", "checked"); err != nil || !cancelled {
		t.Fatalf("cancel after unknown outcome = %v, %v", cancelled, err)
	}
}

func TestPublishedStacksListsEachRepoStack(t *testing.T) {
	item, _, _ := mergeApprovalFixture(t, "loom")
	ctx := context.Background()
	if err := item.store.MarkLanded(ctx, "W", "A", "merge_commit"); err != nil {
		t.Fatal(err)
	}
	stacks, err := PublishedStacksLocal(ctx, "W")
	if err != nil || len(stacks) != 1 || stacks[0].StackID != "feature" || len(stacks[0].Layers) != 4 ||
		!stacks[0].Layers[0].Landed || stacks[0].Layers[1].Landed {
		t.Fatalf("stacks = %+v, %v", stacks, err)
	}
	below, err := item.store.UnlandedPRsBelow(ctx, journal.Publication{Workspace: "W", StackID: "feature", PRNumber: stacks[0].Layers[3].PRNumber})
	if err != nil || len(below) != 2 {
		t.Fatalf("below D = %v, %v", below, err)
	}
}

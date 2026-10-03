package publish

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/layout/refname"
	"github.com/tysonthomas9/loomcli/internal/loomgit/review"
)

var reviewer = review.Actor{Kind: "human", ID: "reviewer"}

// approvalFixture is a configured workspace W whose lead L has a working area.
func approvalFixture(t *testing.T, mode string) (fixture, *fakeForge) {
	t.Helper()
	useExplicitGitIdentity(t)
	fx := newFixture(t)
	configureLocalWorkspace(t, fx)
	ctx := context.Background()
	git(t, fx.repo, "push", "-q", "origin", fx.base+":refs/heads/develop")
	if err := fx.store.SaveWorkingAreas(ctx, []journal.WorkingArea{{
		Workspace: "W", Lead: "L", Repo: "repo", Path: fx.repo, BaseSHA: fx.base,
	}}); err != nil {
		t.Fatal(err)
	}
	if err := fx.store.SetDeliveryMode(ctx, "W", mode); err != nil {
		t.Fatal(err)
	}
	forge := &fakeForge{}
	useLocalForge(t, forge)
	return fx, forge
}

// appliedTask records an applied layer for change and its task, like a
// followed approval leaves behind.
func appliedTask(t *testing.T, fx fixture, change, parent string) loomgit.Revision {
	t.Helper()
	revision := stackRevision(t, fx, change, 1, parent)
	task := "task-" + change
	if mode, _ := fx.store.DeliveryMode(context.Background(), "W"); mode == "trunk" {
		task = "T" // the fixture's feature-flag lookup knows task T
	}
	if _, err := fx.store.DriverChange(context.Background(), "W", task, "repo", change); err != nil {
		t.Fatal(err)
	}
	return revision
}

// approveForLead records an approval targeting lead L and marks how it was followed.
func approveForLead(t *testing.T, fx fixture, revision loomgit.Revision, actor review.Actor, publish bool, follow string) loomgit.Verdict {
	t.Helper()
	ctx := context.Background()
	verdict, err := review.SubmitForLeadPublishing(ctx, fx.store, "W", revision.Change, revision.Number,
		revision.HeadSHA, "approve", "", actor, "L", publish)
	if err != nil {
		t.Fatal(err)
	}
	if err := fx.store.SetApprovalFollow(ctx, journal.PendingApproval{Workspace: "W", Lead: "L", Change: revision.Change,
		Revision: revision.Number, VerdictID: int(verdict.ID)}, follow, nil); err != nil {
		t.Fatal(err)
	}
	return verdict
}

func intentStatus(t *testing.T, fx fixture, change string) journal.ApprovalPublication {
	t.Helper()
	intent, found, err := fx.store.LatestApprovalPublication(context.Background(), "W", change, 1)
	if err != nil || !found {
		t.Fatalf("intent for %s: found=%v err=%v", change, found, err)
	}
	return intent
}

func remoteHead(t *testing.T, fx fixture, change string) string {
	t.Helper()
	branch, err := refname.ChangeBranch("W", change)
	if err != nil {
		t.Fatal(err)
	}
	return git(t, fx.repo, "ls-remote", "origin", "refs/heads/"+branch)
}

func TestApproveAndCreatePROpensNextLayerOfStack(t *testing.T) {
	fx, forge := approvalFixture(t, "stack")
	ctx := context.Background()
	a := appliedTask(t, fx, "A", fx.base)
	b := appliedTask(t, fx, "B", a.HeadSHA)
	if _, err := PublishStackLocal(ctx, "W", LeadStackID("L"), "L", nil); err != nil {
		t.Fatal(err)
	}
	c := appliedTask(t, fx, "C", b.HeadSHA)
	approveForLead(t, fx, c, reviewer, true, "applied")
	outcomes, err := PublishApproved(ctx, "W", "L")
	if err != nil {
		t.Fatal(err)
	}
	if len(outcomes) != 1 || outcomes[0].Change != "C" || outcomes[0].Status != "published" || outcomes[0].PRNumber != 3 {
		t.Fatalf("outcomes = %+v", outcomes)
	}
	if len(forge.prs) != 3 || forge.prs[2].Base != forge.prs[1].Head {
		t.Fatalf("PR C is not based on PR B: %+v", forge.prs)
	}
	if head := remoteHead(t, fx, "C"); !strings.HasPrefix(head, c.HeadSHA) {
		t.Fatalf("PR C head = %q, want layer commit %s", head, c.HeadSHA)
	}
	if intent := intentStatus(t, fx, "C"); intent.Status != "published" || intent.PRNumber != 3 {
		t.Fatalf("intent = %+v", intent)
	}
	again, err := PublishApproved(ctx, "W", "L")
	if err != nil || len(again) != 0 || len(forge.prs) != 3 {
		t.Fatalf("second publish = %+v, %v; PRs=%+v", again, err, forge.prs)
	}
}

func TestApproveAndCreatePRFirstTaskTargetsTrunk(t *testing.T) {
	fx, forge := approvalFixture(t, "stack")
	a := appliedTask(t, fx, "A", fx.base)
	approveForLead(t, fx, a, reviewer, true, "applied")
	if _, err := PublishApproved(context.Background(), "W", "L"); err != nil {
		t.Fatal(err)
	}
	if len(forge.prs) != 1 || forge.prs[0].Base != "develop" {
		t.Fatalf("first PR = %+v", forge.prs)
	}
}

func TestApproveAndCreatePRInTrunkModeOpensOwnPRToTrunk(t *testing.T) {
	fx, forge := approvalFixture(t, "trunk")
	a := appliedTask(t, fx, "A", fx.base)
	approveForLead(t, fx, a, reviewer, true, "applied")
	outcomes, err := PublishApproved(context.Background(), "W", "L")
	if err != nil || len(outcomes) != 1 || outcomes[0].Status != "published" {
		t.Fatalf("trunk outcomes = %+v, %v", outcomes, err)
	}
	if len(forge.prs) != 1 || forge.prs[0].Base != "develop" {
		t.Fatalf("trunk PR = %+v", forge.prs)
	}
	publication, found, err := fx.store.Publication(context.Background(), "W", "A")
	if err != nil || !found || publication.StackID != "" {
		t.Fatalf("trunk PR joined a stack: %+v, %v", publication, err)
	}
}

func TestLeadApprovalOpensPROnlyUnderPolicy(t *testing.T) {
	fx, forge := approvalFixture(t, "stack")
	ctx := context.Background()
	a := appliedTask(t, fx, "A", fx.base)
	lead := review.Actor{Kind: "lead", ID: "L"}
	if err := fx.store.SetLeadMayApprovePublish(ctx, "W", false); err != nil {
		t.Fatal(err)
	}
	_, err := review.SubmitForLeadPublishing(ctx, fx.store, "W", "A", a.Number, a.HeadSHA, "approve", "", lead, "L", true)
	if !errors.Is(err, loomgit.NewError(loomgit.ReviewRequired, "", nil)) {
		t.Fatalf("lead approval with the policy off = %v", err)
	}
	if _, found, _ := fx.store.LatestApprovalPublication(ctx, "W", "A", a.Number); found {
		t.Fatal("refused lead approval recorded a publish intent")
	}
	if err := fx.store.SetLeadMayApprovePublish(ctx, "W", true); err != nil {
		t.Fatal(err)
	}
	verdict := approveForLead(t, fx, a, lead, true, "applied")
	if verdict.Kind != "policy" {
		t.Fatalf("lead verdict = %+v", verdict)
	}
	if _, err := PublishApproved(ctx, "W", "L"); err != nil {
		t.Fatal(err)
	}
	if len(forge.prs) != 1 {
		t.Fatalf("lead approval under the policy opened %d PRs", len(forge.prs))
	}
}

func TestHeldApplyOpensNoPRUntilItApplies(t *testing.T) {
	fx, forge := approvalFixture(t, "stack")
	ctx := context.Background()
	a := appliedTask(t, fx, "A", fx.base)
	verdict := approveForLead(t, fx, a, reviewer, true, "apply_pending")
	if err := fx.store.AdvanceApplied(ctx, "apply-A1", "done", "prepared"); err != nil {
		t.Fatal(err)
	}
	for _, held := range []string{"apply_pending", "conflict"} {
		if err := fx.store.SetApprovalFollow(ctx, journal.PendingApproval{Workspace: "W", Lead: "L", Change: "A",
			Revision: a.Number, VerdictID: int(verdict.ID)}, held, nil); err != nil {
			t.Fatal(err)
		}
		if outcomes, err := PublishApproved(ctx, "W", "L"); err != nil || len(outcomes) != 0 || len(forge.prs) != 0 {
			t.Fatalf("%s approval published: %+v, %v; PRs=%+v", held, outcomes, err, forge.prs)
		}
	}
	if intent := intentStatus(t, fx, "A"); intent.Status != "pending" {
		t.Fatalf("held intent = %+v", intent)
	}
	if err := fx.store.AdvanceApplied(ctx, "apply-A1", "prepared", "done"); err != nil {
		t.Fatal(err)
	}
	if err := fx.store.SetApprovalFollow(ctx, journal.PendingApproval{Workspace: "W", Lead: "L", Change: "A",
		Revision: a.Number, VerdictID: int(verdict.ID)}, "applied", nil); err != nil {
		t.Fatal(err)
	}
	if err := ReconcileApprovalPublicationsAt(ctx, fx.storePath); err != nil {
		t.Fatal(err)
	}
	if len(forge.prs) != 1 || intentStatus(t, fx, "A").Status != "published" {
		t.Fatalf("applied approval did not publish: %+v", forge.prs)
	}
}

func TestPublishFailureAfterApplyRetriesOnceWithoutDuplicates(t *testing.T) {
	fx, forge := approvalFixture(t, "stack")
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0)
	previousNow := approvalNow
	approvalNow = func() time.Time { return now }
	t.Cleanup(func() { approvalNow = previousNow })
	a := appliedTask(t, fx, "A", fx.base)
	approveForLead(t, fx, a, reviewer, true, "applied")
	forge.createError = errors.New("provider down")
	outcomes, err := PublishApproved(ctx, "W", "L")
	if err == nil || len(outcomes) != 1 || outcomes[0].Status != "pending" || !strings.Contains(outcomes[0].Reason, "provider down") {
		t.Fatalf("failed publish = %+v, %v", outcomes, err)
	}
	if intent := intentStatus(t, fx, "A"); intent.Status != "pending" || !strings.Contains(intent.Reason, "provider down") {
		t.Fatalf("failed intent = %+v", intent)
	}
	forge.createError = nil
	creates := forge.creates
	if err := ReconcileApprovalPublicationsAt(ctx, fx.storePath); err != nil || forge.creates != creates {
		t.Fatalf("reconcile retried inside the back-off: %v, creates %d -> %d", err, creates, forge.creates)
	}
	now = now.Add(publishRetryDelay)
	for range 3 {
		if err := ReconcileApprovalPublicationsAt(ctx, fx.storePath); err != nil {
			t.Fatal(err)
		}
	}
	if len(forge.prs) != 1 || intentStatus(t, fx, "A").Status != "published" {
		t.Fatalf("retry PRs = %+v", forge.prs)
	}
}

func TestApproveWithoutProviderAppliesAndSaysNotPublished(t *testing.T) {
	fx, _ := approvalFixture(t, "stack")
	ctx := context.Background()
	previous := localPublishProvider
	localPublishProvider = func() (Forge, string, string) { return nil, "", "" }
	t.Cleanup(func() { localPublishProvider = previous })
	called := 0
	previousPublish := approvalPublishChange
	approvalPublishChange = func(context.Context, string, string, string) (Result, error) {
		called++
		return Result{}, errors.New("publish attempted without a provider")
	}
	t.Cleanup(func() { approvalPublishChange = previousPublish })
	a := appliedTask(t, fx, "A", fx.base)
	approveForLead(t, fx, a, reviewer, true, "applied")
	outcomes, err := PublishApproved(ctx, "W", "L")
	if err != nil || len(outcomes) != 1 || outcomes[0].Status != "not_published" ||
		!strings.HasPrefix(outcomes[0].Reason, NoProviderReason) {
		t.Fatalf("no-provider outcome = %+v, %v", outcomes, err)
	}
	for range 3 {
		if err := ReconcileApprovalPublicationsAt(ctx, fx.storePath); err != nil {
			t.Fatal(err)
		}
	}
	if called != 0 {
		t.Fatalf("publish attempted %d times without a provider", called)
	}
	if applied, err := fx.store.RevisionApplied(ctx, "W", "L", "A", a.Number); err != nil || !applied {
		t.Fatalf("no-provider approval did not stay applied: %v, %v", applied, err)
	}
}

func TestApproveOnlyRecordsNoPublishIntent(t *testing.T) {
	fx, forge := approvalFixture(t, "stack")
	ctx := context.Background()
	a := appliedTask(t, fx, "A", fx.base)
	approveForLead(t, fx, a, reviewer, false, "applied")
	if outcomes, err := PublishApproved(ctx, "W", "L"); err != nil || len(outcomes) != 0 || len(forge.prs) != 0 {
		t.Fatalf("Approve only published: %+v, %v; PRs=%+v", outcomes, err, forge.prs)
	}
	if _, found, _ := fx.store.LatestApprovalPublication(ctx, "W", "A", a.Number); found {
		t.Fatal("Approve only recorded a publish intent")
	}
}

func TestSupersededApprovalClosesItsIntent(t *testing.T) {
	fx, forge := approvalFixture(t, "stack")
	ctx := context.Background()
	a := appliedTask(t, fx, "A", fx.base)
	approveForLead(t, fx, a, reviewer, true, "superseded")
	outcomes, err := PublishApproved(ctx, "W", "L")
	if err != nil || len(outcomes) != 1 || outcomes[0].Status != "superseded" || len(forge.prs) != 0 {
		t.Fatalf("superseded outcome = %+v, %v; PRs=%+v", outcomes, err, forge.prs)
	}
}

package publish

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/landing"
	"github.com/tysonthomas9/loomcli/internal/loomgit/pull"
)

// approvedStack builds A, B and C on lead L one approval at a time, each
// opening its PR as the next layer (D29), like the Approve and create PR button.
func approvedStack(t *testing.T, backend string) (fixture, *fakeForge, []loomgit.Revision) {
	t.Helper()
	useExplicitGitIdentity(t)
	fx := newFixture(t)
	configureLocalWorkspace(t, fx)
	ctx := context.Background()
	git(t, fx.repo, "push", "-q", "origin", fx.base+":refs/heads/develop")
	git(t, fx.repo, "branch", "-m", "loom/ws/W/interactive/L")
	if err := fx.store.SaveWorkingAreas(ctx, []journal.WorkingArea{{Workspace: "W", Lead: "L", Repo: "repo",
		Path: fx.repo, Branch: "loom/ws/W/interactive/L", BaseSHA: fx.base, Mode: "worktree"}}); err != nil {
		t.Fatal(err)
	}
	if err := fx.store.SetDeliveryMode(ctx, "W", "stack"); err != nil {
		t.Fatal(err)
	}
	forge := &fakeForge{}
	useLocalForge(t, forge)
	if backend == "native" {
		native := &fakeNativeForge{fakeForge: forge}
		localPublishProvider = func() (Forge, string, string) { return native, "fixture-token", "owner/repo" }
	}
	t.Setenv("GITHUB_TOKEN", "fixture-token")
	var revisions []loomgit.Revision
	parent := fx.base
	for _, change := range []string{"A", "B", "C"} {
		revision := appliedTask(t, fx, change, parent)
		approveForLead(t, fx, revision, reviewer, true, "applied")
		outcomes, err := PublishApproved(ctx, "W", "L", nil)
		if err != nil || len(outcomes) != 1 || outcomes[0].Status != "published" {
			t.Fatalf("approve %s: outcomes = %+v, err = %v", change, outcomes, err)
		}
		revisions, parent = append(revisions, revision), revision.HeadSHA
	}
	if len(forge.prs) != 3 || forge.prs[1].Base != forge.prs[0].Head || forge.prs[2].Base != forge.prs[1].Head {
		t.Fatalf("approved PRs are not stacked: %+v", forge.prs)
	}
	if recorded, err := fx.store.StackBackend(ctx, "W", LeadStackID("L")); err != nil || recorded != backend {
		t.Fatalf("stack backend = %q, %v; want %s", recorded, err, backend)
	}
	return fx, forge, revisions
}

func dependents(t *testing.T, change string) []string {
	t.Helper()
	found, err := landing.LocalDependents(context.Background(), "W", change)
	if err != nil {
		t.Fatal(err)
	}
	tasks := make([]string, 0, len(found))
	for _, dependent := range found {
		tasks = append(tasks, dependent.Task+"@"+dependent.Repo)
	}
	return tasks
}

func wantDependents(t *testing.T, want map[string][]string) {
	t.Helper()
	for change, tasks := range want {
		got := dependents(t, change)
		if len(got) != len(tasks) {
			t.Fatalf("dependents of %s = %v, want %v", change, got, tasks)
		}
		for index := range tasks {
			if got[index] != tasks[index] {
				t.Fatalf("dependents of %s = %v, want %v", change, got, tasks)
			}
		}
	}
}

func TestApproveBuiltStackDependentsFollowPublishedOrder(t *testing.T) {
	fx, forge, revisions := approvedStack(t, "loom")
	ctx := context.Background()
	want := map[string][]string{"A": {"task-B@repo"}, "B": {"task-C@repo"}, "C": {}}
	wantDependents(t, want)
	// Re-publishing and re-approving the same stack never lists a task twice.
	for range 2 {
		if _, err := PublishLeadChangeLocal(ctx, "W", "L", "C"); err != nil {
			t.Fatal(err)
		}
	}
	reapprove(t, fx, revisions[1])
	if _, err := PublishApproved(ctx, "W", "L", nil); err != nil {
		t.Fatal(err)
	}
	wantDependents(t, want)
	if len(forge.prs) != 3 {
		t.Fatalf("republish opened PRs: %+v", forge.prs)
	}
	// A declared lineage to the same change is listed once.
	if err := fx.store.RecordLocalLineage(ctx, journal.LocalLineage{Workspace: "W", Task: "task-C", Repo: "repo",
		PredecessorChange: "B", PredecessorRevision: revisions[1].Number, BaseSHA: revisions[1].HeadSHA}); err != nil {
		t.Fatal(err)
	}
	wantDependents(t, want)
	// Approve-built order never gates trunk publishing or approval following.
	if predecessor, err := fx.store.DependencyForChange(ctx, "W", "B"); err != nil || predecessor != "" {
		t.Fatalf("declared dependency of B = %q, %v", predecessor, err)
	}
	if _, err := fx.store.LocalLineage(ctx, "W", "task-B", "repo"); !errors.Is(err, journal.ErrNotFound) {
		t.Fatalf("approve wrote a declared lineage: %v", err)
	}
}

func TestTrunkModeApprovalHasNoStackDependents(t *testing.T) {
	fx, forge := approvalFixture(t, "trunk")
	a := appliedTask(t, fx, "A", fx.base)
	approveForLead(t, fx, a, reviewer, true, "applied")
	if _, err := PublishApproved(context.Background(), "W", "L", nil); err != nil {
		t.Fatal(err)
	}
	if len(forge.prs) != 1 {
		t.Fatalf("trunk PRs = %+v", forge.prs)
	}
	wantDependents(t, map[string][]string{"A": {}})
}

// A crash after the stack publication commits, before the approval intent
// records its outcome, still leaves landing everything it needs.
func TestApproveBuiltDependentsSurviveCrashAfterPublish(t *testing.T) {
	fx, forge, _ := approvedStack(t, "loom")
	ctx := context.Background()
	d := appliedTask(t, fx, "D", git(t, fx.repo, "rev-parse", "HEAD"))
	approveForLead(t, fx, d, reviewer, true, "applied")
	previous := approvalPublishChange
	approvalPublishChange = func(ctx context.Context, workspace, lead, change string) (Result, error) {
		if _, err := previous(ctx, workspace, lead, change); err != nil {
			return Result{}, err
		}
		return Result{}, errors.New("crash after publish")
	}
	t.Cleanup(func() { approvalPublishChange = previous })
	if _, err := PublishApproved(ctx, "W", "L", nil); err == nil {
		t.Fatal("injected crash did not surface")
	}
	if intent := intentStatus(t, fx, "D"); intent.Status != "pending" {
		t.Fatalf("intent after crash = %+v", intent)
	}
	wantDependents(t, map[string][]string{"C": {"task-D@repo"}})
	trunk := landBottom(t, fx, forge)
	options := landing.Options{Dependents: landing.LocalDependents, Restack: RestackOffer}
	if err := landing.ReconcileWithOptions(ctx, fx.store, forge, options); err != nil {
		t.Fatal(err)
	}
	if publication, _, err := fx.store.Publication(ctx, "W", "B"); err != nil || publication.Trunk != "develop" {
		t.Fatalf("B after crash and landing = %+v, %v (trunk %s)", publication, err, trunk)
	}
}

// Two approvals published at the same time each publish the whole stack under
// its lease; the dependents read the final publication records.
func TestConcurrentApprovalsLeaveOneStackOrder(t *testing.T) {
	fx, forge := approvalFixture(t, "stack")
	ctx := context.Background()
	a := appliedTask(t, fx, "A", fx.base)
	approveForLead(t, fx, a, reviewer, true, "applied")
	if _, err := PublishApproved(ctx, "W", "L", nil); err != nil {
		t.Fatal(err)
	}
	b := appliedTask(t, fx, "B", a.HeadSHA)
	c := appliedTask(t, fx, "C", b.HeadSHA)
	approveForLead(t, fx, b, reviewer, true, "applied")
	approveForLead(t, fx, c, reviewer, true, "applied")
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for index := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[index] = PublishApproved(ctx, "W", "L", nil)
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(forge.prs) != 3 || forge.prs[1].Base != forge.prs[0].Head || forge.prs[2].Base != forge.prs[1].Head {
		t.Fatalf("concurrent approvals PRs = %+v", forge.prs)
	}
	wantDependents(t, map[string][]string{"A": {"task-B@repo"}, "B": {"task-C@repo"}, "C": {}})
}

func TestUnapplyDropsLayerFromApproveBuiltDependents(t *testing.T) {
	fx, forge, _ := approvedStack(t, "loom")
	ctx := context.Background()
	if _, err := pull.UnapplyLocal(ctx, fx.repo, "B", "unapply-B"); err != nil {
		t.Fatal(err)
	}
	// B left the stack at once; C's PR still sits on B's branch until republished.
	wantDependents(t, map[string][]string{"A": {}, "B": {"task-C@repo"}})
	if _, err := PublishLeadChangeLocal(ctx, "W", "L", "C"); err != nil {
		t.Fatal(err)
	}
	if forge.prs[2].Base != forge.prs[0].Head {
		t.Fatalf("C PR after republish = %+v", forge.prs[2])
	}
	wantDependents(t, map[string][]string{"A": {"task-C@repo"}, "B": {}})
	if _, err := pull.UnapplyLocal(ctx, fx.repo, "A", "unapply-A"); err != nil {
		t.Fatal(err)
	}
	// C's PR still sits on A's: if A's PR merges, C must still be restacked.
	wantDependents(t, map[string][]string{"A": {"task-C@repo"}})
	if _, err := PublishLeadChangeLocal(ctx, "W", "L", "C"); err != nil {
		t.Fatal(err)
	}
	if forge.prs[2].Base != "develop" {
		t.Fatalf("C PR after A left = %+v", forge.prs[2])
	}
	wantDependents(t, map[string][]string{"A": {}, "C": {}})
}

// landBottom squash-merges A onto develop on the provider.
func landBottom(t *testing.T, fx fixture, forge *fakeForge) string {
	t.Helper()
	merged := squashOnto(t, fx, fx.base, "develop", "A")[0]
	mergePR(&forge.prs[0], merged)
	return merged
}

func TestLandingRestacksApproveBuiltStackLoom(t *testing.T) {
	fx, forge, revisions := approvedStack(t, "loom")
	ctx := context.Background()
	trunk := landBottom(t, fx, forge)
	options := landing.Options{Dependents: landing.LocalDependents, Restack: RestackOffer}
	if err := landing.RunAtWithOptions(ctx, filepath.Join(config.GetConfigDir(), "loomgit", "store.db"),
		forge, "", options); err != nil {
		t.Fatal(err)
	}
	if status, err := fx.store.LandingStatus(ctx, "W", "A"); err != nil || status.State != "landed" {
		t.Fatalf("A landing = %+v, %v", status, err)
	}
	restacked, err := fx.store.SourceRevision(ctx, "W", "B")
	if err != nil || restacked <= revisions[1].Number {
		t.Fatalf("B restacked revision = %d, %v", restacked, err)
	}
	derived, err := fx.store.GetRevision(ctx, "W", "B", restacked)
	if err != nil || derived.Operation != "restack" || derived.BaseSHA != trunk {
		t.Fatalf("B derived revision = %+v, %v", derived, err)
	}
	publication, found, err := fx.store.Publication(ctx, "W", "B")
	if err != nil || !found || publication.Trunk != "develop" || publication.Head != derived.HeadSHA {
		t.Fatalf("B publication = %+v, %v", publication, err)
	}
	if forge.prs[1].Base != "develop" || forge.prs[2].Base != forge.prs[1].Head {
		t.Fatalf("PRs after restack: %+v", forge.prs)
	}
	if got := git(t, fx.remote, "rev-parse", "refs/heads/"+publication.Branch); got != publication.Head {
		t.Fatalf("remote B = %s, want %s", got, publication.Head)
	}
	offers, err := fx.store.OpenRestackOffers(ctx)
	if err != nil || len(offers) != 0 {
		t.Fatalf("open restack offers = %+v, %v", offers, err)
	}
	// The new bottom B lands next; C follows onto trunk the same way.
	trunk = squashOnto(t, fx, trunk, "develop", "B")[0]
	mergePR(&forge.prs[1], trunk)
	if err := landing.ReconcileWithOptions(ctx, fx.store, forge, options); err != nil {
		t.Fatal(err)
	}
	restacked, err = fx.store.SourceRevision(ctx, "W", "C")
	if err != nil || restacked <= revisions[2].Number {
		t.Fatalf("C restacked revision = %d, %v", restacked, err)
	}
	if derived, err = fx.store.GetRevision(ctx, "W", "C", restacked); err != nil || derived.BaseSHA != trunk {
		t.Fatalf("C derived revision = %+v, %v", derived, err)
	}
	if forge.prs[2].Base != "develop" {
		t.Fatalf("C PR after B lands: %+v", forge.prs[2])
	}
}

func TestLandingRestacksApproveBuiltStackNative(t *testing.T) {
	fx, forge, revisions := approvedStack(t, "native")
	ctx := context.Background()
	trunk := landBottom(t, fx, forge)
	// The provider restacks B and C onto trunk itself; Loom adopts those heads.
	provider := squashOnto(t, fx, trunk, forge.prs[1].Head, "B")[0]
	forge.prs[1].Base, forge.prs[1].HeadSHA = "develop", provider
	forge.prs[2].HeadSHA = squashOnto(t, fx, provider, forge.prs[2].Head, "C")[0]
	options := landing.Options{Dependents: landing.LocalDependents, Restack: RestackOffer}
	if err := landing.ReconcileWithOptions(ctx, fx.store, forge, options); err != nil {
		t.Fatalf("native restack of approve-built stack: %v", err)
	}
	publication, found, err := fx.store.Publication(ctx, "W", "B")
	if err != nil || !found || publication.Head != provider || publication.Trunk != "develop" {
		t.Fatalf("B publication = %+v, %v", publication, err)
	}
	if revision, err := fx.store.SourceRevision(ctx, "W", "B"); err != nil || revision <= revisions[1].Number {
		t.Fatalf("B derived revision = %d, %v", revision, err)
	}
	offers, err := fx.store.OpenRestackOffers(ctx)
	if err != nil || len(offers) != 0 {
		t.Fatalf("open restack offers = %+v, %v", offers, err)
	}
	// The new bottom B lands next; the provider moves C onto trunk.
	trunk = squashOnto(t, fx, trunk, "develop", "B")[0]
	mergePR(&forge.prs[1], trunk)
	provider = squashOnto(t, fx, trunk, forge.prs[2].Head, "C")[0]
	forge.prs[2].Base, forge.prs[2].HeadSHA = "develop", provider
	if err := landing.ReconcileWithOptions(ctx, fx.store, forge, options); err != nil {
		t.Fatalf("native restack of C: %v", err)
	}
	publication, _, err = fx.store.Publication(ctx, "W", "C")
	if err != nil || publication.Head != provider || publication.Trunk != "develop" {
		t.Fatalf("C publication = %+v, %v", publication, err)
	}
	if revision, err := fx.store.SourceRevision(ctx, "W", "C"); err != nil || revision <= revisions[2].Number {
		t.Fatalf("C derived revision = %d, %v", revision, err)
	}
}

// A stack declared with `loom stack` keeps only its declared lineage: its
// publication order adds no dependents.
func TestDeclaredStackPublicationAddsNoDependents(t *testing.T) {
	fixture, _, _ := landingStackFixture(t, false, "loom")
	ctx := context.Background()
	if _, err := fixture.store.DriverChange(ctx, "W", "task-B", "repo", "B"); err != nil {
		t.Fatal(err)
	}
	if publication, found, err := fixture.store.Publication(ctx, "W", "B"); err != nil || !found || publication.StackID != "feature-1" {
		t.Fatalf("declared publication = %+v, %v", publication, err)
	}
	found, err := fixture.store.DependentsOf(ctx, "W", "A")
	if err != nil || len(found) != 0 {
		t.Fatalf("declared stack dependents = %+v, %v", found, err)
	}
}

// The verifier's race: B publishes AB, C publishes ABC, then B's late publish
// finishes. The stack order must still end as A, B, C.
func TestLateApprovalPublishKeepsLaterLayer(t *testing.T) {
	fx, forge := approvalFixture(t, "stack")
	ctx := context.Background()
	a := appliedTask(t, fx, "A", fx.base)
	b := appliedTask(t, fx, "B", a.HeadSHA)
	if _, err := PublishLeadChangeLocal(ctx, "W", "L", "B"); err != nil {
		t.Fatal(err)
	}
	wantDependents(t, map[string][]string{"A": {"task-B@repo"}, "B": {}})
	appliedTask(t, fx, "C", b.HeadSHA)
	if _, err := PublishLeadChangeLocal(ctx, "W", "L", "C"); err != nil {
		t.Fatal(err)
	}
	if _, err := PublishLeadChangeLocal(ctx, "W", "L", "B"); err != nil {
		t.Fatal(err)
	}
	if len(forge.prs) != 3 || forge.prs[2].Base != forge.prs[1].Head {
		t.Fatalf("PRs after late B publish = %+v", forge.prs)
	}
	wantDependents(t, map[string][]string{"A": {"task-B@repo"}, "B": {"task-C@repo"}, "C": {}})
}

// A declared stack is not a lead stack even when its name starts with "lead-";
// only the lead's exact stack ID reads publication order as dependents.
func TestOnlyTheLeadsExactStackIDAddsDependents(t *testing.T) {
	for stackID, want := range map[string][]string{"lead-feature": {}, LeadStackID("L"): {"task-B"}} {
		fixture := newFixture(t)
		ctx := context.Background()
		first := stackRevision(t, fixture, "A", 1, fixture.base)
		stackRevision(t, fixture, "B", 1, first.HeadSHA)
		request := fixture.request()
		request.forge = &fakeForge{}
		if _, err := publishStack(ctx, fixture.store, StackRequest{Request: request, StackID: stackID,
			Changes: []string{"A", "B"}}); err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.store.DriverChange(ctx, "W", "task-B", "repo", "B"); err != nil {
			t.Fatal(err)
		}
		found, err := fixture.store.DependentsOf(ctx, "W", "A")
		if err != nil || len(found) != len(want) || (len(want) == 1 && found[0].Task != want[0]) {
			t.Fatalf("stack %s dependents = %+v, %v; want %v", stackID, found, err, want)
		}
	}
}

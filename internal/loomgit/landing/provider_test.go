package landing

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/stackpublish"
)

func publishProviderChange(t *testing.T, fixture *fixture, change, head, prior string, number int) {
	t.Helper()
	ctx := context.Background()
	publication := journal.Publication{Workspace: "W", Change: change, Repo: fixture.source,
		Branch: "loom/ws/W/change/" + change, Trunk: "main", Slug: "owner/repo", Head: head,
		StackID: "feature", Prior: prior}
	if err := fixture.store.BeginPublication(ctx, publication); err != nil {
		t.Fatal(err)
	}
	publication.Phase, publication.PRNumber = "done", number
	if err := fixture.store.AdvancePublication(ctx, publication); err != nil {
		t.Fatal(err)
	}
}

func sourceRevision(t *testing.T, fixture *fixture, change, head string) loomgit.Revision {
	t.Helper()
	ctx := context.Background()
	revision, err := fixture.store.ReserveRevision(ctx, loomgit.Revision{Workspace: "W", Change: change,
		RequestID: "source-" + change, Kind: "source", Outcome: "completed", BaseSHA: fixture.initial,
		TreeHash: git(t, fixture.source, "rev-parse", head+"^{tree}"), SourceHeadSHA: head})
	if err != nil {
		t.Fatal(err)
	}
	revision.HeadSHA = head
	if err := fixture.store.FinishRevision(ctx, revision); err != nil {
		t.Fatal(err)
	}
	return revision
}

func providerBranch(t *testing.T, fixture *fixture) string {
	t.Helper()
	git(t, fixture.source, "switch", "-q", "-c", "loom/ws/W/change/B")
	old := fixture.commit(t, "B patch")
	git(t, fixture.source, "push", "-q", "origin", "HEAD:refs/heads/loom/ws/W/change/B")
	publishProviderChange(t, fixture, "B", old, "", 42)
	sourceRevision(t, fixture, "B", old)
	return old
}

func TestProviderRestackAdoptsEquivalentHeadAndCarriesVerdict(t *testing.T) {
	fixture := newFixture(t)
	old := providerBranch(t, fixture)
	if _, err := fixture.store.RecordVerdict(context.Background(), loomgit.Verdict{Workspace: "W", Change: "B",
		Number: 1, HeadSHA: old, Kind: "approve", ActorKind: "human", ActorID: "reviewer"}); err != nil {
		t.Fatal(err)
	}
	git(t, fixture.source, "switch", "-q", "main")
	if err := os.WriteFile(filepath.Join(fixture.source, "trunk"), []byte("trunk\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, fixture.source, "add", "trunk")
	git(t, fixture.source, "commit", "-qm", "trunk update")
	newBase := git(t, fixture.source, "rev-parse", "HEAD")
	git(t, fixture.source, "push", "-q", "origin", "main")
	git(t, fixture.source, "switch", "-q", "-c", "restacked")
	git(t, fixture.source, "cherry-pick", old)
	newHead := git(t, fixture.source, "rev-parse", "HEAD")
	git(t, fixture.source, "push", "-q", "--force", "origin", "HEAD:refs/heads/loom/ws/W/change/B")
	fixture.forge.pull = stackpublish.PR{Number: 42, Head: "loom/ws/W/change/B", HeadSHA: newHead, Base: "main", State: "open"}
	if err := Reconcile(context.Background(), fixture.store, fixture.forge); err != nil {
		t.Fatal(err)
	}
	number, head, err := fixture.store.LatestReadyRevision(context.Background(), "W", "B")
	if err != nil || number != 2 || head != newHead {
		t.Fatalf("derived revision = %d %s, %v", number, head, err)
	}
	revision, err := fixture.store.GetRevision(context.Background(), "W", "B", number)
	if err != nil || revision.Operation != "provider_restack" || revision.BaseSHA != newBase || revision.DerivedFromNumber != 1 {
		t.Fatalf("derived revision = %+v, %v", revision, err)
	}
	verdict, err := fixture.store.LatestVerdict(context.Background(), revision)
	if err != nil || verdict.Kind != "carried" {
		t.Fatalf("carried verdict = %+v, %v", verdict, err)
	}
	before, err := fixture.store.PendingEvents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := Reconcile(context.Background(), fixture.store, fixture.forge); err != nil {
		t.Fatal(err)
	}
	after, err := fixture.store.PendingEvents(context.Background())
	if err != nil || len(after) != len(before) {
		t.Fatalf("repeat observation created events: %d -> %d, %v", len(before), len(after), err)
	}
}

func TestProviderManualPushRecordsDivergenceAndFeedback(t *testing.T) {
	fixture := newFixture(t)
	providerBranch(t, fixture)
	newHead := fixture.commit(t, "human edit")
	git(t, fixture.source, "push", "-q", "origin", "HEAD:refs/heads/loom/ws/W/change/B")
	fixture.forge.pull = stackpublish.PR{Number: 42, Head: "loom/ws/W/change/B", HeadSHA: newHead, Base: "main", State: "open"}
	if err := Reconcile(context.Background(), fixture.store, fixture.forge); err != nil {
		t.Fatal(err)
	}
	status, err := fixture.store.LandingStatus(context.Background(), "W", "B")
	if err != nil || status.State != "diverged" {
		t.Fatalf("provider status = %+v, %v", status, err)
	}
	number, _, err := fixture.store.LatestReadyRevision(context.Background(), "W", "B")
	if err != nil || number != 1 {
		t.Fatalf("manual push created revision %d, %v", number, err)
	}
	events, err := fixture.store.PendingEvents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var feedback bool
	for _, event := range events {
		feedback = feedback || event.Kind == "git.feedback_recorded" && strings.Contains(string(event.Payload), newHead)
	}
	if !feedback {
		t.Fatal("provider push created no durable feedback event")
	}
}

func TestRunOnceObservesProviderPushFromConfiguredJournal(t *testing.T) {
	fixture := newFixture(t)
	providerBranch(t, fixture)
	t.Setenv("LOOM_CONFIG_DIR", filepath.Dir(fixture.source))
	newHead := fixture.commit(t, "human edit")
	git(t, fixture.source, "push", "-q", "origin", "HEAD:refs/heads/loom/ws/W/change/B")
	fixture.forge.pull = stackpublish.PR{Number: 42, Head: "loom/ws/W/change/B", HeadSHA: newHead, Base: "main", State: "open"}
	ctx := context.Background()
	if err := RunOnceWithOptions(ctx, Options{Forge: fixture.forge}); err != nil {
		t.Fatal(err)
	}
	status, err := Status(ctx, "W", "B")
	if err != nil || status.State != "diverged" {
		t.Fatalf("configured status = %+v, %v", status, err)
	}
	before, err := fixture.store.PendingEvents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := RunOnceWithOptions(ctx, Options{Forge: fixture.forge}); err != nil {
		t.Fatal(err)
	}
	after, err := fixture.store.PendingEvents(ctx)
	if err != nil || len(after) != len(before) {
		t.Fatalf("repeat run created events: %d -> %d, %v", len(before), len(after), err)
	}
}

func TestProviderRestackChangedPatchRequiresNewVerdict(t *testing.T) {
	fixture := newFixture(t)
	old := providerBranch(t, fixture)
	if _, err := fixture.store.RecordVerdict(context.Background(), loomgit.Verdict{Workspace: "W", Change: "B",
		Number: 1, HeadSHA: old, Kind: "approve", ActorKind: "human", ActorID: "reviewer"}); err != nil {
		t.Fatal(err)
	}
	git(t, fixture.source, "switch", "-q", "main")
	if err := os.WriteFile(filepath.Join(fixture.source, "trunk"), []byte("trunk\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, fixture.source, "add", "trunk")
	git(t, fixture.source, "commit", "-qm", "trunk update")
	git(t, fixture.source, "push", "-q", "origin", "main")
	git(t, fixture.source, "switch", "-q", "-c", "restacked")
	newHead := fixture.commit(t, "different patch")
	git(t, fixture.source, "push", "-q", "--force", "origin", "HEAD:refs/heads/loom/ws/W/change/B")
	fixture.forge.pull = stackpublish.PR{Number: 42, Head: "loom/ws/W/change/B", HeadSHA: newHead, Base: "main", State: "open"}
	if err := Reconcile(context.Background(), fixture.store, fixture.forge); err != nil {
		t.Fatal(err)
	}
	revision, err := fixture.store.GetRevision(context.Background(), "W", "B", 2)
	if err != nil || revision.HeadSHA != newHead || revision.Operation != "provider_restack" {
		t.Fatalf("unreviewed derived revision = %+v, %v", revision, err)
	}
	if _, err := fixture.store.LatestVerdict(context.Background(), revision); err == nil {
		t.Fatal("changed patch inherited the source verdict")
	}
}

func TestProviderClosedLayerAbandonsDependents(t *testing.T) {
	fixture := newFixture(t)
	publishProviderChange(t, fixture, "A", fixture.initial, "", 41)
	publishProviderChange(t, fixture, "B", fixture.initial, fixture.initial, 42)
	fixture.forge.pulls = map[int]stackpublish.PR{
		41: {Number: 41, Head: "loom/ws/W/change/A", HeadSHA: fixture.initial, Base: "main", State: "closed"},
		42: {Number: 42, Head: "loom/ws/W/change/B", HeadSHA: fixture.initial, Base: "loom/ws/W/change/A", State: "open"},
	}
	if err := Reconcile(context.Background(), fixture.store, fixture.forge); err != nil {
		t.Fatal(err)
	}
	status, err := fixture.store.LandingStatus(context.Background(), "W", "B")
	if err != nil || status.State != "dependency_abandoned" {
		t.Fatalf("dependent status = %+v, %v", status, err)
	}
}

func TestProviderRetargetAfterMergeRecordsTrunkBase(t *testing.T) {
	fixture := newFixture(t)
	publishProviderChange(t, fixture, "A", fixture.initial, "", 41)
	publishProviderChange(t, fixture, "B", fixture.initial, fixture.initial, 42)
	fixture.forge.pulls = map[int]stackpublish.PR{
		41: {Number: 41, Head: "loom/ws/W/change/A", HeadSHA: fixture.initial, Base: "main",
			State: "closed", Merged: true, MergeCommitSHA: fixture.initial},
		42: {Number: 42, Head: "loom/ws/W/change/B", HeadSHA: fixture.initial, Base: "main", State: "open"},
	}
	if err := Reconcile(context.Background(), fixture.store, fixture.forge); err != nil {
		t.Fatal(err)
	}
	landed, err := fixture.store.LandingStatus(context.Background(), "W", "A")
	if err != nil || landed.State != "landed" {
		t.Fatalf("predecessor = %+v, %v", landed, err)
	}
	observation, found, err := fixture.store.ProviderObservation(context.Background(), "W", "B")
	if err != nil || !found || observation.Base != "main" || observation.State != "open" {
		t.Fatalf("retarget = %+v, found=%v, %v", observation, found, err)
	}
}

func TestProviderUnexpectedRetargetIsDurableDrift(t *testing.T) {
	fixture := newFixture(t)
	publishProviderChange(t, fixture, "A", fixture.initial, "", 41)
	publishProviderChange(t, fixture, "B", fixture.initial, fixture.initial, 42)
	fixture.forge.pulls = map[int]stackpublish.PR{
		41: {Number: 41, Head: "loom/ws/W/change/A", HeadSHA: fixture.initial, Base: "main", State: "open"},
		42: {Number: 42, Head: "loom/ws/W/change/B", HeadSHA: fixture.initial, Base: "main", State: "open"},
	}
	if err := Reconcile(context.Background(), fixture.store, fixture.forge); err != nil {
		t.Fatal(err)
	}
	status, err := fixture.store.LandingStatus(context.Background(), "W", "B")
	if err != nil || status.State != "diverged" {
		t.Fatalf("retarget status = %+v, %v", status, err)
	}
	observation, found, err := fixture.store.ProviderObservation(context.Background(), "W", "B")
	if err != nil || !found || observation.Base != "main" {
		t.Fatalf("retarget observation = %+v, %v, %v", observation, found, err)
	}
}

func TestNativeAutoRestackAfterMergeIsNotDrift(t *testing.T) {
	fixture := newFixture(t)
	ctx := context.Background()
	heads := map[string]string{}
	trunk, prior := "main", ""
	for index, change := range []string{"A", "B", "C"} {
		branch := "loom/ws/W/change/" + change
		git(t, fixture.source, "switch", "-q", "-c", branch)
		heads[change] = fixture.commit(t, change+" patch")
		git(t, fixture.source, "push", "-q", "origin", "HEAD:refs/heads/"+branch)
		publication := journal.Publication{Workspace: "W", Change: change, Repo: fixture.source, Branch: branch,
			Trunk: trunk, Slug: "owner/repo", Head: heads[change], StackID: "feature", Prior: prior}
		if err := fixture.store.BeginPublication(ctx, publication); err != nil {
			t.Fatal(err)
		}
		publication.Phase, publication.PRNumber = "done", 41+index
		if err := fixture.store.AdvancePublication(ctx, publication); err != nil {
			t.Fatal(err)
		}
		sourceRevision(t, fixture, change, heads[change])
		trunk, prior = branch, heads[change]
	}
	if err := fixture.store.RecordStackBackend(ctx, "W", "feature", "native"); err != nil {
		t.Fatal(err)
	}
	git(t, fixture.source, "switch", "-q", "main")
	git(t, fixture.source, "merge", "-q", "--no-ff", "-m", "merge A", "loom/ws/W/change/A")
	merge := git(t, fixture.source, "rev-parse", "HEAD")
	git(t, fixture.source, "push", "-q", "origin", "main")
	git(t, fixture.source, "switch", "-q", "-c", "github-restack")
	git(t, fixture.source, "cherry-pick", heads["B"])
	restackedB := git(t, fixture.source, "rev-parse", "HEAD")
	git(t, fixture.source, "cherry-pick", heads["C"])
	restackedC := git(t, fixture.source, "rev-parse", "HEAD")
	git(t, fixture.source, "push", "-q", "--force", "origin", restackedB+":refs/heads/loom/ws/W/change/B",
		restackedC+":refs/heads/loom/ws/W/change/C")
	fixture.forge.pulls = map[int]stackpublish.PR{
		41: {Number: 41, Head: "loom/ws/W/change/A", HeadSHA: heads["A"], Base: "main", State: "closed", Merged: true, MergeCommitSHA: merge},
		42: {Number: 42, Head: "loom/ws/W/change/B", HeadSHA: restackedB, Base: "main", State: "open"},
		43: {Number: 43, Head: "loom/ws/W/change/C", HeadSHA: restackedC, Base: "loom/ws/W/change/B", State: "open"},
	}
	if err := Reconcile(ctx, fixture.store, fixture.forge); err != nil {
		t.Fatal(err)
	}
	for _, change := range []string{"B", "C"} {
		observation, found, err := fixture.store.ProviderObservation(ctx, "W", change)
		if err != nil || !found || observation.State != "native_restack" {
			t.Fatalf("%s observation = %+v, found=%v, %v", change, observation, found, err)
		}
		status, err := fixture.store.LandingStatus(ctx, "W", change)
		if err != nil || status.State == "diverged" {
			t.Fatalf("%s native auto-restack status = %+v, %v", change, status, err)
		}
		number, _, err := fixture.store.LatestReadyRevision(ctx, "W", change)
		if err != nil || number != 1 {
			t.Fatalf("%s native auto-restack created provider revision %d, %v", change, number, err)
		}
	}
}

func TestProviderPRWithoutHeadSkipsObservationAndStillLands(t *testing.T) {
	fixture := newFixture(t)
	publishProviderChange(t, fixture, "A", fixture.initial, "", 41)
	fixture.forge.pull = stackpublish.PR{Number: 41, Head: "loom/ws/W/change/A", State: "closed",
		Merged: true, MergeCommitSHA: fixture.initial}
	if err := Reconcile(context.Background(), fixture.store, fixture.forge); err != nil {
		t.Fatal(err)
	}
	if _, found, err := fixture.store.ProviderObservation(context.Background(), "W", "A"); err != nil || found {
		t.Fatalf("incomplete provider PR recorded an observation: found=%v, %v", found, err)
	}
	status, err := fixture.store.LandingStatus(context.Background(), "W", "A")
	if err != nil || status.State != "landed" {
		t.Fatalf("landing status = %+v, %v", status, err)
	}
}

// stackedLineage publishes A-C with distinct commits, each PR based on the
// branch below it, and squash-merges A onto main as a new commit.
func stackedLineage(t *testing.T, native bool) (*fixture, map[string]string) {
	t.Helper()
	fixture, ctx := newFixture(t), context.Background()
	heads, trunk, prior := map[string]string{}, "main", ""
	for index, change := range []string{"A", "B", "C"} {
		branch := "loom/ws/W/change/" + change
		git(t, fixture.source, "switch", "-q", "-c", branch)
		heads[change] = fixture.commit(t, change+" patch")
		git(t, fixture.source, "push", "-q", "origin", "HEAD:refs/heads/"+branch)
		publication := journal.Publication{Workspace: "W", Change: change, Repo: fixture.source, Branch: branch,
			Trunk: trunk, Slug: "owner/repo", Head: heads[change], StackID: "feature", Prior: prior}
		if err := fixture.store.BeginPublication(ctx, publication); err != nil {
			t.Fatal(err)
		}
		publication.Phase, publication.PRNumber = "done", 41+index
		if err := fixture.store.AdvancePublication(ctx, publication); err != nil {
			t.Fatal(err)
		}
		sourceRevision(t, fixture, change, heads[change])
		trunk, prior = branch, heads[change]
	}
	if native {
		if err := fixture.store.RecordStackBackend(ctx, "W", "feature", "native"); err != nil {
			t.Fatal(err)
		}
	}
	heads["squash A"] = squashOntoMain(t, fixture, heads["A"])
	return fixture, heads
}

func squashOntoMain(t *testing.T, fixture *fixture, layer string) string {
	t.Helper()
	git(t, fixture.source, "switch", "-q", "main")
	git(t, fixture.source, "cherry-pick", "--no-commit", layer)
	git(t, fixture.source, "commit", "-qm", "squash "+layer)
	git(t, fixture.source, "push", "-q", "origin", "main")
	return git(t, fixture.source, "rev-parse", "HEAD")
}

// adoptNativeRestack has GitHub restack B onto the squashed A and C onto the
// new B, then records Loom's adoption of both heads, as native restack does.
func adoptNativeRestack(t *testing.T, fixture *fixture, heads map[string]string) {
	t.Helper()
	ctx := context.Background()
	git(t, fixture.source, "switch", "-q", "-c", "github-restack", heads["squash A"])
	git(t, fixture.source, "cherry-pick", heads["B"])
	heads["restacked B"] = git(t, fixture.source, "rev-parse", "HEAD")
	git(t, fixture.source, "cherry-pick", heads["C"])
	heads["restacked C"] = git(t, fixture.source, "rev-parse", "HEAD")
	git(t, fixture.source, "push", "-q", "--force", "origin", heads["restacked B"]+":refs/heads/loom/ws/W/change/B",
		heads["restacked C"]+":refs/heads/loom/ws/W/change/C")
	b, _, errB := fixture.store.Publication(ctx, "W", "B")
	c, _, errC := fixture.store.Publication(ctx, "W", "C")
	if errB != nil || errC != nil {
		t.Fatal(errB, errC)
	}
	b.Trunk = "main"
	if err := fixture.store.AdoptStackPublications(ctx, []journal.Publication{b, c},
		map[string]string{"B": heads["restacked B"], "C": heads["restacked C"]}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.MarkLanded(ctx, "W", "A", "merge_commit"); err != nil {
		t.Fatal(err)
	}
}

func lineagePulls(heads map[string]string, c stackpublish.PR) map[int]stackpublish.PR {
	c.Number, c.Head, c.State = 43, "loom/ws/W/change/C", "open"
	return map[int]stackpublish.PR{
		41: {Number: 41, Head: "loom/ws/W/change/A", HeadSHA: heads["A"], Base: "main", State: "closed",
			Merged: true, MergeCommitSHA: heads["squash A"]},
		42: {Number: 42, Head: "loom/ws/W/change/B", HeadSHA: heads["restacked B"], Base: "main", State: "open"},
		43: c,
	}
}

func assertObservation(t *testing.T, fixture *fixture, change, want string) {
	t.Helper()
	if err := Reconcile(context.Background(), fixture.store, fixture.forge); err != nil {
		t.Fatal(err)
	}
	observation, found, err := fixture.store.ProviderObservation(context.Background(), "W", change)
	if err != nil || !found || observation.State != want {
		t.Fatalf("%s observation = %+v, found=%v, %v; want %s", change, observation, found, err, want)
	}
}

func TestNativeForeignRetargetAfterAdoptionIsDrift(t *testing.T) {
	fixture, heads := stackedLineage(t, true)
	adoptNativeRestack(t, fixture, heads)
	fixture.forge.pulls = lineagePulls(heads, stackpublish.PR{HeadSHA: heads["restacked C"], Base: "main"})
	assertObservation(t, fixture, "C", "diverged")
	assertObservation(t, fixture, "B", "open")
}

func TestNativeForeignPushAfterAdoptionIsDrift(t *testing.T) {
	fixture, heads := stackedLineage(t, true)
	adoptNativeRestack(t, fixture, heads)
	pushed := fixture.commit(t, "foreign edit")
	git(t, fixture.source, "push", "-q", "--force", "origin", "HEAD:refs/heads/loom/ws/W/change/C")
	fixture.forge.pulls = lineagePulls(heads, stackpublish.PR{HeadSHA: pushed, Base: "loom/ws/W/change/B"})
	assertObservation(t, fixture, "C", "diverged")
}

func TestRetargetToTrunkAfterTwoLandedLayersIsExpected(t *testing.T) {
	fixture, heads := stackedLineage(t, false)
	squashB := squashOntoMain(t, fixture, heads["B"])
	fixture.forge.pulls = lineagePulls(heads, stackpublish.PR{HeadSHA: heads["C"], Base: "main"})
	fixture.forge.pulls[42] = stackpublish.PR{Number: 42, Head: "loom/ws/W/change/B", HeadSHA: heads["B"],
		Base: "loom/ws/W/change/A", State: "closed", Merged: true, MergeCommitSHA: squashB}
	assertObservation(t, fixture, "C", "open")
	for _, change := range []string{"A", "B"} {
		if status, err := fixture.store.LandingStatus(context.Background(), "W", change); err != nil || status.State != "landed" {
			t.Fatalf("%s landing = %+v, %v", change, status, err)
		}
	}
}

package publish

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/layout/refname"
	"github.com/tysonthomas9/loomcli/internal/loomgit/review"
	"github.com/tysonthomas9/loomcli/internal/stackpublish"
)

func (f *fakeForge) UpdatePRBase(_ context.Context, _, _ string, number int, base string) error {
	for index := range f.prs {
		if f.prs[index].Number == number {
			f.prs[index].Base = base
			return nil
		}
	}
	return os.ErrNotExist
}

func stackRevision(t *testing.T, fixture fixture, change string, number int, parent string) loomgit.Revision {
	t.Helper()
	git(t, fixture.repo, "reset", "-q", "--hard", parent)
	path := filepath.Join(fixture.repo, change)
	if err := os.WriteFile(path, []byte(change+strconv.Itoa(number)), 0600); err != nil {
		t.Fatal(err)
	}
	git(t, fixture.repo, "add", change)
	git(t, fixture.repo, "commit", "-qm", change+" layer")
	head := git(t, fixture.repo, "rev-parse", "HEAD")
	ctx := context.Background()
	revision, err := fixture.store.ReserveRevision(ctx, loomgit.Revision{Workspace: "W", Change: change,
		RequestID: change + strconv.Itoa(number), Kind: "derived", Operation: "apply", Outcome: "completed",
		BaseSHA: parent, TreeHash: head, SourceHeadSHA: head})
	if err != nil {
		t.Fatal(err)
	}
	revision.HeadSHA = head
	if err := fixture.store.FinishRevision(ctx, revision); err != nil {
		t.Fatal(err)
	}
	requestID := "apply-" + change + strconv.Itoa(number)
	if err := fixture.store.SaveApplied(ctx, loomgit.AppliedLayer{RequestID: requestID, Workspace: "W", Lead: "L", Change: change,
		Revision: revision.Number, OldTip: parent, NewTip: head, Commits: []string{head}}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.AdvanceApplied(ctx, requestID, "prepared", "done"); err != nil {
		t.Fatal(err)
	}
	ref, err := refname.RevisionHead("W", change, strconv.Itoa(revision.Number))
	if err != nil {
		t.Fatal(err)
	}
	git(t, fixture.repo, "update-ref", ref, head)
	if _, err := review.Submit(ctx, fixture.store, "W", change, revision.Number, head, "approve", "", review.Actor{Kind: "human", ID: "reviewer"}); err != nil {
		t.Fatal(err)
	}
	return revision
}

func TestPublishStackPushesDerivedHeadsBeforeCreatingPRs(t *testing.T) {
	fixture := newFixture(t)
	first := stackRevision(t, fixture, "A", 1, fixture.base)
	second := stackRevision(t, fixture, "B", 1, first.HeadSHA)
	forge := &fakeForge{}
	request := fixture.request()
	request.forge = forge
	got, err := publishStack(context.Background(), fixture.store, StackRequest{Request: request, StackID: "feature-1", Changes: []string{"A", "B"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].HeadSHA != first.HeadSHA || got[1].HeadSHA != second.HeadSHA {
		t.Fatalf("published revisions = %+v", got)
	}
	for index, change := range []string{"A", "B"} {
		branch, _ := refname.ChangeBranch("W", change)
		remote := git(t, fixture.repo, "ls-remote", fixture.remote, "refs/heads/"+branch)
		if !strings.HasPrefix(remote, got[index].HeadSHA+"\t") {
			t.Fatalf("%s remote head = %s", change, remote)
		}
	}
	if len(forge.prs) != 2 || forge.prs[0].Base != "develop" || forge.prs[1].Base != forge.prs[0].Head {
		t.Fatalf("PR bases = %+v", forge.prs)
	}
	for index, change := range []string{"A", "B"} {
		if !strings.Contains(forge.prs[index].Body, "Loom-Change-Id: "+change) {
			t.Fatalf("PR %s lacks change ID: %q", change, forge.prs[index].Body)
		}
	}
}

func TestPublishStackStaleLeaseChangesNoBranchOrPRBase(t *testing.T) {
	fixture := newFixture(t)
	first := stackRevision(t, fixture, "A", 1, fixture.base)
	second := stackRevision(t, fixture, "B", 1, first.HeadSHA)
	forge := &fakeForge{}
	request := fixture.request()
	request.forge = forge
	stack := StackRequest{Request: request, StackID: "feature-1", Changes: []string{"A", "B"}}
	if _, err := publishStack(context.Background(), fixture.store, stack); err != nil {
		t.Fatal(err)
	}
	firstNew := stackRevision(t, fixture, "A", 2, fixture.base)
	secondNew := stackRevision(t, fixture, "B", 2, firstNew.HeadSHA)
	branch, _ := refname.ChangeBranch("W", "B")
	git(t, fixture.repo, "push", "--force", fixture.remote, fixture.base+":refs/heads/"+branch)
	_, err := publishStack(context.Background(), fixture.store, stack)
	codeIs(t, err, loomgit.Diverged)
	firstBranch, _ := refname.ChangeBranch("W", "A")
	if got := git(t, fixture.remote, "rev-parse", "refs/heads/"+firstBranch); got != first.HeadSHA {
		t.Fatalf("first branch moved to %s after stale second lease", got)
	}
	if forge.prs[0].Base != "develop" || forge.prs[1].Base != firstBranch {
		t.Fatalf("PR bases changed on stale lease: %+v", forge.prs)
	}
	if secondNew.HeadSHA == second.HeadSHA {
		t.Fatal("test did not produce a new derived head")
	}
}

func TestPublishStackLeasedReplacementOfRestackedHeads(t *testing.T) {
	fixture := newFixture(t)
	first := stackRevision(t, fixture, "A", 1, fixture.base)
	stackRevision(t, fixture, "B", 1, first.HeadSHA)
	forge := &fakeForge{}
	request := fixture.request()
	request.forge = forge
	stack := StackRequest{Request: request, StackID: "feature-1", Changes: []string{"A", "B"}}
	if _, err := publishStack(context.Background(), fixture.store, stack); err != nil {
		t.Fatal(err)
	}
	firstNew := stackRevision(t, fixture, "A", 2, fixture.base)
	secondNew := stackRevision(t, fixture, "B", 2, firstNew.HeadSHA)
	if _, err := publishStack(context.Background(), fixture.store, stack); err != nil {
		t.Fatal(err)
	}
	for _, layer := range []struct{ change, head string }{{"A", firstNew.HeadSHA}, {"B", secondNew.HeadSHA}} {
		branch, _ := refname.ChangeBranch("W", layer.change)
		if got := git(t, fixture.remote, "rev-parse", "refs/heads/"+branch); got != layer.head {
			t.Fatalf("%s = %s, want %s", layer.change, got, layer.head)
		}
	}
	if forge.creates != 2 {
		t.Fatalf("replacement opened %d PRs, want 2", forge.creates)
	}
}

func TestPublishStackRetargetsExistingPRsAndBlocksSinglePublish(t *testing.T) {
	fixture := newFixture(t)
	first := stackRevision(t, fixture, "A", 1, fixture.base)
	stackRevision(t, fixture, "B", 1, first.HeadSHA)
	firstBranch, _ := refname.ChangeBranch("W", "A")
	secondBranch, _ := refname.ChangeBranch("W", "B")
	forge := &fakeForge{prs: []stackpublish.PR{
		{Number: 1, Head: firstBranch, Base: "wrong", State: "open"},
		{Number: 2, Head: secondBranch, Base: "wrong", State: "open"},
	}}
	request := fixture.request()
	request.forge = forge
	if _, err := publishStack(context.Background(), fixture.store, StackRequest{Request: request, StackID: "feature-1", Changes: []string{"A", "B"}}); err != nil {
		t.Fatal(err)
	}
	if forge.prs[0].Base != "develop" || forge.prs[1].Base != firstBranch {
		t.Fatalf("PR bases = %+v", forge.prs)
	}
	request.Change = "A"
	_, err := Publish(context.Background(), fixture.store, request)
	codeIs(t, err, loomgit.ModeMismatch)
	if forge.prs[0].Base != "develop" || forge.prs[1].Base != firstBranch {
		t.Fatalf("single publish changed stacked PR bases: %+v", forge.prs)
	}
}

func TestPublishStackReconcileRetainsDependentBase(t *testing.T) {
	fixture := newFixture(t)
	first := stackRevision(t, fixture, "A", 1, fixture.base)
	stackRevision(t, fixture, "B", 1, first.HeadSHA)
	firstBranch, _ := refname.ChangeBranch("W", "A")
	forge := &fakeForge{prs: []stackpublish.PR{{Number: 1, Head: firstBranch, Base: "develop", State: "open"}}, createError: os.ErrPermission}
	request := fixture.request()
	request.forge = forge
	_, err := publishStack(context.Background(), fixture.store, StackRequest{Request: request, StackID: "feature-1", Changes: []string{"A", "B"}})
	if !errors.Is(err, os.ErrPermission) {
		t.Fatalf("interrupted stack publish = %v", err)
	}
	forge.createError = nil
	if err := Reconcile(context.Background(), fixture.store, forge, "fixture-token"); err != nil {
		t.Fatal(err)
	}
	if len(forge.prs) != 2 || forge.prs[1].Base != firstBranch {
		t.Fatalf("reconciled PRs = %+v", forge.prs)
	}
}

func TestPublishStackUnreviewedLayerPushesNothing(t *testing.T) {
	fixture := newFixture(t)
	fixture.revision(t, 1, fixture.base, "unreviewed", "source")
	request := fixture.request()
	_, err := publishStack(context.Background(), fixture.store, StackRequest{Request: request, StackID: "feature-1", Changes: []string{"C"}})
	codeIs(t, err, loomgit.ReviewRequired)
	if got := git(t, fixture.remote, "for-each-ref", "--format=%(refname)", "refs/heads"); got != "" {
		t.Fatalf("unreviewed layer pushed %s", got)
	}
}

func TestPublishStackRejectsMissingPredecessor(t *testing.T) {
	fixture := newFixture(t)
	first := stackRevision(t, fixture, "A", 1, fixture.base)
	stackRevision(t, fixture, "B", 1, first.HeadSHA)
	request := fixture.request()
	_, err := publishStack(context.Background(), fixture.store, StackRequest{Request: request, StackID: "feature-1", Changes: []string{"B"}})
	codeIs(t, err, loomgit.StackNotLinear)
	if got := git(t, fixture.remote, "for-each-ref", "--format=%(refname)", "refs/heads"); got != "" {
		t.Fatalf("nonlinear stack pushed %s", got)
	}
}

func TestStackLayersRejectsTwoChildrenAndMergeParent(t *testing.T) {
	fixture := newFixture(t)
	first := stackRevision(t, fixture, "A", 1, fixture.base)
	second := stackRevision(t, fixture, "B", 1, fixture.base)
	git(t, fixture.repo, "merge", "--no-ff", "-m", "join", first.HeadSHA)
	merge := git(t, fixture.repo, "rev-parse", "HEAD")
	runner, err := gitexec.New(fixture.repo, gitexec.Options{})
	if err != nil {
		t.Fatal(err)
	}
	request := fixture.request()
	_, _, err = stackLayers(context.Background(), fixture.store, runner, runner,
		StackRequest{Request: request, StackID: "feature-1", Changes: []string{"A", "B"}},
		[]loomgit.AppliedLayer{{Change: "A", OldTip: fixture.base, NewTip: first.HeadSHA}, {Change: "B", OldTip: fixture.base, NewTip: second.HeadSHA}})
	codeIs(t, err, loomgit.StackNotLinear)
	request.BaseSHA = second.HeadSHA
	_, _, err = prepareStackLayer(context.Background(), fixture.store, runner, runner, request,
		loomgit.AppliedLayer{Change: "M", OldTip: second.HeadSHA, NewTip: merge},
		map[string]string{first.HeadSHA: "A", second.HeadSHA: "B"})
	codeIs(t, err, loomgit.StackNotLinear)
	if !strings.Contains(err.Error(), "A") || !strings.Contains(err.Error(), "B") {
		t.Fatalf("merge error omitted parent layers: %v", err)
	}
}

func TestPublishStackRecordedEntryUsesConfiguredWorkingArea(t *testing.T) {
	fixture := newFixture(t)
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	first := stackRevision(t, fixture, "A", 1, fixture.base)
	second := stackRevision(t, fixture, "B", 1, first.HeadSHA)
	ctx := context.Background()
	for _, change := range []string{"A", "B"} {
		if _, err := fixture.store.DriverChange(ctx, "W", change, "repo", change); err != nil {
			t.Fatal(err)
		}
	}
	if err := fixture.store.SaveWorkingAreas(ctx, []journal.WorkingArea{{Workspace: "W", Lead: "L", Repo: "repo", Path: fixture.repo, BaseSHA: fixture.base}}); err != nil {
		t.Fatal(err)
	}
	cfg := &config.LoomConfig{Workspaces: map[string]config.WorkspaceConfig{
		"workspace": {ID: "W", Path: fixture.repo, Repos: []config.RepoConfig{{Name: "repo", Path: fixture.repo}}},
	}}
	forge := &fakeForge{}
	result, err := publishStackRecorded(ctx, fixture.store, cfg, "W", "feature-1", "L", []string{"A", "B"}, forge, "fixture-token", "owner/repo")
	if err != nil || len(result) != 2 || result[0].Revision.HeadSHA != first.HeadSHA || result[1].Revision.HeadSHA != second.HeadSHA {
		t.Fatalf("recorded stack publish = %+v, %v", result, err)
	}
	if selected, err := fixture.store.StackBackend(ctx, "W", "feature-1"); err != nil || selected != "loom" {
		t.Fatalf("recorded backend = %q, %v", selected, err)
	}
}

type nativeCapableForge struct{ *fakeForge }

func (nativeCapableForge) SupportsNativeStacks() bool { return true }

type nativeBackend struct{ LoomStackBackend }

func TestChooseStackBackendUsesForgeCapabilityAndRecordsChoice(t *testing.T) {
	fixture := newFixture(t)
	loom := LoomStackBackend{Store: fixture.store}
	native := nativeBackend{loom}
	selected, err := chooseStackBackend(context.Background(), fixture.store, "W", "native-stack", nativeCapableForge{&fakeForge{}}, loom, native)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := selected.(nativeBackend); !ok {
		t.Fatalf("selected %T, want native backend", selected)
	}
	if recorded, err := fixture.store.StackBackend(context.Background(), "W", "native-stack"); err != nil || recorded != "native" {
		t.Fatalf("native selection = %q, %v", recorded, err)
	}
	github := stackpublish.NewGitHubForge("fixture-token", nil, "")
	selected, err = chooseStackBackend(context.Background(), fixture.store, "W", "github-stack", github, loom, native)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := selected.(LoomStackBackend); !ok {
		t.Fatalf("selected %T, want Loom backend", selected)
	}
	if recorded, err := fixture.store.StackBackend(context.Background(), "W", "github-stack"); err != nil || recorded != "loom" {
		t.Fatalf("GitHub selection = %q, %v", recorded, err)
	}
}

func TestPublicationSchemaAddsStackIdentityToExistingJournal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = database.Exec(`CREATE TABLE change_publications (
		workspace TEXT NOT NULL, change_id TEXT NOT NULL, repo TEXT NOT NULL,
		branch TEXT NOT NULL, trunk TEXT NOT NULL, slug TEXT NOT NULL,
		head_sha TEXT NOT NULL, phase TEXT NOT NULL,
		pr_number INTEGER NOT NULL DEFAULT 0, pr_url TEXT NOT NULL DEFAULT '',
		PRIMARY KEY(workspace, change_id))`)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := journal.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	if err := store.BeginPublication(context.Background(), journal.Publication{Workspace: "W", Change: "A", StackID: "feature-1"}); err != nil {
		t.Fatal(err)
	}
	publication, found, err := store.Publication(context.Background(), "W", "A")
	if err != nil || !found || publication.StackID != "feature-1" {
		t.Fatalf("migrated publication = %+v, %v", publication, err)
	}
}

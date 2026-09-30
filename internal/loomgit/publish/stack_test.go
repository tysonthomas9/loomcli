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
	"github.com/tysonthomas9/loomcli/internal/loomgit/landing"
	"github.com/tysonthomas9/loomcli/internal/loomgit/mirror"
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

func TestStackBackendRejectsTrunkMode(t *testing.T) {
	fixture := newFixture(t)
	ctx := context.Background()
	if err := fixture.store.SetDeliveryMode(ctx, "W", "trunk"); err != nil {
		t.Fatal(err)
	}
	backend := LoomStackBackend{Store: fixture.store}
	_, err := backend.Publish(ctx, StackRequest{Request: fixture.request(), StackID: "feature-1", Changes: []string{"A"}})
	codeIs(t, err, loomgit.ModeMismatch)
}

type limitedForge struct {
	*fakeForge
	limit int
}

func (forge limitedForge) StackLimit() int { return forge.limit }

func TestPublishStackProviderLimitBeforePush(t *testing.T) {
	fixture := newFixture(t)
	first := stackRevision(t, fixture, "A", 1, fixture.base)
	stackRevision(t, fixture, "B", 1, first.HeadSHA)
	forge := limitedForge{fakeForge: &fakeForge{}, limit: 1}
	request := fixture.request()
	request.forge = forge
	_, err := (LoomStackBackend{Store: fixture.store}).Publish(context.Background(), StackRequest{
		Request: request, StackID: "feature", Changes: []string{"A", "B"}})
	codeIs(t, err, loomgit.ProviderStackLimit)
	if forge.creates != 0 || git(t, fixture.repo, "ls-remote", fixture.remote, "refs/heads/*") != "" {
		t.Fatal("provider limit pushed a branch or opened a PR")
	}
}

func TestPublishStackListsAllRevisionsNeedingApproval(t *testing.T) {
	fixture := newFixture(t)
	first := stackRevision(t, fixture, "A", 1, fixture.base)
	second := stackRevision(t, fixture, "B", 1, first.HeadSHA)
	ctx := context.Background()
	for _, revision := range []loomgit.Revision{first, second} {
		if _, err := review.Submit(ctx, fixture.store, "W", revision.Change, revision.Number, revision.HeadSHA,
			"reject", "", review.Actor{Kind: "human", ID: "reviewer"}); err != nil {
			t.Fatal(err)
		}
	}
	request := fixture.request()
	forge := &fakeForge{}
	request.forge = forge
	_, err := publishStack(ctx, fixture.store, StackRequest{Request: request, StackID: "feature", Changes: []string{"A", "B"}})
	codeIs(t, err, loomgit.ReviewRequired)
	if !strings.Contains(err.Error(), "A revision 1") || !strings.Contains(err.Error(), "B revision 1") || forge.creates != 0 {
		t.Fatalf("missing review list: %v; PRs = %d", err, forge.creates)
	}
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

func TestPublishStackUsesEveryWorkingAreaLayerInOrder(t *testing.T) {
	fixture := newFixture(t)
	changes := make([]string, 0, 12)
	parent := fixture.base
	for index := range 12 {
		change := "T" + strconv.Itoa(index+1)
		revision := stackRevision(t, fixture, change, 1, parent)
		changes = append(changes, change)
		parent = revision.HeadSHA
	}
	request := fixture.request()
	forge := &fakeForge{}
	request.forge = forge
	ctx := context.Background()
	for _, subset := range [][]string{changes[:11], append([]string{changes[1], changes[0]}, changes[2:]...)} {
		_, err := publishStack(ctx, fixture.store, StackRequest{Request: request, StackID: "feature", Changes: subset})
		codeIs(t, err, loomgit.StackNotLinear)
	}
	if forge.creates != 0 {
		t.Fatalf("invalid stack created %d PRs", forge.creates)
	}
	got, err := publishStack(ctx, fixture.store, StackRequest{Request: request, StackID: "feature", Changes: changes})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 12 || len(forge.prs) != 12 || got[11].HeadSHA != parent {
		t.Fatalf("published %d revisions and %d PRs", len(got), len(forge.prs))
	}
	for index := 1; index < len(forge.prs); index++ {
		if forge.prs[index].Base != forge.prs[index-1].Head {
			t.Fatalf("PR %d base = %q, want %q", index+1, forge.prs[index].Base, forge.prs[index-1].Head)
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

type deletingPusher struct {
	mirror.RefPusher
	test    *testing.T
	remote  string
	ref     string
	queries int
}

func (pusher *deletingPusher) RemoteSHA(ctx context.Context, remote, ref string) (string, error) {
	if ref == pusher.ref {
		pusher.queries++
		if pusher.queries == 2 {
			git(pusher.test, pusher.remote, "update-ref", "-d", ref)
		}
	}
	return pusher.RefPusher.RemoteSHA(ctx, remote, ref)
}

func TestPublishStackReportsUnchangedLayerDriftAndRetryConverges(t *testing.T) {
	fixture := newFixture(t)
	first := stackRevision(t, fixture, "A", 1, fixture.base)
	second := stackRevision(t, fixture, "B", 1, first.HeadSHA)
	firstBranch, _ := refname.ChangeBranch("W", "A")
	secondBranch, _ := refname.ChangeBranch("W", "B")
	git(t, fixture.repo, "push", fixture.remote, first.HeadSHA+":refs/heads/"+firstBranch)
	runner, err := gitexec.New(fixture.repo, gitexec.Options{FallbackIdentity: gitexec.Identity{Name: "Test", Email: "test@example.test"}})
	if err != nil {
		t.Fatal(err)
	}
	request := fixture.request()
	forge := &fakeForge{prs: []stackpublish.PR{
		{Number: 1, Head: firstBranch, Base: "original-first", State: "open"},
		{Number: 2, Head: secondBranch, Base: "original-second", State: "open"},
	}}
	request.forge = forge
	stack := StackRequest{Request: request, StackID: "feature-1", Changes: []string{"A", "B"},
		pusher: &deletingPusher{RefPusher: mirror.NewPusher(runner), test: t, remote: fixture.remote, ref: "refs/heads/" + firstBranch}}
	_, err = publishStack(context.Background(), fixture.store, stack)
	codeIs(t, err, loomgit.Diverged)
	if !strings.Contains(err.Error(), "A") || !strings.Contains(err.Error(), first.HeadSHA) || !strings.Contains(err.Error(), "<absent>") {
		t.Fatalf("drift error omits layer and SHA: %v", err)
	}
	reopened, err := journal.OpenSQLite(fixture.storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	publication, found, err := reopened.Publication(context.Background(), "W", "A")
	if err != nil || !found || publication.Phase != "drift" || publication.DriftSHA != "" {
		t.Fatalf("durable drift = %+v, %v", publication, err)
	}
	if got := git(t, fixture.remote, "rev-parse", "refs/heads/"+secondBranch); got != second.HeadSHA {
		t.Fatalf("changed layer head = %s", got)
	}
	if len(forge.prs) != 2 || forge.prs[0].Base != "original-first" || forge.prs[1].Base != "original-second" {
		t.Fatalf("PR bases changed before recovery: %+v", forge.prs)
	}
	stack.pusher = nil
	if _, err := publishStack(context.Background(), reopened, stack); err != nil {
		t.Fatal(err)
	}
	publication, found, err = reopened.Publication(context.Background(), "W", "A")
	if err != nil || !found || publication.Phase != "done" || publication.DriftSHA != "" {
		t.Fatalf("recovered publication = %+v, %v", publication, err)
	}
	if got := git(t, fixture.remote, "rev-parse", "refs/heads/"+firstBranch); got != first.HeadSHA {
		t.Fatalf("recovered first layer head = %s", got)
	}
	if len(forge.prs) != 2 || forge.prs[1].Base != firstBranch {
		t.Fatalf("recovered PRs = %+v", forge.prs)
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
	reopened, err := journal.OpenSQLite(fixture.storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	if err := Reconcile(context.Background(), reopened, forge, "fixture-token"); err != nil {
		t.Fatal(err)
	}
	if len(forge.prs) != 2 || forge.prs[1].Base != firstBranch {
		t.Fatalf("reconciled PRs = %+v", forge.prs)
	}
}

func TestPublishStackReconcilePushesAllIntentsAfterRestart(t *testing.T) {
	fixture := newFixture(t)
	first := stackRevision(t, fixture, "A", 1, fixture.base)
	second := stackRevision(t, fixture, "B", 1, first.HeadSHA)
	request := fixture.request()
	request.forge = &fakeForge{}
	ctx := context.Background()
	runner, err := gitexec.New(fixture.repo, gitexec.Options{FallbackIdentity: gitexec.Identity{Name: "Test", Email: "test@example.test"}})
	if err != nil {
		t.Fatal(err)
	}
	layers, _, err := stackLayers(ctx, fixture.store, runner, runner,
		StackRequest{Request: request, StackID: "feature-1", Changes: []string{"A", "B"}},
		[]loomgit.AppliedLayer{{Change: "A", OldTip: fixture.base, NewTip: first.HeadSHA}, {Change: "B", OldTip: first.HeadSHA, NewTip: second.HeadSHA}})
	if err != nil {
		t.Fatal(err)
	}
	publications := []journal.Publication{layers[0].publication, layers[1].publication}
	if err := fixture.store.BeginStackPublications(ctx, publications); err != nil {
		t.Fatal(err)
	}
	reopened, err := journal.OpenSQLite(fixture.storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	forge := &fakeForge{}
	if err := Reconcile(ctx, reopened, forge, "fixture-token"); err != nil {
		t.Fatal(err)
	}
	for _, layer := range layers {
		if got := git(t, fixture.remote, "rev-parse", "refs/heads/"+layer.publication.Branch); got != layer.revision.HeadSHA {
			t.Fatalf("%s remote head = %s", layer.publication.Change, got)
		}
	}
	if len(forge.prs) != 2 || forge.prs[1].Base != forge.prs[0].Head {
		t.Fatalf("recovered PRs = %+v", forge.prs)
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
	runner, err := gitexec.New(fixture.repo, gitexec.Options{FallbackIdentity: gitexec.Identity{Name: "Test", Email: "test@example.test"}})
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
	if result[0].Backend != "loom" || result[0].StatusReason == "" {
		t.Fatalf("fallback status = %+v", result[0])
	}
}

type nativeCapableForge struct{ *fakeForge }

func (nativeCapableForge) SupportsNativeStacks() bool { return true }

type nativeBackend struct{ LoomStackBackend }

type fakeNativeForge struct {
	*fakeForge
	stacks [][]int
}

func (forge *fakeNativeForge) NativeStacksEnabled(context.Context, string, string) (bool, error) {
	return true, nil
}

func (forge *fakeNativeForge) EnsureNativeStack(_ context.Context, _, _ string, numbers []int) error {
	forge.stacks = append(forge.stacks, append([]int(nil), numbers...))
	return nil
}

func TestPublishStackRecordedCreatesNativeStackAfterLeasedPush(t *testing.T) {
	fixture := newFixture(t)
	first := stackRevision(t, fixture, "A", 1, fixture.base)
	stackRevision(t, fixture, "B", 1, first.HeadSHA)
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
	forge := &fakeNativeForge{fakeForge: &fakeForge{}}
	for attempt := 0; attempt < 2; attempt++ {
		results, err := publishStackRecorded(ctx, fixture.store, cfg, "W", "feature-1", "L", []string{"A", "B"}, forge, "fixture-token", "owner/repo")
		if err != nil {
			t.Fatal(err)
		}
		if results[0].Backend != "native" || results[0].StatusReason != "" {
			t.Fatalf("native status = %+v", results[0])
		}
	}
	if len(forge.prs) != 2 || len(forge.stacks) != 2 || len(forge.stacks[0]) != 2 || forge.stacks[0][0] != forge.prs[0].Number || forge.stacks[0][1] != forge.prs[1].Number {
		t.Fatalf("native stack calls = %+v, PRs = %+v", forge.stacks, forge.prs)
	}
	if selected, err := fixture.store.StackBackend(ctx, "W", "feature-1"); err != nil || selected != "native" {
		t.Fatalf("backend = %q, %v", selected, err)
	}
}

func (f *fakeForge) PullByNumber(_ context.Context, _, _ string, number int) (stackpublish.PR, error) {
	for _, pr := range f.prs {
		if pr.Number == number {
			return pr, nil
		}
	}
	return stackpublish.PR{}, os.ErrNotExist
}

func (f *fakeForge) PullsForCommit(context.Context, string, string, string) ([]stackpublish.PR, error) {
	return nil, nil
}

func landingStackFixture(t *testing.T) (fixture, *fakeForge, loomgit.Revision) {
	fixture := newFixture(t)
	ctx := context.Background()
	first := stackRevision(t, fixture, "A", 1, fixture.base)
	second := stackRevision(t, fixture, "B", 1, first.HeadSHA)
	git(t, fixture.repo, "branch", "-m", "loom/ws/W/interactive/L")
	if err := fixture.store.SaveWorkingAreas(ctx, []journal.WorkingArea{{Workspace: "W", Lead: "L", Repo: "repo",
		Path: fixture.repo, Branch: "loom/ws/W/interactive/L", BaseSHA: fixture.base, Mode: "worktree"}}); err != nil {
		t.Fatal(err)
	}
	forge := &fakeForge{}
	request := fixture.request()
	request.forge = forge
	if _, err := publishStack(ctx, fixture.store, StackRequest{Request: request, StackID: "feature-1", Changes: []string{"A", "B"}}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.RecordStackBackend(ctx, "W", "feature-1", "loom"); err != nil {
		t.Fatal(err)
	}
	trunk := filepath.Join(t.TempDir(), "trunk")
	git(t, fixture.repo, "worktree", "add", "-q", "--detach", trunk, fixture.base)
	if err := os.WriteFile(filepath.Join(trunk, "A"), []byte("A1"), 0600); err != nil {
		t.Fatal(err)
	}
	git(t, trunk, "add", "A")
	git(t, trunk, "commit", "-qm", "squash A")
	merged := git(t, trunk, "rev-parse", "HEAD")
	git(t, trunk, "push", "-q", "origin", "HEAD:refs/heads/develop")
	if err := fixture.store.MarkLanded(ctx, "W", "A", "merge_commit"); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.OfferRestack(ctx, journal.RestackOffer{Workspace: "W", Change: "B", Predecessor: "A",
		Repo: "repo", Revision: second.Number, TrunkSHA: merged}); err != nil {
		t.Fatal(err)
	}
	configDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(configDir, "loomgit"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(filepath.Dir(fixture.repo), "store.db"), filepath.Join(configDir, "loomgit", "store.db")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LOOM_CONFIG_DIR", configDir)
	t.Setenv("GITHUB_TOKEN", "fixture-token")
	if err := os.WriteFile(filepath.Join(fixture.repo, "unsaved.txt"), []byte("keep me"), 0600); err != nil {
		t.Fatal(err)
	}
	return fixture, forge, second
}

func TestLandingReconcileRestacksPublishedStack(t *testing.T) {
	fixture, forge, second := landingStackFixture(t)
	ctx := context.Background()
	if err := landing.ReconcileWithOptions(ctx, fixture.store, forge, landing.Options{Restack: RestackOffer}); err != nil {
		t.Fatal(err)
	}
	restacked, err := fixture.store.SourceRevision(ctx, "W", "B")
	if err != nil || restacked <= second.Number {
		t.Fatalf("restacked revision = %d, %v", restacked, err)
	}
	derived, err := fixture.store.GetRevision(ctx, "W", "B", restacked)
	if err != nil || derived.Operation != "restack" || derived.BaseSHA != git(t, fixture.remote, "rev-parse", "refs/heads/develop") {
		t.Fatalf("derived revision = %+v, %v", derived, err)
	}
	publication, found, err := fixture.store.Publication(ctx, "W", "B")
	if err != nil || !found || publication.Trunk != "develop" || publication.Head == second.HeadSHA {
		t.Fatalf("restacked publication = %+v, %v", publication, err)
	}
	if got := git(t, fixture.remote, "rev-parse", "refs/heads/"+publication.Branch); got != publication.Head {
		t.Fatalf("remote head = %s, want %s", got, publication.Head)
	}
	if forge.prs[1].Base != "develop" {
		t.Fatalf("remaining PR base = %s", forge.prs[1].Base)
	}
	if contents, err := os.ReadFile(filepath.Join(fixture.repo, "unsaved.txt")); err != nil || string(contents) != "keep me" {
		t.Fatalf("uncommitted file = %q, %v", contents, err)
	}
}

func TestChooseStackBackendUsesForgeCapabilityAndRecordsChoice(t *testing.T) {
	fixture := newFixture(t)
	loom := LoomStackBackend{Store: fixture.store}
	native := nativeBackend{loom}
	selected, err := chooseStackBackend(context.Background(), fixture.store, "W", "native-stack", "owner/repo", nativeCapableForge{&fakeForge{}}, loom, native)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := selected.(nativeBackend); !ok {
		t.Fatalf("selected %T, want native backend", selected)
	}
	if recorded, err := fixture.store.StackBackend(context.Background(), "W", "native-stack"); err != nil || recorded != "native" {
		t.Fatalf("native selection = %q, %v", recorded, err)
	}
	selected, err = chooseStackBackend(context.Background(), fixture.store, "W", "github-stack", "owner/repo", &fakeForge{}, loom, native)
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

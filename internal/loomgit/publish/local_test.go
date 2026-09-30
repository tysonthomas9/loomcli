package publish

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/layout/refname"
	"github.com/tysonthomas9/loomcli/internal/loomgit/landing"
	"github.com/tysonthomas9/loomcli/internal/loomgit/review"
	"github.com/tysonthomas9/loomcli/internal/stackpublish"
	storepkg "github.com/tysonthomas9/loomcli/internal/store"
)

func TestPublishStackRecordedIncludesLeadOwnedLayer(t *testing.T) {
	fixture := newFixture(t)
	ctx := context.Background()
	if _, err := fixture.store.DriverChange(ctx, "W", "T", "repo", "C"); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.SaveWorkingAreas(ctx, []journal.WorkingArea{{Workspace: "W", Lead: "L", Repo: "repo", Path: fixture.repo, BaseSHA: fixture.base}}); err != nil {
		t.Fatal(err)
	}
	task := stackRevision(t, fixture, "C", 1, fixture.base)
	if err := os.WriteFile(filepath.Join(fixture.repo, "own"), []byte("lead"), 0600); err != nil {
		t.Fatal(err)
	}
	git(t, fixture.repo, "add", "own")
	git(t, fixture.repo, "commit", "-qm", "lead work")
	leadHead := git(t, fixture.repo, "rev-parse", "HEAD")
	cfg := &config.LoomConfig{Workspaces: map[string]config.WorkspaceConfig{
		"workspace": {ID: "W", Path: fixture.repo, Repos: []config.RepoConfig{{Name: "repo", Path: fixture.repo}}},
	}}
	forge := &fakeForge{}
	results, err := publishStackRecorded(ctx, fixture.store, cfg, "W", "feature", "L", []string{"C"}, forge, "fixture-token", "owner/repo")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || results[0].Revision.HeadSHA != task.HeadSHA || results[1].Revision.HeadSHA != leadHead {
		t.Fatalf("published layers = %+v", results)
	}
	if len(forge.prs) != 2 || forge.prs[1].Base != forge.prs[0].Head {
		t.Fatalf("PR chain = %+v", forge.prs)
	}
}

func TestPublishStackRecordedRejectsTrunkMode(t *testing.T) {
	fixture := newFixture(t)
	ctx := context.Background()
	if err := fixture.store.SetDeliveryMode(ctx, "W", "trunk"); err != nil {
		t.Fatal(err)
	}
	forge := &fakeForge{}
	_, err := publishStackRecorded(ctx, fixture.store, nil, "W", "feature", "L", []string{"C"}, forge, "fixture-token", "owner/repo")
	var coded *loomgit.Error
	if !errors.As(err, &coded) || coded.Kind != loomgit.ModeMismatch || forge.creates != 0 {
		t.Fatalf("trunk stack publish = %v; PRs = %d", err, forge.creates)
	}
}

func TestPublishStackRecordedRequiresHumanVerdictForLeadLayerWhenPolicyOff(t *testing.T) {
	fixture := newFixture(t)
	ctx := context.Background()
	if _, err := fixture.store.DriverChange(ctx, "W", "T", "repo", "C"); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.SaveWorkingAreas(ctx, []journal.WorkingArea{{Workspace: "W", Lead: "L", Repo: "repo", Path: fixture.repo, BaseSHA: fixture.base}}); err != nil {
		t.Fatal(err)
	}
	stackRevision(t, fixture, "C", 1, fixture.base)
	if err := os.WriteFile(filepath.Join(fixture.repo, "own"), []byte("lead"), 0600); err != nil {
		t.Fatal(err)
	}
	git(t, fixture.repo, "add", "own")
	git(t, fixture.repo, "commit", "-qm", "lead work")
	if err := fixture.store.SetLeadMayApprovePublish(ctx, "W", false); err != nil {
		t.Fatal(err)
	}
	cfg := &config.LoomConfig{Workspaces: map[string]config.WorkspaceConfig{
		"workspace": {ID: "W", Path: fixture.repo, Repos: []config.RepoConfig{{Name: "repo", Path: fixture.repo}}},
	}}
	forge := &fakeForge{}
	_, err := publishStackRecorded(ctx, fixture.store, cfg, "W", "feature", "L", []string{"C"}, forge, "fixture-token", "owner/repo")
	var coded *loomgit.Error
	if !errors.As(err, &coded) || coded.Kind != loomgit.ReviewRequired || forge.creates != 0 {
		t.Fatalf("policy-off publish = %v; PRs = %d", err, forge.creates)
	}
}

func TestPublishStackRecordedOwnOnlyWorkingArea(t *testing.T) {
	fixture := newFixture(t)
	ctx := context.Background()
	if err := fixture.store.SaveWorkingAreas(ctx, []journal.WorkingArea{{Workspace: "W", Lead: "L", Repo: "repo", Path: fixture.repo, BaseSHA: fixture.base}}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture.repo, "own"), []byte("lead"), 0600); err != nil {
		t.Fatal(err)
	}
	git(t, fixture.repo, "add", "own")
	git(t, fixture.repo, "commit", "-qm", "lead work")
	head := git(t, fixture.repo, "rev-parse", "HEAD")
	cfg := &config.LoomConfig{Workspaces: map[string]config.WorkspaceConfig{
		"workspace": {ID: "W", Path: fixture.repo, Repos: []config.RepoConfig{{Name: "repo", Path: fixture.repo}}},
	}}
	forge := &fakeForge{}
	results, err := publishStackRecorded(ctx, fixture.store, cfg, "W", "feature", "L", nil, forge, "fixture-token", "owner/repo")
	if err != nil || len(results) != 1 || results[0].Revision.HeadSHA != head || len(forge.prs) != 1 {
		t.Fatalf("own-only publish = %+v, PRs = %+v, error = %v", results, forge.prs, err)
	}
}

func TestPublishRecordedRequiresVerdictBeforePush(t *testing.T) {
	fixture := newFixture(t)
	revision := fixture.revision(t, 1, fixture.base, "change", "source")
	ctx := context.Background()
	if _, err := fixture.store.DriverChange(ctx, "W", "T", "repo", "C"); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.SaveWorkingAreas(ctx, []journal.WorkingArea{{
		Workspace: "W", Lead: "L", Repo: "repo", Path: fixture.repo, BaseSHA: fixture.base,
	}}); err != nil {
		t.Fatal(err)
	}
	cfg := &config.LoomConfig{Workspaces: map[string]config.WorkspaceConfig{
		"workspace": {ID: "W", Path: fixture.repo, Repos: []config.RepoConfig{{Name: "repo", Path: fixture.repo}}},
	}}
	forge := &fakeForge{}
	request := func() (Result, error) {
		return publishRecorded(ctx, fixture.store, cfg, "W", "L", "C", forge, "fixture-token", "owner/repo", nil)
	}
	_, err := request()
	var coded *loomgit.Error
	if !errors.As(err, &coded) || coded.Kind != loomgit.ReviewRequired {
		t.Fatalf("unapproved publish error = %v", err)
	}
	if forge.creates != 0 {
		t.Fatal("unapproved change opened a PR")
	}
	fixture.approve(t, revision)
	result, err := request()
	if err != nil || result.Revision.HeadSHA != revision.HeadSHA || result.PRURL == "" || result.AlreadyExists {
		t.Fatalf("approved publish = %+v, %v", result, err)
	}
	result, err = request()
	if err != nil || !result.AlreadyExists || forge.creates != 1 {
		t.Fatalf("repeated publish = %+v, %v; creates=%d", result, err, forge.creates)
	}
}

func TestPublishRecordedTrunkReplaysIndependentLayer(t *testing.T) {
	fixture := newFixture(t)
	ctx := context.Background()
	git(t, fixture.repo, "push", "origin", fixture.base+":refs/heads/develop")
	git(t, fixture.repo, "commit", "--allow-empty", "-qm", "independent A")
	predecessor := git(t, fixture.repo, "rev-parse", "HEAD")
	first, err := fixture.store.ReserveRevision(ctx, loomgit.Revision{Workspace: "W", Change: "A", RequestID: "A-source",
		Kind: "source", Operation: "capture", Outcome: "completed", BaseSHA: fixture.base, TreeHash: predecessor, SourceHeadSHA: predecessor})
	if err != nil {
		t.Fatal(err)
	}
	first.HeadSHA = predecessor
	if err := fixture.store.FinishRevision(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.SaveApplied(ctx, loomgit.AppliedLayer{RequestID: "apply-A", Workspace: "W", Lead: "L",
		Change: "A", Revision: first.Number, OldTip: fixture.base, NewTip: predecessor, Commits: []string{predecessor}}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.AdvanceApplied(ctx, "apply-A", "prepared", "done"); err != nil {
		t.Fatal(err)
	}
	firstRef, err := refname.RevisionHead("W", "A", strconv.Itoa(first.Number))
	if err != nil {
		t.Fatal(err)
	}
	git(t, fixture.repo, "update-ref", firstRef, predecessor)
	if _, err := review.Submit(ctx, fixture.store, "W", "A", first.Number, predecessor,
		"approve", "", review.Actor{Kind: "human", ID: "reviewer"}); err != nil {
		t.Fatal(err)
	}
	revision := fixture.revision(t, 1, predecessor, "independent C", "source")
	fixture.approve(t, revision)
	if _, err := fixture.store.DriverChange(ctx, "W", "T", "repo", "C"); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.DriverChange(ctx, "W", "T-A", "repo", "A"); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.SetDeliveryMode(ctx, "W", "trunk"); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.SaveWorkingAreas(ctx, []journal.WorkingArea{{
		Workspace: "W", Lead: "L", Repo: "repo", Path: fixture.repo, BaseSHA: fixture.base,
	}}); err != nil {
		t.Fatal(err)
	}
	cfg := &config.LoomConfig{Workspaces: map[string]config.WorkspaceConfig{
		"workspace": {ID: "W", Path: fixture.repo, Repos: []config.RepoConfig{{Name: "repo", Path: fixture.repo}}},
	}}
	forge := &fakeForge{}
	if _, err := publishRecorded(ctx, fixture.store, cfg, "W", "L", "A", forge, "fixture-token", "owner/repo", nil); err != nil {
		t.Fatal(err)
	}
	result, err := publishRecorded(ctx, fixture.store, cfg, "W", "L", "C", forge, "fixture-token", "owner/repo",
		func(_ context.Context, task string) (string, error) {
			if task != "T" {
				t.Fatalf("flag lookup task = %q", task)
			}
			return "new_checkout", nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if len(forge.prs) != 2 || forge.prs[0].Base != "develop" || forge.prs[1].Base != "develop" ||
		forge.prs[0].Head == forge.prs[1].Head ||
		!strings.Contains(forge.prs[1].Body, "Ships behind feature flag: new_checkout") || result.Revision.BaseSHA != fixture.base ||
		result.Revision.HeadSHA == revision.HeadSHA || result.Revision.Kind != "derived" {
		t.Fatalf("trunk publication = %+v, PRs = %+v", result, forge.prs)
	}
	if parent := git(t, fixture.repo, "rev-parse", result.Revision.HeadSHA+"^1"); parent != fixture.base {
		t.Fatalf("trunk PR parent = %s, want %s", parent, fixture.base)
	}
	if head := git(t, fixture.repo, "rev-parse", "HEAD"); head != revision.HeadSHA {
		t.Fatalf("working area moved to %s", head)
	}
}

func TestPublishRecordedTrunkHoldsDependentUntilLanding(t *testing.T) {
	fixture := newFixture(t)
	ctx := context.Background()
	git(t, fixture.repo, "push", "origin", fixture.base+":refs/heads/develop")
	revision := fixture.revision(t, 1, fixture.base, "dependent", "source")
	fixture.approve(t, revision)
	if _, err := fixture.store.DriverChange(ctx, "W", "T", "repo", "C"); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.RecordLocalLineage(ctx, journal.LocalLineage{
		Workspace: "W", Task: "T", Repo: "repo", PredecessorChange: "A", PredecessorRevision: 1, BaseSHA: fixture.base,
	}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.SetDeliveryMode(ctx, "W", "trunk"); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.SaveWorkingAreas(ctx, []journal.WorkingArea{{
		Workspace: "W", Lead: "L", Repo: "repo", Path: fixture.repo, BaseSHA: fixture.base,
	}}); err != nil {
		t.Fatal(err)
	}
	cfg := &config.LoomConfig{Workspaces: map[string]config.WorkspaceConfig{
		"workspace": {ID: "W", Path: fixture.repo, Repos: []config.RepoConfig{{Name: "repo", Path: fixture.repo}}},
	}}
	forge := &fakeForge{}
	_, err := publishRecorded(ctx, fixture.store, cfg, "W", "L", "C", forge, "fixture-token", "owner/repo",
		func(context.Context, string) (string, error) {
			t.Fatal("flag lookup ran for held change")
			return "", nil
		})
	if err == nil || !strings.Contains(err.Error(), "waiting_on_dependency") || forge.creates != 0 {
		t.Fatalf("dependent publish = %v, creates = %d", err, forge.creates)
	}
	hold, err := fixture.store.DeliveryHold(ctx, "W", "C")
	if err != nil || hold != "waiting_on_dependency" {
		t.Fatalf("delivery hold = %q, %v", hold, err)
	}
	if err := fixture.store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := journal.OpenSQLite(fixture.storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	hold, err = reopened.DeliveryHold(ctx, "W", "C")
	if err != nil || hold != "waiting_on_dependency" {
		t.Fatalf("reopened dependency hold = %q, %v", hold, err)
	}
}

func TestPublishRecordedTrunkVerdictHoldSurvivesReopen(t *testing.T) {
	fixture := newFixture(t)
	ctx := context.Background()
	git(t, fixture.repo, "push", "origin", fixture.base+":refs/heads/develop")
	fixture.revision(t, 1, fixture.base, "unreviewed", "source")
	if _, err := fixture.store.DriverChange(ctx, "W", "T", "repo", "C"); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.SetDeliveryMode(ctx, "W", "trunk"); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.SaveWorkingAreas(ctx, []journal.WorkingArea{{
		Workspace: "W", Lead: "L", Repo: "repo", Path: fixture.repo, BaseSHA: fixture.base,
	}}); err != nil {
		t.Fatal(err)
	}
	cfg := &config.LoomConfig{Workspaces: map[string]config.WorkspaceConfig{
		"workspace": {ID: "W", Path: fixture.repo, Repos: []config.RepoConfig{{Name: "repo", Path: fixture.repo}}},
	}}
	forge := &fakeForge{}
	_, err := publishRecorded(ctx, fixture.store, cfg, "W", "L", "C", forge, "fixture-token", "owner/repo", nil)
	if !errors.Is(err, loomgit.NewError(loomgit.ReviewRequired, "", nil)) || forge.creates != 0 {
		t.Fatalf("unreviewed publish = %v, creates=%d", err, forge.creates)
	}
	if err := fixture.store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := journal.OpenSQLite(fixture.storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	hold, err := reopened.DeliveryHold(ctx, "W", "C")
	if err != nil || hold != "waiting_on_verdict" {
		t.Fatalf("reopened verdict hold = %q, %v", hold, err)
	}
}

func TestPublishRecordedTrunkChangedPatchWaitsForVerdict(t *testing.T) {
	fixture := newFixture(t)
	ctx := context.Background()
	file := filepath.Join(fixture.repo, "file")
	baseText := "a\nb\nc\nd\ne\nf\ng\nh\n"
	if err := os.WriteFile(file, []byte(baseText), 0600); err != nil {
		t.Fatal(err)
	}
	git(t, fixture.repo, "add", "file")
	git(t, fixture.repo, "commit", "-qm", "expanded base")
	base := git(t, fixture.repo, "rev-parse", "HEAD")
	trunk := filepath.Join(t.TempDir(), "trunk")
	git(t, fixture.repo, "worktree", "add", "--detach", trunk, base)
	if err := os.WriteFile(filepath.Join(trunk, "file"), []byte(strings.Replace(baseText, "b\n", "B\n", 1)), 0600); err != nil {
		t.Fatal(err)
	}
	git(t, trunk, "add", "file")
	git(t, trunk, "commit", "-qm", "trunk context")
	git(t, trunk, "push", "origin", "HEAD:refs/heads/develop")
	if err := os.WriteFile(file, []byte(strings.Replace(baseText, "d\n", "D\n", 1)), 0600); err != nil {
		t.Fatal(err)
	}
	git(t, fixture.repo, "add", "file")
	git(t, fixture.repo, "commit", "-qm", "source change")
	head := git(t, fixture.repo, "rev-parse", "HEAD")
	revision, err := fixture.store.ReserveRevision(ctx, loomgit.Revision{Workspace: "W", Change: "C", RequestID: "C-source",
		Kind: "source", Operation: "capture", Outcome: "completed", BaseSHA: base,
		TreeHash: git(t, fixture.repo, "rev-parse", head+"^{tree}"), SourceHeadSHA: head})
	if err != nil {
		t.Fatal(err)
	}
	revision.HeadSHA = head
	if err := fixture.store.FinishRevision(ctx, revision); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.SaveApplied(ctx, loomgit.AppliedLayer{RequestID: "apply-C", Workspace: "W", Lead: "L",
		Change: "C", Revision: revision.Number, OldTip: base, NewTip: head, Commits: []string{head}}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.AdvanceApplied(ctx, "apply-C", "prepared", "done"); err != nil {
		t.Fatal(err)
	}
	ref, err := refname.RevisionHead("W", "C", strconv.Itoa(revision.Number))
	if err != nil {
		t.Fatal(err)
	}
	git(t, fixture.repo, "update-ref", ref, head)
	fixture.approve(t, revision)
	if _, err := fixture.store.DriverChange(ctx, "W", "T", "repo", "C"); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.SetDeliveryMode(ctx, "W", "trunk"); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.SaveWorkingAreas(ctx, []journal.WorkingArea{{
		Workspace: "W", Lead: "L", Repo: "repo", Path: fixture.repo, BaseSHA: base,
	}}); err != nil {
		t.Fatal(err)
	}
	cfg := &config.LoomConfig{Workspaces: map[string]config.WorkspaceConfig{
		"workspace": {ID: "W", Path: fixture.repo, Repos: []config.RepoConfig{{Name: "repo", Path: fixture.repo}}},
	}}
	forge := &fakeForge{}
	_, err = publishRecorded(ctx, fixture.store, cfg, "W", "L", "C", forge, "fixture-token", "owner/repo", nil)
	if !errors.Is(err, loomgit.NewError(loomgit.ReviewRequired, "", nil)) || forge.creates != 0 {
		t.Fatalf("changed patch publish = %v, creates=%d", err, forge.creates)
	}
	if hold, err := fixture.store.DeliveryHold(ctx, "W", "C"); err != nil || hold != "waiting_on_verdict" {
		t.Fatalf("changed patch hold = %q, %v", hold, err)
	}
}

func TestFeatureFlagLabel(t *testing.T) {
	flag, err := featureFlagFromLabels([]string{"backend", "feature-flag:new_checkout"})
	if err != nil || flag != "new_checkout" {
		t.Fatalf("feature flag = %q, %v", flag, err)
	}
	if _, err := featureFlagFromLabels([]string{"feature-flag:new_checkout", "feature-flag:other"}); err == nil {
		t.Fatal("multiple feature flags accepted")
	}
}

func TestPublishLocalEntryReplaysTrunkLayer(t *testing.T) {
	fixture := newFixture(t)
	ctx := context.Background()
	configureLocalWorkspace(t, fixture)
	git(t, fixture.repo, "push", "origin", fixture.base+":refs/heads/develop")
	git(t, fixture.repo, "commit", "--allow-empty", "-qm", "independent layer")
	predecessor := git(t, fixture.repo, "rev-parse", "HEAD")
	revision := fixture.revision(t, 1, predecessor, "trunk change", "source")
	fixture.approve(t, revision)
	if _, err := fixture.store.DriverChange(ctx, "W", "T", "repo", "C"); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.SaveWorkingAreas(ctx, []journal.WorkingArea{{
		Workspace: "W", Lead: "L", Repo: "repo", Path: fixture.repo, BaseSHA: fixture.base,
	}}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.SetDeliveryMode(ctx, "W", "trunk"); err != nil {
		t.Fatal(err)
	}
	forge := &fakeForge{}
	useLocalForge(t, forge)
	result, err := PublishLocal(ctx, "W", "L", "C")
	if err != nil || result.Revision.Kind != "derived" || len(forge.prs) != 1 ||
		forge.prs[0].Base != "develop" || !strings.Contains(forge.prs[0].Body, "new_checkout") {
		t.Fatalf("PublishLocal = %+v, %v; PRs=%+v", result, err, forge.prs)
	}
	if current := git(t, fixture.repo, "rev-parse", "HEAD"); current != revision.HeadSHA {
		t.Fatalf("working area changed to %s", current)
	}
}

type landedForge struct {
	pull stackpublish.PR
}

func (forge landedForge) PullByNumber(context.Context, string, string, int) (stackpublish.PR, error) {
	return forge.pull, nil
}

func (landedForge) PullsForCommit(context.Context, string, string, string) ([]stackpublish.PR, error) {
	return nil, nil
}

func TestLandingReconcilePublishesTrunkDependent(t *testing.T) {
	fixture := newFixture(t)
	ctx := context.Background()
	configureLocalWorkspace(t, fixture)
	git(t, fixture.repo, "push", "origin", fixture.base+":refs/heads/develop")
	git(t, fixture.repo, "commit", "--allow-empty", "-qm", "predecessor")
	predecessor := git(t, fixture.repo, "rev-parse", "HEAD")
	revision := fixture.revision(t, 1, predecessor, "dependent", "source")
	fixture.approve(t, revision)
	git(t, fixture.repo, "checkout", "-q", "-B", "landed", predecessor)
	git(t, fixture.repo, "commit", "--allow-empty", "-qm", "landed predecessor")
	merged := git(t, fixture.repo, "rev-parse", "HEAD")
	git(t, fixture.repo, "push", "origin", merged+":refs/heads/develop")
	git(t, fixture.repo, "checkout", "-q", "-B", "work", revision.HeadSHA)
	if _, err := fixture.store.DriverChange(ctx, "W", "T", "repo", "C"); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.RecordLocalLineage(ctx, journal.LocalLineage{
		Workspace: "W", Task: "T", Repo: "repo", PredecessorChange: "A", PredecessorRevision: 1, BaseSHA: fixture.base,
	}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.SaveWorkingAreas(ctx, []journal.WorkingArea{{
		Workspace: "W", Lead: "L", Repo: "repo", Path: fixture.repo, BaseSHA: fixture.base,
	}}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.SetDeliveryMode(ctx, "W", "trunk"); err != nil {
		t.Fatal(err)
	}
	publication := journal.Publication{Workspace: "W", Change: "A", Repo: fixture.repo,
		Branch: "loom/ws/W/change/A", Trunk: "develop", Slug: "owner/repo", Head: predecessor}
	if err := fixture.store.BeginPublication(ctx, publication); err != nil {
		t.Fatal(err)
	}
	publication.Phase, publication.PRNumber = "done", 42
	if err := fixture.store.AdvancePublication(ctx, publication); err != nil {
		t.Fatal(err)
	}
	forge := &fakeForge{}
	useLocalForge(t, forge)
	err := landing.ReconcileWithOptions(ctx, fixture.store, landedForge{pull: stackpublish.PR{
		Number: 42, Head: publication.Branch, Merged: true, MergeCommitSHA: merged,
	}}, landing.Options{
		Dependents: func(context.Context, string, string) ([]landing.Dependent, error) {
			return []landing.Dependent{{Task: "T", Repo: "repo"}}, nil
		},
		Restack: func(ctx context.Context, offer journal.RestackOffer) (int, error) {
			result, err := PublishLocal(ctx, offer.Workspace, "L", offer.Change)
			return result.Revision.Number, err
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	dependent, ok, err := fixture.store.Publication(ctx, "W", "C")
	if err != nil || !ok || dependent.Phase != "done" || len(forge.prs) != 1 || forge.prs[0].Base != "develop" {
		t.Fatalf("dependent publication = %+v, %v; PRs=%+v", dependent, err, forge.prs)
	}
	offers, err := fixture.store.RestackOffers(ctx, "W", "C")
	if err != nil || len(offers) != 1 || offers[0].DerivedRevision == 0 {
		t.Fatalf("completed restack offers = %+v, %v", offers, err)
	}
}

func configureLocalWorkspace(t *testing.T, fixture fixture) {
	t.Helper()
	ctx := context.Background()
	configDir := filepath.Dir(filepath.Dir(fixture.storePath))
	t.Setenv("LOOM_CONFIG_DIR", configDir)
	handle, err := bootstrap.OpenStore(ctx, configDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := handle.Store.Workspaces().Create(ctx, storepkg.WorkspaceCreate{Key: "W", Name: "W"}); err != nil {
		t.Fatal(err)
	}
	if _, err := handle.Store.Repos().Create(ctx, storepkg.RepoCreate{
		WorkspaceKey: "W", Name: "repo", DefaultBranch: "develop",
	}); err != nil {
		t.Fatal(err)
	}
	if err := handle.Close(); err != nil {
		t.Fatal(err)
	}
	if err := bootstrap.MutateStateCache(func(state *bootstrap.StateCache) error {
		if state.Workspaces == nil {
			state.Workspaces = make(map[string]bootstrap.WorkspaceLocalState)
		}
		state.Workspaces["W"] = bootstrap.WorkspaceLocalState{Path: fixture.repo,
			Repos: map[string]string{"repo": fixture.repo}}
		state.LastWorkspace = "W"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func useLocalForge(t *testing.T, forge *fakeForge) {
	t.Helper()
	previousProvider, previousFlag := localPublishProvider, localFlagForTask
	localPublishProvider = func() (Forge, string, string) { return forge, "fixture-token", "owner/repo" }
	localFlagForTask = func(_ context.Context, task string) (string, error) {
		if task != "T" {
			t.Fatalf("task lookup = %q", task)
		}
		return "new_checkout", nil
	}
	t.Cleanup(func() { localPublishProvider, localFlagForTask = previousProvider, previousFlag })
}

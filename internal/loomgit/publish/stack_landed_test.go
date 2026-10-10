package publish

import (
	"context"
	"slices"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/layout/refname"
)

func landedStackConfig(fixture fixture) *config.LoomConfig {
	return &config.LoomConfig{Workspaces: map[string]config.WorkspaceConfig{
		"workspace": {ID: "W", Path: fixture.repo, Repos: []config.RepoConfig{{Name: "repo", Path: fixture.repo}}},
	}}
}

// recordStackChanges records the changes' repository and, given a base, the
// lead's working area.
func recordStackChanges(t *testing.T, fixture fixture, base string, changes ...string) {
	t.Helper()
	ctx := context.Background()
	for _, change := range changes {
		if _, err := fixture.store.DriverChange(ctx, "W", change, "repo", change); err != nil {
			t.Fatal(err)
		}
	}
	if base == "" {
		return
	}
	if err := fixture.store.SaveWorkingAreas(ctx, []journal.WorkingArea{{Workspace: "W", Lead: "L", Repo: "repo", Path: fixture.repo, BaseSHA: base}}); err != nil {
		t.Fatal(err)
	}
}

// publishLeadStack publishes the lead's stack the way PublishLeadChangeLocal
// does: the requested changes come from the lead's recorded applied log.
func publishLeadStack(t *testing.T, fixture fixture, forge Forge) ([]Result, error) {
	t.Helper()
	ctx := context.Background()
	requested, err := taskStackChanges(ctx, fixture.store, "W", "L", "repo")
	if err != nil {
		return nil, err
	}
	return publishStackRecorded(ctx, fixture.store, landedStackConfig(fixture), "W", "feature-1", "L", requested, forge, "fixture-token", "owner/repo")
}

func mergeForgePR(forge *fakeForge, change string) {
	branch, _ := refname.ChangeBranch("W", change)
	for index := range forge.prs {
		if forge.prs[index].Head == branch {
			forge.prs[index].State, forge.prs[index].Merged = "closed", true
		}
	}
}

// S3: A's PR merged before B was approved, so no landing restack ran and the
// working area still starts with A. Publishing B must not reopen A or stack B on it.
func TestPublishLeadStackSkipsMergedBottomLayer(t *testing.T) {
	fixture := newFixture(t)
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	ctx := context.Background()
	first := stackRevision(t, fixture, "A", 1, fixture.base)
	recordStackChanges(t, fixture, fixture.base, "A")
	forge := &fakeForge{}
	if _, err := publishLeadStack(t, fixture, forge); err != nil {
		t.Fatal(err)
	}
	mergeForgePR(forge, "A")
	if err := fixture.store.MarkMerged(ctx, "W", "A"); err != nil {
		t.Fatal(err)
	}
	second := stackRevision(t, fixture, "B", 1, first.HeadSHA)
	recordStackChanges(t, fixture, "", "B")
	results, err := publishLeadStack(t, fixture, forge)
	if err != nil {
		t.Fatal(err)
	}
	branchB, _ := refname.ChangeBranch("W", "B")
	if len(results) != 1 || results[0].Revision.HeadSHA != second.HeadSHA {
		t.Fatalf("published = %+v", results)
	}
	if forge.creates != 2 || forge.prs[1].Head != branchB || forge.prs[1].Base != "develop" {
		t.Fatalf("PRs after A merged = %+v (creates %d)", forge.prs, forge.creates)
	}
}

// Walk: A and B landed and a landing restack moved the working-area base to
// B; C merged later without a restack. E's publish must include E only.
func TestPublishLeadStackAfterLandingRestackPublishesOnlyOpenLayers(t *testing.T) {
	fixture := newFixture(t)
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	ctx := context.Background()
	a := stackRevision(t, fixture, "A", 1, fixture.base)
	b := stackRevision(t, fixture, "B", 1, a.HeadSHA)
	c := stackRevision(t, fixture, "C", 1, b.HeadSHA)
	e := stackRevision(t, fixture, "E", 1, c.HeadSHA)
	recordStackChanges(t, fixture, b.HeadSHA, "A", "B", "C", "E")
	for _, change := range []string{"A", "B"} {
		if err := fixture.store.MarkLanded(ctx, "W", change, "merge_commit"); err != nil {
			t.Fatal(err)
		}
	}
	if err := fixture.store.MarkMerged(ctx, "W", "C"); err != nil {
		t.Fatal(err)
	}
	requested, err := taskStackChanges(ctx, fixture.store, "W", "L", "repo")
	if err != nil || !slices.Equal(requested, []string{"E"}) {
		t.Fatalf("requested = %v, %v", requested, err)
	}
	forge := &fakeForge{}
	results, err := publishLeadStack(t, fixture, forge)
	if err != nil {
		t.Fatal(err)
	}
	branchE, _ := refname.ChangeBranch("W", "E")
	if len(results) != 1 || results[0].Revision.HeadSHA != e.HeadSHA || len(forge.prs) != 1 ||
		forge.prs[0].Head != branchE || forge.prs[0].Base != "develop" {
		t.Fatalf("published = %+v, PRs = %+v", results, forge.prs)
	}
}

// Before landing reconcile records A's merge, a retry must not open a second
// PR for A's already-merged head.
func TestPublishStackRefusesDuplicatePRForMergedHead(t *testing.T) {
	fixture := newFixture(t)
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	first := stackRevision(t, fixture, "A", 1, fixture.base)
	recordStackChanges(t, fixture, fixture.base, "A")
	forge := &fakeForge{}
	if _, err := publishLeadStack(t, fixture, forge); err != nil {
		t.Fatal(err)
	}
	mergeForgePR(forge, "A")
	forge.prs[0].HeadSHA = first.HeadSHA
	stackRevision(t, fixture, "B", 1, first.HeadSHA)
	recordStackChanges(t, fixture, "", "B")
	_, err := publishLeadStack(t, fixture, forge)
	codeIs(t, err, loomgit.Stale)
	if forge.creates != 1 {
		t.Fatalf("merged head reopened: %+v", forge.prs)
	}
}

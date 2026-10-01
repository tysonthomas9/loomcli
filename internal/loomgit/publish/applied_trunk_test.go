package publish

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/apply"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/pool"
)

// appliedInPlace freezes a task revision in the lead area itself, then applies it there.
func appliedInPlace(t *testing.T) (fixture, loomgit.Revision, *config.LoomConfig) {
	t.Helper()
	useExplicitGitIdentity(t)
	t.Setenv("GIT_AUTHOR_NAME", "Test User")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@example.test")
	t.Setenv("GIT_COMMITTER_NAME", "Env Committer")
	t.Setenv("GIT_COMMITTER_EMAIL", "env@example.test")
	fixture, ctx := newFixture(t), context.Background()
	git(t, fixture.repo, "push", "origin", fixture.base+":refs/heads/develop")
	git(t, fixture.repo, "checkout", "-q", "-b", "loom/ws/W/interactive/L")
	revision := fixture.revision(t, 1, fixture.base, "task change", "source")
	fixture.approve(t, revision)
	git(t, fixture.repo, "config", "user.name", "Test User")
	git(t, fixture.repo, "config", "user.email", "test@example.test")
	repo, err := pool.New(fixture.store, gitexec.Options{}).Admit(ctx, fixture.repo)
	if err != nil {
		t.Fatal(err)
	}
	runner, err := gitexec.New(fixture.repo, gitexec.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := apply.New(fixture.store, repo, runner).Apply(ctx, apply.Request{
		Workspace: "W", Lead: "L", Change: "C", RequestID: "approve-apply", Revision: revision.Number}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.DriverChange(ctx, "W", "T", "repo", "C"); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.SaveWorkingAreas(ctx, []journal.WorkingArea{{
		Workspace: "W", Lead: "L", Repo: "repo", Path: fixture.repo, BaseSHA: fixture.base}}); err != nil {
		t.Fatal(err)
	}
	cfg := &config.LoomConfig{Workspaces: map[string]config.WorkspaceConfig{
		"workspace": {ID: "W", Path: fixture.repo, Repos: []config.RepoConfig{{Name: "repo", Path: fixture.repo}}},
	}}
	return fixture, revision, cfg
}

// advanceTrunk pushes a foreign commit to develop and returns to the lead area.
func advanceTrunk(t *testing.T, fixture fixture, from, name, body string) string {
	t.Helper()
	git(t, fixture.repo, "checkout", "-q", "--detach", from)
	if err := os.WriteFile(filepath.Join(fixture.repo, name), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	git(t, fixture.repo, "add", name)
	git(t, fixture.repo, "-c", "user.name=Foreign", "-c", "user.email=foreign@example.test", "commit", "-qm", "foreign trunk")
	trunk := git(t, fixture.repo, "rev-parse", "HEAD")
	git(t, fixture.repo, "push", "-q", "origin", trunk+":refs/heads/develop")
	git(t, fixture.repo, "checkout", "-q", "loom/ws/W/interactive/L")
	return trunk
}

func TestPublishRecordedTrunkAfterInPlaceApplyCarriesTaskChange(t *testing.T) {
	fixture, revision, cfg := appliedInPlace(t)
	ctx := context.Background()
	trunk := advanceTrunk(t, fixture, fixture.base, "foreign", "foreign\n")
	if err := fixture.store.SetDeliveryMode(ctx, "W", "trunk"); err != nil {
		t.Fatal(err)
	}
	forge := &fakeForge{}
	result, err := publishRecorded(ctx, fixture.store, cfg, "W", "L", "C", forge, "fixture-token", "owner/repo", nil)
	if err != nil {
		t.Fatal(err)
	}
	head := result.Revision.HeadSHA
	if len(forge.prs) != 1 || head == trunk || git(t, fixture.repo, "rev-parse", head+"^1") != trunk ||
		git(t, fixture.repo, "show", head+":file") != "task change" || git(t, fixture.repo, "rev-parse", "HEAD") != revision.HeadSHA {
		t.Fatalf("trunk PR head %s lacks the task change on trunk %s; PRs=%+v", head, trunk, forge.prs)
	}
	if identity := git(t, fixture.repo, "show", "-s", "--format=%an <%ae>|%cn <%ce>", head); identity != "Test User <test@example.test>|Test User <test@example.test>" {
		t.Fatalf("published identity = %q", identity)
	}
}

func TestPublishRecordedTrunkRefusesEmptyReplay(t *testing.T) {
	fixture, _, cfg := appliedInPlace(t)
	ctx := context.Background()
	advanceTrunk(t, fixture, fixture.base, "file", "task change\n")
	if err := fixture.store.SetDeliveryMode(ctx, "W", "trunk"); err != nil {
		t.Fatal(err)
	}
	forge := &fakeForge{}
	_, err := publishRecorded(ctx, fixture.store, cfg, "W", "L", "C", forge, "fixture-token", "owner/repo", nil)
	codeIs(t, err, loomgit.StaleSubject)
	if len(forge.prs) != 0 {
		t.Fatalf("empty replay opened PRs: %+v", forge.prs)
	}
}

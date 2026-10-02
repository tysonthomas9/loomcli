package landing

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/stackpublish"
)

type dependencyPost struct {
	slug, sha string
	status    stackpublish.DependencyStatus
}

// twoRepoForge serves PRs from two repositories and records loom/dependencies posts.
type twoRepoForge struct {
	pulls       map[string]stackpublish.PR
	associated  map[string][]stackpublish.PR
	queued      map[string]string
	enforcement map[string]string
	posts       []dependencyPost
	failPosts   int
}

func (forge *twoRepoForge) PullByNumber(_ context.Context, owner, repo string, number int) (stackpublish.PR, error) {
	return forge.pulls[fmt.Sprintf("%s/%s#%d", owner, repo, number)], nil
}

func (forge *twoRepoForge) PullsForCommit(_ context.Context, _, _, sha string) ([]stackpublish.PR, error) {
	return forge.associated[sha], nil
}

func (forge *twoRepoForge) PostDependencyStatus(_ context.Context, owner, repo, sha string, status stackpublish.DependencyStatus) error {
	if forge.failPosts > 0 {
		forge.failPosts--
		return errors.New("github POST statuses: 403: API rate limit exceeded")
	}
	forge.posts = append(forge.posts, dependencyPost{slug: owner + "/" + repo, sha: sha, status: status})
	return nil
}

func (forge *twoRepoForge) MergeQueueHead(_ context.Context, owner, repo string, number int) (string, error) {
	return forge.queued[fmt.Sprintf("%s/%s#%d", owner, repo, number)], nil
}

func (forge *twoRepoForge) DependencyEnforcement(_ context.Context, owner, repo, _ string) (string, error) {
	if state := forge.enforcement[owner+"/"+repo]; state != "" {
		return state, nil
	}
	return "not_enforced", nil
}

func (forge *twoRepoForge) last(t *testing.T, sha string) stackpublish.DependencyStatus {
	t.Helper()
	for index := len(forge.posts) - 1; index >= 0; index-- {
		if forge.posts[index].sha == sha {
			return forge.posts[index].status
		}
	}
	t.Fatalf("no loom/dependencies post on %s: %+v", sha, forge.posts)
	return stackpublish.DependencyStatus{}
}

type crossRepoFixture struct {
	store          *journal.SQLite
	forge          *twoRepoForge
	repo1, repo2   string
	head1, head2   string
	predecessors   Predecessors
	predecessorErr error
}

// newCrossRepoFixture publishes C1 (task T1, owner/repo1 PR 1) and C2 (task T2,
// owner/repo2 PR 2); T2 depends on T1.
func newCrossRepoFixture(t *testing.T) *crossRepoFixture {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	store, err := journal.OpenSQLite(filepath.Join(root, "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	fixture := &crossRepoFixture{store: store, forge: &twoRepoForge{pulls: map[string]stackpublish.PR{},
		associated: map[string][]stackpublish.PR{}, queued: map[string]string{}, enforcement: map[string]string{}}}
	fixture.predecessors = func(_ context.Context, workspace, task string) ([]string, error) {
		if workspace != "W" {
			t.Fatalf("predecessor lookup workspace = %q", workspace)
		}
		if task == "T2" {
			return []string{"T1"}, fixture.predecessorErr
		}
		return nil, fixture.predecessorErr
	}
	for index, name := range []string{"repo1", "repo2"} {
		source := filepath.Join(root, name)
		if err := os.Mkdir(source, 0o700); err != nil {
			t.Fatal(err)
		}
		git(t, source, "init", "-q", "-b", "main")
		git(t, source, "config", "user.name", "Test")
		git(t, source, "config", "user.email", "test@example.com")
		if err := os.WriteFile(filepath.Join(source, "readme"), []byte(name+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		git(t, source, "add", "readme")
		git(t, source, "commit", "-qm", "initial")
		git(t, root, "clone", "-q", "--bare", source, filepath.Join(root, name+".git"))
		git(t, source, "remote", "add", "origin", filepath.Join(root, name+".git"))
		head := git(t, source, "rev-parse", "HEAD")
		change, task, number := fmt.Sprintf("C%d", index+1), fmt.Sprintf("T%d", index+1), index+1
		branch := "loom/ws/W/change/" + change
		publication := journal.Publication{Workspace: "W", Change: change, Repo: source, Branch: branch,
			Trunk: "main", Slug: "owner/" + name, Head: head}
		if err := store.BeginPublication(ctx, publication); err != nil {
			t.Fatal(err)
		}
		publication.Phase, publication.PRNumber = "done", number
		publication.PRURL = fmt.Sprintf("https://github.com/owner/%s/pull/%d", name, number)
		if err := store.AdvancePublication(ctx, publication); err != nil {
			t.Fatal(err)
		}
		if _, err := store.DriverChange(ctx, "W", task, name, change); err != nil {
			t.Fatal(err)
		}
		fixture.forge.pulls[fmt.Sprintf("owner/%s#%d", name, number)] = stackpublish.PR{Number: number, Head: branch,
			HeadSHA: head, Base: "main", State: "open"}
		if index == 0 {
			fixture.repo1, fixture.head1 = source, head
		} else {
			fixture.repo2, fixture.head2 = source, head
		}
	}
	return fixture
}

func (fixture *crossRepoFixture) reconcile(t *testing.T) error {
	t.Helper()
	return ReconcileWithOptions(context.Background(), fixture.store, fixture.forge, Options{Predecessors: fixture.predecessors})
}

// land merges PR1 on repo1's trunk the way the provider records it.
func (fixture *crossRepoFixture) land(t *testing.T) {
	t.Helper()
	merged := commitFile(t, fixture.repo1, "PR1 merged")
	git(t, fixture.repo1, "push", "-q", "origin", "main")
	pull := fixture.forge.pulls["owner/repo1#1"]
	pull.Merged, pull.State, pull.MergeCommitSHA = true, "closed", merged
	fixture.forge.pulls["owner/repo1#1"] = pull
}

func commitFile(t *testing.T, repo, message string) string {
	t.Helper()
	path := filepath.Join(repo, "readme")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, []byte(message+"\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", "readme")
	git(t, repo, "commit", "-qm", message)
	return git(t, repo, "rev-parse", "HEAD")
}

func TestDependencyPendingNamesPredecessorPR(t *testing.T) {
	fixture := newCrossRepoFixture(t)
	if err := fixture.reconcile(t); err != nil {
		t.Fatal(err)
	}
	status := fixture.forge.last(t, fixture.head2)
	if status.State != "pending" || !strings.Contains(status.Description, "owner/repo1#1") {
		t.Fatalf("PR2 loom/dependencies = %+v", status)
	}
	for _, post := range fixture.forge.posts {
		if post.slug != "owner/repo2" {
			t.Fatalf("posted on a PR with no cross-repo predecessor: %+v", post)
		}
	}
}

func TestDependencyTurnsSuccessInOnePassWhenPredecessorLands(t *testing.T) {
	fixture := newCrossRepoFixture(t)
	if err := fixture.reconcile(t); err != nil {
		t.Fatal(err)
	}
	fixture.land(t)
	if err := fixture.reconcile(t); err != nil {
		t.Fatal(err)
	}
	if status := fixture.forge.last(t, fixture.head2); status.State != "success" {
		t.Fatalf("PR2 after PR1 landed = %+v", status)
	}
	posts := len(fixture.forge.posts)
	if err := fixture.reconcile(t); err != nil || len(fixture.forge.posts) != posts {
		t.Fatalf("unchanged result reposted: %v, %+v", err, fixture.forge.posts)
	}
}

func TestDependencyIgnoresTrailerNotTiedToOwnedPR(t *testing.T) {
	fixture := newCrossRepoFixture(t)
	foreign := commitFile(t, fixture.repo1, "Copied work\n\nLoom-Change-Id: C1")
	git(t, fixture.repo1, "push", "-q", "origin", "main")
	fixture.forge.associated[foreign] = []stackpublish.PR{{Number: 99, Merged: true}}
	pull := fixture.forge.pulls["owner/repo1#1"]
	pull.Merged, pull.State = true, "closed"
	fixture.forge.pulls["owner/repo1#1"] = pull
	if err := fixture.reconcile(t); err != nil {
		t.Fatal(err)
	}
	if status := fixture.forge.last(t, fixture.head2); status.State != "pending" {
		t.Fatalf("PR2 after a foreign trailer commit = %+v", status)
	}
}

func TestDependencyMergeQueue(t *testing.T) {
	fixture := newCrossRepoFixture(t)
	fixture.forge.queued["owner/repo1#1"] = "1111111111111111111111111111111111111111"
	fixture.forge.queued["owner/repo2#2"] = "2222222222222222222222222222222222222222"
	if err := fixture.reconcile(t); err != nil {
		t.Fatal(err)
	}
	head, group := fixture.forge.last(t, fixture.head2), fixture.forge.last(t, "2222222222222222222222222222222222222222")
	if head.State != "pending" || group != head {
		t.Fatalf("queued PR1 head = %+v, PR2 merge group = %+v", head, group)
	}
	fixture.land(t)
	if err := fixture.reconcile(t); err != nil {
		t.Fatal(err)
	}
	if group := fixture.forge.last(t, "2222222222222222222222222222222222222222"); group.State != "success" {
		t.Fatalf("PR2 merge group after PR1 landed = %+v", group)
	}
}

func TestDependencyClosedOrEjectedPredecessorStaysPending(t *testing.T) {
	fixture := newCrossRepoFixture(t)
	pull := fixture.forge.pulls["owner/repo1#1"]
	pull.State = "closed"
	fixture.forge.pulls["owner/repo1#1"] = pull
	if err := fixture.reconcile(t); err != nil {
		t.Fatal(err)
	}
	status := fixture.forge.last(t, fixture.head2)
	if status.State != "pending" || !strings.Contains(status.Description, "owner/repo1#1 (closed unmerged: dependency_abandoned)") {
		t.Fatalf("PR2 with PR1 closed = %+v", status)
	}
	checks, _, err := fixture.store.DependencyChecks(context.Background())
	if err != nil || len(checks) != 1 || checks[0].Change != "C2" || checks[0].Reason != status.Description {
		t.Fatalf("status reason = %+v, %v", checks, err)
	}
}

func TestDependencyPostFailureRetriesAndNeverShowsFalseSuccess(t *testing.T) {
	fixture := newCrossRepoFixture(t)
	fixture.forge.failPosts = 1
	if err := fixture.reconcile(t); err == nil || len(fixture.forge.posts) != 0 {
		t.Fatalf("failed post = %v, %+v", err, fixture.forge.posts)
	}
	if err := fixture.reconcile(t); err != nil {
		t.Fatal(err)
	}
	if status := fixture.forge.last(t, fixture.head2); status.State != "pending" {
		t.Fatalf("retried post = %+v", status)
	}
	fixture.predecessorErr = errors.New("issue backend unavailable")
	fixture.land(t)
	if err := fixture.reconcile(t); err == nil {
		t.Fatal("dependency lookup failure was not reported")
	}
	if status := fixture.forge.last(t, fixture.head2); status.State != "pending" {
		t.Fatalf("success posted without a fresh evaluation: %+v", status)
	}
	fixture.predecessorErr = nil
	fixture.forge.failPosts = 1
	if err := fixture.reconcile(t); err == nil {
		t.Fatal("failed success post was not reported")
	}
	if err := fixture.reconcile(t); err != nil {
		t.Fatal(err)
	}
	if status := fixture.forge.last(t, fixture.head2); status.State != "success" {
		t.Fatalf("retried success post = %+v", status)
	}
}

func TestDependencyEnforcementRecorded(t *testing.T) {
	fixture := newCrossRepoFixture(t)
	fixture.forge.enforcement["owner/repo2"] = "not_pinned"
	if err := fixture.reconcile(t); err != nil {
		t.Fatal(err)
	}
	_, enforcement, err := fixture.store.DependencyChecks(context.Background())
	if err != nil || len(enforcement) != 1 || enforcement[0].Repo != "owner/repo2" || enforcement[0].Branch != "main" ||
		enforcement[0].State != "not_pinned" || !strings.Contains(enforcement[0].Reason, "any collaborator") {
		t.Fatalf("enforcement = %+v, %v", enforcement, err)
	}
}

func TestDependencyRemovedReleasesPendingPR(t *testing.T) {
	fixture := newCrossRepoFixture(t)
	if err := fixture.reconcile(t); err != nil {
		t.Fatal(err)
	}
	fixture.predecessors = func(context.Context, string, string) ([]string, error) { return nil, nil }
	if err := fixture.reconcile(t); err != nil {
		t.Fatal(err)
	}
	if status := fixture.forge.last(t, fixture.head2); status.State != "success" {
		t.Fatalf("PR2 after its dependency was removed = %+v", status)
	}
}

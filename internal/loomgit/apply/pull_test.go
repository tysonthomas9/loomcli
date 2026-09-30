package apply

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/review"
)

func preparePull(t *testing.T, fixture *fixture) string {
	t.Helper()
	ctx := context.Background()
	entry, _, err := fixture.store.Begin(ctx, "workspace-create:W", "ensure_workspace")
	if err != nil {
		t.Fatal(err)
	}
	entry, err = fixture.store.Advance(ctx, entry, "checkouts_added", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	entry, err = fixture.store.Advance(ctx, entry, "rows_written", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.CommitWorkspace(ctx, entry, []loomgit.WorkspaceRepo{{Workspace: "W", Repo: "repo", Trunk: "main", BaseSHA: fixture.base}}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.SaveWorkingAreas(ctx, []journal.WorkingArea{{Workspace: "W", Lead: "L", Repo: "repo",
		Path: fixture.dir, Branch: "loom/ws/W/interactive/L", BaseSHA: fixture.base, Mode: "worktree"}}); err != nil {
		t.Fatal(err)
	}
	trunk := filepath.Join(t.TempDir(), "trunk")
	fixture.git(t, "worktree", "add", "-q", trunk, "main")
	fixture.git(t, "remote", "add", "origin", fixture.dir)
	return trunk
}

func advanceTrunk(t *testing.T, fixture *fixture, trunk, name string) string {
	t.Helper()
	trunkGit(t, trunk, "commit", "--allow-empty", "-qm", name)
	return fixture.git(t, "rev-parse", "main")
}

func trunkGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=Test", "-c", "user.email=test@example.com"}, args...)...) //nolint:norawexec // Temporary real-Git fixture, no network.
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("trunk git %v: %s: %v", args, out, err)
	}
	return strings.TrimSpace(string(out))
}

func TestPullRestacksAndCarriesVerdictWithoutPush(t *testing.T) {
	fixture := newFixture(t)
	trunk := preparePull(t, fixture)
	if _, err := fixture.apply(t); err != nil {
		t.Fatal(err)
	}
	second := filepath.Join(t.TempDir(), "second")
	fixture.git(t, "worktree", "add", "-q", "-b", "source2", second, fixture.source)
	if err := os.WriteFile(filepath.Join(second, "second"), []byte("second\n"), 0600); err != nil {
		t.Fatal(err)
	}
	trunkGit(t, second, "add", "second")
	trunkGit(t, second, "commit", "-qm", "second")
	secondHead := fixture.git(t, "rev-parse", "source2")
	ctx := context.Background()
	revision, err := fixture.store.ReserveRevision(ctx, loomgit.Revision{Workspace: "W", Change: "C2",
		RequestID: "source2", Kind: "source", Operation: "snapshot", Outcome: "completed",
		BaseSHA: fixture.source, TreeHash: fixture.git(t, "rev-parse", secondHead+"^{tree}"), SourceHeadSHA: secondHead})
	if err != nil {
		t.Fatal(err)
	}
	revision.HeadSHA = secondHead
	if err := fixture.store.FinishRevision(ctx, revision); err != nil {
		t.Fatal(err)
	}
	if _, err := review.Submit(ctx, fixture.store, "W", "C2", revision.Number, secondHead, "approve", "", review.Actor{Kind: "human", ID: "reviewer"}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.Apply(ctx, Request{Workspace: "W", Lead: "L", Change: "C2", Revision: revision.Number, RequestID: "apply-2"}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"trunk-1", "trunk-2", "trunk-3"} {
		advanceTrunk(t, fixture, trunk, name)
	}
	trunkHead := fixture.git(t, "rev-parse", "main")
	fixture.write(t, "unrelated", "dirty\n")
	result, err := fixture.service.Pull(context.Background(), PullRequest{Workspace: "W", Lead: "L", Repo: "repo", RequestID: "pull-1"})
	if err != nil {
		t.Fatal(err)
	}
	if result.HeadSHA != fixture.git(t, "rev-parse", "HEAD") || fixture.git(t, "rev-parse", "HEAD~2") != trunkHead {
		t.Fatalf("unexpected restacked head: %+v", result)
	}
	if got := fixture.git(t, "rev-parse", "main"); got != trunkHead {
		t.Fatalf("trunk was pushed: %s", got)
	}
	if got := fixture.git(t, "status", "--porcelain"); !strings.Contains(got, "unrelated") {
		t.Fatalf("uncommitted edit lost: %s", got)
	}
	log, err := fixture.service.AppliedLog(context.Background(), "W", "L")
	if err != nil || len(log) != 2 || log[0].Change != "C1" || log[1].Change != "C2" {
		t.Fatalf("restacked log = %+v, %v", log, err)
	}
	for _, layer := range log {
		derived, err := fixture.store.GetRevision(ctx, "W", layer.Change, layer.Revision)
		if err != nil || derived.Operation != "pull" {
			t.Fatalf("derived = %+v, %v", derived, err)
		}
		verdict, err := fixture.store.LatestVerdict(ctx, derived)
		if err != nil || verdict.Kind != "carried" {
			t.Fatalf("verdict = %+v, %v", verdict, err)
		}
	}
}

func TestPullLandedLayerIsDropped(t *testing.T) {
	fixture := newFixture(t)
	trunk := preparePull(t, fixture)
	if _, err := fixture.apply(t); err != nil {
		t.Fatal(err)
	}
	trunkHead := advanceTrunk(t, fixture, trunk, "trunk")
	if err := fixture.store.MarkLanded(context.Background(), "W", "C1"); err != nil {
		t.Fatal(err)
	}
	result, err := fixture.service.Pull(context.Background(), PullRequest{Workspace: "W", Lead: "L", Repo: "repo", RequestID: "pull-landed"})
	if err != nil || result.HeadSHA != trunkHead || fixture.git(t, "rev-parse", "HEAD") != trunkHead {
		t.Fatalf("landed pull = %+v, %v", result, err)
	}
}

func TestPullConflictLeavesCheckoutAndRevisionRefs(t *testing.T) {
	fixture := newFixture(t)
	trunk := preparePull(t, fixture)
	if _, err := fixture.apply(t); err != nil {
		t.Fatal(err)
	}
	fixture.commit(t, "owned", "lead\n", "lead-owned")
	if err := os.WriteFile(filepath.Join(trunk, "owned"), []byte("other\n"), 0600); err != nil {
		t.Fatal(err)
	}
	trunkGit(t, trunk, "add", "owned")
	trunkGit(t, trunk, "commit", "-qm", "trunk")
	before := fixture.git(t, "rev-parse", "HEAD")
	refs := fixture.git(t, "for-each-ref", "--format=%(refname)", "refs/loom")
	result, err := fixture.service.Pull(context.Background(), PullRequest{Workspace: "W", Lead: "L", Repo: "repo", RequestID: "pull-conflict"})
	var pullError *loomgit.Error
	if !errors.As(err, &pullError) || pullError.Code() != string(loomgit.Conflict) || len(result.Paths) == 0 {
		t.Fatalf("expected conflict paths, got %+v, %v", result, err)
	}
	if fixture.git(t, "rev-parse", "HEAD") != before {
		t.Fatal("conflict moved branch")
	}
	if fixture.git(t, "for-each-ref", "--format=%(refname)", "refs/loom") != refs {
		t.Fatal("conflict changed revision refs")
	}
}

func TestPullOverlapHoldsSwap(t *testing.T) {
	fixture := newFixture(t)
	trunk := preparePull(t, fixture)
	if _, err := fixture.apply(t); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(trunk, "base"), []byte("upstream\n"), 0600); err != nil {
		t.Fatal(err)
	}
	trunkGit(t, trunk, "add", "base")
	trunkGit(t, trunk, "commit", "-qm", "trunk")
	fixture.write(t, "base", "dirty\n")
	before := fixture.git(t, "rev-parse", "HEAD")
	result, err := fixture.service.Pull(context.Background(), PullRequest{Workspace: "W", Lead: "L", Repo: "repo", RequestID: "pull-held"})
	var pullError *loomgit.Error
	if !errors.As(err, &pullError) || pullError.Code() != string(loomgit.SwapHeld) || len(result.Paths) != 1 || result.Paths[0] != "base" {
		t.Fatalf("expected swap held, got %+v, %v", result, err)
	}
	if fixture.git(t, "rev-parse", "HEAD") != before {
		t.Fatal("held swap moved branch")
	}
}

func TestPullReconcileCompletesLayerLogAfterSwap(t *testing.T) {
	fixture := newFixture(t)
	trunk := preparePull(t, fixture)
	if err := fixture.store.SaveWorkingAreas(context.Background(), []journal.WorkingArea{{Workspace: "W", Lead: "L", Repo: "other",
		Path: filepath.Join(t.TempDir(), "other"), Branch: "loom/ws/W/interactive/L", BaseSHA: fixture.base, Mode: "clone"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.apply(t); err != nil {
		t.Fatal(err)
	}
	advanceTrunk(t, fixture, trunk, "trunk")
	fixture.service.beforeCompletePull = func() error { return errors.New("interrupted after swap") }
	_, err := fixture.service.Pull(context.Background(), PullRequest{Workspace: "W", Lead: "L", Repo: "repo", RequestID: "pull-recover"})
	if err == nil {
		t.Fatal("expected injected interruption")
	}
	targets, err := fixture.store.OpenAppliedTargets(context.Background())
	if err != nil || len(targets) != 1 || targets[0].Repo != "repo" {
		t.Fatalf("pull recovery target = %+v, %v", targets, err)
	}
	fixture.service.beforeCompletePull = nil
	if err := fixture.service.Reconcile(context.Background(), "W", "L"); err != nil {
		t.Fatal(err)
	}
	log, err := fixture.service.AppliedLog(context.Background(), "W", "L")
	if err != nil || len(log) != 1 || log[0].Change != "C1" {
		t.Fatalf("recovered layer log = %+v, %v", log, err)
	}
}

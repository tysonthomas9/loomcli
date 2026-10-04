package driverfreeze_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit/driverfreeze"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
)

// D29 / P1.26: only an attempt that froze code waits for review. An empty
// attempt closes ("No changes"), an attempt with no revision closes, and a
// new attempt on a change whose PR is open updates that PR (P2.19b).
func TestAttemptAwaitsReviewOnlyForCodeAwaitingReview(t *testing.T) {
	journalPath := filepath.Join(t.TempDir(), "loomgit", "store.db")
	repo := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...) //nolint:norawexec // Real temporary Git repository validates revision objects.
		cmd.Dir = repo
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	git("config", "user.name", "Test")
	git("config", "user.email", "test@example.test")
	if err := os.WriteFile(filepath.Join(repo, "a"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", "a")
	git("commit", "-qm", "base")
	base := git("rev-parse", "HEAD")
	ctx := context.Background()
	awaits := func(attempt string) bool {
		t.Helper()
		got, err := driverfreeze.AttemptAwaitsReviewAt(ctx, journalPath, "W", attempt)
		if err != nil {
			t.Fatalf("AttemptAwaitsReviewAt(%s): %v", attempt, err)
		}
		return got
	}
	if awaits("before-any-journal") {
		t.Fatal("no journal yet: a task with no Loom Git copy must close")
	}
	if _, err := driverfreeze.FreezeCaptureAt(ctx, journalPath, driverfreeze.CaptureRequest{Workspace: "W", Task: "T1",
		Repo: "repo", Attempt: "empty", Worktree: repo, Base: base, CaptureSHA: base, Outcome: "completed",
		Complete: true, SkipRetention: true}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "a"), []byte("changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	patch := git("diff", "--binary", base)
	git("restore", "--worktree", ".")
	changed, err := driverfreeze.FreezeAt(ctx, journalPath, driverfreeze.Request{Workspace: "W", Task: "T2",
		Repo: "repo", Attempt: "changed", Worktree: repo, Base: base, Patch: []byte(patch + "\n"), Outcome: "completed"})
	if err != nil {
		t.Fatal(err)
	}
	if awaits("empty") {
		t.Fatal("an empty attempt (No changes) must close its task")
	}
	if awaits("unknown") {
		t.Fatal("an attempt with no revision must close its task")
	}
	if !awaits("changed") {
		t.Fatal("an attempt with code must keep its task open for review")
	}
	if got, err := driverfreeze.AttemptAwaitsReviewAt(ctx, journalPath, "OTHER", "changed"); err != nil || got {
		t.Fatalf("another workspace's attempt = %v %v, want false", got, err)
	}
	store, err := journal.OpenSQLite(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	publication := journal.Publication{Workspace: "W", Change: changed.Change, Repo: "repo", Branch: "loom/t2",
		Trunk: "main", Head: changed.HeadSHA, Phase: "done", PRNumber: 7}
	if err := store.BeginPublication(ctx, publication); err != nil {
		t.Fatal(err)
	}
	if !awaits("changed") {
		t.Fatal("a publication still starting has no open PR: the code still awaits review")
	}
	if err := store.AdvancePublication(ctx, publication); err != nil {
		t.Fatal(err)
	}
	if awaits("changed") {
		t.Fatal("a fix-up of a change whose PR is open updates the PR without review (P2.19b)")
	}
}

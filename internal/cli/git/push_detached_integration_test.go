package git

import (
	"io"
	"log/slog"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
)

// TestPushBranchInRepo_DetachedFallback_Engages is the regression test for
// loomcli-r3ddn.10. It sets up a real bare origin + main worktree + sibling
// feature worktree, drives pushBranchInRepo through cli.DefaultDeps(), and
// verifies that the detached-HEAD fallback engages end-to-end and origin/main
// receives the feature commit.
//
// Before the fix, defaultRunGitWithOutput returned a bare *exec.ExitError,
// isWorktreeConflictErr never matched, and the call returned a cryptic
// "checking out main: exit status 128". After the fix, GitExecError embeds
// stderr in Error(), the gate matches, and the detached path runs.
func TestPushBranchInRepo_DetachedFallback_Engages(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	clitest.ClearGitEnvVars(t)

	tmp := t.TempDir()
	origin := filepath.Join(tmp, "origin")
	repo := filepath.Join(tmp, "repo")
	wtFeature := filepath.Join(tmp, "wt-feature")

	mustGit(t, "", "init", "--bare", "-b", "main", origin)
	mustGit(t, tmp, "clone", origin, repo)
	writeFile(t, filepath.Join(repo, "README.md"), "hello\n")
	mustGit(t, repo, "add", ".")
	mustGit(t, repo, "commit", "-m", "seed main")
	mustGit(t, repo, "push", "origin", "main")

	// Sibling worktree with a feature branch and a divergent commit on a
	// distinct file (so the merge into main doesn't conflict).
	mustGit(t, repo, "worktree", "add", "-b", "feature", wtFeature)
	writeFile(t, filepath.Join(wtFeature, "feature.txt"), "feature work\n")
	mustGit(t, wtFeature, "add", ".")
	mustGit(t, wtFeature, "commit", "-m", "feature work")

	// Remember the feature commit so we can verify origin/main contains it.
	featureSHA := strings.TrimSpace(mustGitOut(t, wtFeature, "rev-parse", "HEAD"))

	deps := cli.DefaultDeps()
	deps.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	deps.Agent = &noopAgent{}

	if err := pushBranchInRepo(deps, wtFeature, "feature", "main", ""); err != nil {
		t.Fatalf("pushBranchInRepo returned err; expected detached fallback to succeed: %v", err)
	}

	// Verify origin/main now contains the feature commit. We check via
	// `git log --format=%H origin/main` from a fresh clone to avoid the
	// possibility that any of the test worktrees' refs are stale.
	verifyClone := filepath.Join(tmp, "verify")
	mustGit(t, "", "clone", origin, verifyClone)
	log := mustGitOut(t, verifyClone, "log", "--format=%H")
	if !strings.Contains(log, featureSHA) {
		t.Fatalf("origin/main does not contain feature commit %s after push.\nlog:\n%s", featureSHA, log)
	}
}

// TestPushBranchInRepo_DetachedFallback_StderrInError verifies that
// defaultRunGitWithOutput's stderr capture works against a real git failure
// caused by a worktree conflict — the precise failure mode the fix targets.
func TestPushBranchInRepo_DetachedFallback_StderrInError(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	clitest.ClearGitEnvVars(t)

	tmp := t.TempDir()
	origin := filepath.Join(tmp, "origin")
	repo := filepath.Join(tmp, "repo")
	wtFeature := filepath.Join(tmp, "wt-feature")

	mustGit(t, "", "init", "--bare", "-b", "main", origin)
	mustGit(t, tmp, "clone", origin, repo)
	writeFile(t, filepath.Join(repo, "README.md"), "hello\n")
	mustGit(t, repo, "add", ".")
	mustGit(t, repo, "commit", "-m", "seed main")
	mustGit(t, repo, "push", "origin", "main")
	mustGit(t, repo, "worktree", "add", "-b", "feature", wtFeature)

	// Attempt to check out main from wtFeature — main is checked out in <repo>,
	// so this fails with "fatal: 'main' is already used by worktree at ...".
	deps := cli.DefaultDeps()
	deps.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	err := deps.Git.RunWithOutput(wtFeature, "checkout", "main")
	if err == nil {
		t.Fatal("expected `git checkout main` to fail with worktree conflict")
	}
	if !strings.Contains(err.Error(), "already used by worktree") {
		t.Errorf("Error() should embed git stderr (looking for 'already used by worktree'); got %q", err.Error())
	}
	if !isWorktreeConflictErr(err) {
		t.Errorf("isWorktreeConflictErr should return true for the wrapped error; got false")
	}
}

func mustGitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:norawexec,gosec // test harness
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = clitest.GitSafeEnv(
		"GIT_AUTHOR_NAME=test",
		"GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test",
		"GIT_COMMITTER_EMAIL=test@example.com",
	)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v (dir=%s) failed: %v", args, dir, err)
	}
	return string(out)
}

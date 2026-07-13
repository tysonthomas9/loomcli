package localworkspace

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureGitWorktreeFromBranchUsesFetchedDefaultBranch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	seed := filepath.Join(root, "seed")
	repo := filepath.Join(root, "repo")
	target := filepath.Join(root, "worktrees", "worker")

	git(t, "", "init", "--bare", remote)
	git(t, "", "init", seed)
	git(t, seed, "checkout", "-b", "main")
	git(t, seed, "config", "user.name", "Test User")
	git(t, seed, "config", "user.email", "test@example.test")
	writeFile(t, filepath.Join(seed, "base.txt"), "v1\n")
	git(t, seed, "add", "base.txt")
	git(t, seed, "commit", "-m", "base")
	git(t, seed, "remote", "add", "origin", remote)
	git(t, seed, "push", "origin", "main")

	git(t, "", "clone", remote, repo)
	git(t, repo, "checkout", "main")

	writeFile(t, filepath.Join(seed, "base.txt"), "v2\n")
	git(t, seed, "add", "base.txt")
	git(t, seed, "commit", "-m", "advance")
	git(t, seed, "push", "origin", "main")

	if err := EnsureGitWorktreeFromBranch(repo, target, "worker", "origin", "main"); err != nil {
		t.Fatalf("EnsureGitWorktreeFromBranch() error = %v", err)
	}

	gotBytes, err := os.ReadFile(filepath.Join(target, "base.txt"))
	if err != nil {
		t.Fatalf("read target file: %v", err)
	}
	if got := string(gotBytes); got != "v2\n" {
		t.Fatalf("target base.txt = %q, want fetched v2", got)
	}
}

func TestEnsureGitWorktreeFromBranchFallsBackToLocalDefaultBranch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	seed := filepath.Join(root, "seed")
	repo := filepath.Join(root, "repo")
	target := filepath.Join(root, "worktrees", "worker")

	git(t, "", "init", "--bare", remote)
	git(t, "", "init", seed)
	git(t, seed, "checkout", "-b", "main")
	git(t, seed, "config", "user.name", "Test User")
	git(t, seed, "config", "user.email", "test@example.test")
	writeFile(t, filepath.Join(seed, "base.txt"), "main\n")
	git(t, seed, "add", "base.txt")
	git(t, seed, "commit", "-m", "base")
	git(t, seed, "remote", "add", "origin", remote)
	git(t, seed, "push", "origin", "main")

	git(t, "", "clone", remote, repo)
	git(t, repo, "checkout", "-b", "browser-e2e")
	writeFile(t, filepath.Join(repo, "base.txt"), "local branch\n")
	git(t, repo, "add", "base.txt")
	git(t, repo, "commit", "-m", "local branch")

	if err := EnsureGitWorktreeFromBranch(repo, target, "worker", "origin", "browser-e2e"); err != nil {
		t.Fatalf("EnsureGitWorktreeFromBranch() error = %v", err)
	}

	gotBytes, err := os.ReadFile(filepath.Join(target, "base.txt"))
	if err != nil {
		t.Fatalf("read target file: %v", err)
	}
	if got := string(gotBytes); got != "local branch\n" {
		t.Fatalf("target base.txt = %q, want local branch content", got)
	}
}

func TestGitRemoteURL(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	const url = "https://github.com/owner/repo.git"
	dir := t.TempDir()
	git(t, "", "init", dir)
	git(t, dir, "remote", "add", "origin", url)

	got, err := GitRemoteURL(dir, "origin")
	if err != nil {
		t.Fatalf("GitRemoteURL: %v", err)
	}
	if got != url {
		t.Errorf("GitRemoteURL = %q, want %q", got, url)
	}

	// Empty remote name defaults to origin.
	if got, err := GitRemoteURL(dir, ""); err != nil || got != url {
		t.Errorf("GitRemoteURL(\"\") = %q, %v; want %q", got, err, url)
	}

	// A non-git directory is reported as an error (the "not a usable checkout" signal).
	if _, err := GitRemoteURL(t.TempDir(), "origin"); err == nil {
		t.Error("GitRemoteURL on a non-git dir should return an error")
	}
}

func TestEnsureDetachedGitWorktreeAtPRHead(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	seed := filepath.Join(root, "seed")
	repo := filepath.Join(root, "repo")
	target := filepath.Join(root, "pr-worktrees", "repo", "pr-7")

	git(t, "", "init", "--bare", remote)
	git(t, "", "init", seed)
	git(t, seed, "checkout", "-b", "main")
	git(t, seed, "config", "user.name", "Test User")
	git(t, seed, "config", "user.email", "test@example.test")
	writeFile(t, filepath.Join(seed, "base.txt"), "base\n")
	git(t, seed, "add", "base.txt")
	git(t, seed, "commit", "-m", "base")
	git(t, seed, "remote", "add", "origin", remote)
	git(t, seed, "push", "origin", "HEAD:refs/heads/main")

	writeFile(t, filepath.Join(seed, "pr.txt"), "pr v1\n")
	git(t, seed, "add", "pr.txt")
	git(t, seed, "commit", "-m", "pr head")
	headSHA := gitOutput(t, seed, "rev-parse", "HEAD")
	git(t, seed, "push", "origin", "HEAD:refs/pull/7/head")

	git(t, "", "clone", remote, repo)
	git(t, repo, "checkout", "main")

	gotSHA, err := EnsureDetachedGitWorktreeAtPRHead(repo, target, "origin", 7, headSHA)
	if err != nil {
		t.Fatalf("EnsureDetachedGitWorktreeAtPRHead() create error = %v", err)
	}
	if gotSHA != headSHA {
		t.Fatalf("create returned sha = %s, want %s", gotSHA, headSHA)
	}
	if _, err := os.Stat(filepath.Join(target, ".git")); err != nil {
		t.Fatalf("target .git does not exist: %v", err)
	}
	if got := gitOutput(t, target, "rev-parse", "HEAD"); got != headSHA {
		t.Fatalf("target HEAD = %s, want %s", got, headSHA)
	}
	if out, err := gitMaybe(target, "symbolic-ref", "-q", "HEAD"); err == nil {
		t.Fatalf("target HEAD is attached to %q, want detached", strings.TrimSpace(out))
	}

	// Clean-tree cache hit: a re-ensure with no changes is a no-op at the same sha.
	if gotSHA, err := EnsureDetachedGitWorktreeAtPRHead(repo, target, "origin", 7, headSHA); err != nil {
		t.Fatalf("EnsureDetachedGitWorktreeAtPRHead() clean cache hit error = %v", err)
	} else if gotSHA != headSHA {
		t.Fatalf("clean cache hit returned sha = %s, want %s", gotSHA, headSHA)
	}
	if got := gitOutput(t, target, "rev-parse", "HEAD"); got != headSHA {
		t.Fatalf("target HEAD after clean cache hit = %s, want %s", got, headSHA)
	}

	// Pristine guarantee: an untracked file at the right sha is scrubbed, not
	// handed back — a review checkout must faithfully match the PR head.
	sentinel := filepath.Join(target, "cache-hit-sentinel.txt")
	writeFile(t, sentinel, "cruft\n")
	if _, err := EnsureDetachedGitWorktreeAtPRHead(repo, target, "origin", 7, headSHA); err != nil {
		t.Fatalf("EnsureDetachedGitWorktreeAtPRHead() pristine scrub error = %v", err)
	}
	if got := gitOutput(t, target, "rev-parse", "HEAD"); got != headSHA {
		t.Fatalf("target HEAD after pristine scrub = %s, want %s", got, headSHA)
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatalf("untracked sentinel survived a re-ensure (err=%v), want it scrubbed", err)
	}

	git(t, target, "reset", "--hard", "HEAD~1")
	if _, err := EnsureDetachedGitWorktreeAtPRHead(repo, target, "origin", 7, headSHA); err != nil {
		t.Fatalf("EnsureDetachedGitWorktreeAtPRHead() drift repair error = %v", err)
	}
	if got := gitOutput(t, target, "rev-parse", "HEAD"); got != headSHA {
		t.Fatalf("target HEAD after drift repair = %s, want %s", got, headSHA)
	}

	writeFile(t, filepath.Join(seed, "pr.txt"), "pr v2\n")
	git(t, seed, "add", "pr.txt")
	git(t, seed, "commit", "-m", "advance pr head")
	newHeadSHA := gitOutput(t, seed, "rev-parse", "HEAD")
	git(t, seed, "push", "--force", "origin", "HEAD:refs/pull/7/head")

	gotSHA, err = EnsureDetachedGitWorktreeAtPRHead(repo, target, "origin", 7, newHeadSHA)
	if err != nil {
		t.Fatalf("EnsureDetachedGitWorktreeAtPRHead() advance error = %v", err)
	}
	if gotSHA != newHeadSHA {
		t.Fatalf("advance returned sha = %s, want %s", gotSHA, newHeadSHA)
	}
	if got := gitOutput(t, target, "rev-parse", "HEAD"); got != newHeadSHA {
		t.Fatalf("target HEAD after advance = %s, want %s", got, newHeadSHA)
	}
}

func TestEnsureDetachedGitWorktreeAtPRHeadRejectsFastForwardedTip(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	seed := filepath.Join(root, "seed")
	repo := filepath.Join(root, "repo")
	target := filepath.Join(root, "pr-worktrees", "repo", "pr-7")

	git(t, "", "init", "--bare", remote)
	git(t, "", "init", seed)
	git(t, seed, "checkout", "-b", "main")
	git(t, seed, "config", "user.name", "Test User")
	git(t, seed, "config", "user.email", "test@example.test")
	writeFile(t, filepath.Join(seed, "pr.txt"), "A\n")
	git(t, seed, "add", "pr.txt")
	git(t, seed, "commit", "-m", "PR head A")
	headA := gitOutput(t, seed, "rev-parse", "HEAD")
	git(t, seed, "remote", "add", "origin", remote)
	git(t, seed, "push", "origin", "HEAD:refs/heads/main")
	git(t, seed, "push", "origin", "HEAD:refs/pull/7/head")

	git(t, "", "clone", remote, repo)
	git(t, repo, "checkout", "main")
	if _, err := EnsureDetachedGitWorktreeAtPRHead(repo, target, "origin", 7, headA); err != nil {
		t.Fatalf("materialize head A: %v", err)
	}
	sentinel := filepath.Join(target, "stale-sentinel.txt")
	writeFile(t, sentinel, "leave untouched\n")

	writeFile(t, filepath.Join(seed, "pr.txt"), "B\n")
	git(t, seed, "add", "pr.txt")
	git(t, seed, "commit", "-m", "PR head B")
	headB := gitOutput(t, seed, "rev-parse", "HEAD")
	git(t, seed, "push", "origin", "HEAD:refs/pull/7/head")

	gotTip, err := EnsureDetachedGitWorktreeAtPRHead(repo, target, "origin", 7, " "+strings.ToUpper(headA)+" ")
	var changed *PRHeadChangedError
	if !errors.As(err, &changed) {
		t.Fatalf("stale ensure error = %v, want PRHeadChangedError", err)
	}
	if gotTip != headB || changed.TipSHA != headB {
		t.Fatalf("stale tip = returned:%q error:%q, want %q", gotTip, changed.TipSHA, headB)
	}
	if !strings.EqualFold(strings.TrimSpace(changed.ExpectedSHA), headA) {
		t.Fatalf("stale expected sha = %q, want %q", changed.ExpectedSHA, headA)
	}
	if got := gitOutput(t, target, "rev-parse", "HEAD"); got != headA {
		t.Fatalf("target HEAD after stale outcome = %s, want untouched %s", got, headA)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("stale outcome scrubbed existing worktree: %v", err)
	}

	gotSHA, err := EnsureDetachedGitWorktreeAtPRHead(repo, target, "origin", 7, "\n"+strings.ToUpper(headB)+"\t")
	if err != nil {
		t.Fatalf("ensure expected head B: %v", err)
	}
	if gotSHA != headB {
		t.Fatalf("expected-B ensure returned %q, want %q", gotSHA, headB)
	}
	if got := gitOutput(t, target, "rev-parse", "HEAD"); got != headB {
		t.Fatalf("target HEAD after expected-B ensure = %s, want %s", got, headB)
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatalf("expected-B ensure did not scrub sentinel (err=%v)", err)
	}
}

func TestPRHeadReviewWorktreePath(t *testing.T) {
	root := t.TempDir()
	got, err := PRReviewWorktreePath(root, "repo", 7)
	if err != nil {
		t.Fatalf("PRReviewWorktreePath() error = %v", err)
	}
	want := filepath.Join(root, ".loom", "pr-worktrees", "repo", "pr-7")
	if got != want {
		t.Fatalf("PRReviewWorktreePath() = %q, want %q", got, want)
	}
	if !PathContains(root, got) {
		t.Fatalf("PRReviewWorktreePath() = %q, want under %q", got, root)
	}

	if _, err := PRReviewWorktreePath("", "repo", 7); err == nil {
		t.Fatal("PRReviewWorktreePath() with empty workspace path returned nil error")
	}
	if _, err := PRReviewWorktreePath(root, "", 7); err == nil {
		t.Fatal("PRReviewWorktreePath() with empty repo name returned nil error")
	}
	if _, err := PRReviewWorktreePath(root, "repo", 0); err == nil {
		t.Fatal("PRReviewWorktreePath() with zero PR number returned nil error")
	}
	if _, err := PRReviewWorktreePath(root, "repo", -1); err == nil {
		t.Fatal("PRReviewWorktreePath() with negative PR number returned nil error")
	}
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:norawexec,gosec // fixed test helper commands.
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
}

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitMaybe(dir, args...)
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, strings.TrimSpace(out))
	}
	return strings.TrimSpace(out)
}

func gitMaybe(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...) //nolint:norawexec,gosec // fixed test helper commands.
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestRecordPRReviewContext(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	seed := filepath.Join(root, "seed")
	repo := filepath.Join(root, "repo")
	target := filepath.Join(root, "wt", "pr-7")

	git(t, "", "init", "--bare", remote)
	git(t, "", "init", seed)
	git(t, seed, "checkout", "-b", "main")
	git(t, seed, "config", "user.name", "T")
	git(t, seed, "config", "user.email", "t@t")
	writeFile(t, filepath.Join(seed, "base.txt"), "base\n")
	git(t, seed, "add", "base.txt")
	git(t, seed, "commit", "-m", "base")
	git(t, seed, "remote", "add", "origin", remote)
	git(t, seed, "push", "origin", "HEAD:refs/heads/main")
	baseSHA := gitOutput(t, seed, "rev-parse", "HEAD")
	// A PR head commit on top of base.
	writeFile(t, filepath.Join(seed, "pr.txt"), "pr\n")
	git(t, seed, "add", "pr.txt")
	git(t, seed, "commit", "-m", "pr head")
	prHeadSHA := gitOutput(t, seed, "rev-parse", "HEAD")
	git(t, seed, "push", "origin", "HEAD:refs/pull/7/head")

	git(t, "", "clone", remote, repo)
	if _, err := EnsureDetachedGitWorktreeAtPRHead(repo, target, "origin", 7, prHeadSHA); err != nil {
		t.Fatalf("worktree: %v", err)
	}

	got, err := RecordPRReviewContext(target, "origin", "main", map[string]string{"Pr": "7", "Title": "Add X"})
	if err != nil {
		t.Fatalf("RecordPRReviewContext: %v", err)
	}
	if got != baseSHA {
		t.Fatalf("returned base = %s, want %s", got, baseSHA)
	}
	// Recorded per-worktree, readable, and the review diff shows the PR change.
	if rec := strings.TrimSpace(gitOutput(t, target, "config", "loom.reviewBase")); rec != baseSHA {
		t.Fatalf("loom.reviewBase = %s, want %s", rec, baseSHA)
	}
	if diff := gitOutput(t, target, "diff", baseSHA+"...HEAD", "--name-only"); !strings.Contains(diff, "pr.txt") {
		t.Fatalf("review diff = %q, want pr.txt", diff)
	}
	if pr := strings.TrimSpace(gitOutput(t, target, "config", "loom.reviewPr")); pr != "7" {
		t.Fatalf("loom.reviewPr = %q, want 7", pr)
	}
}

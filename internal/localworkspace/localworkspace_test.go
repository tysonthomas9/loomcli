package localworkspace

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/gitbranch"
	"github.com/tysonthomas9/loomcli/internal/lockfile"
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

func TestRunGitHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := runGit(ctx, t.TempDir(), "status")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("runGit error = %v, want context canceled", err)
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

func TestEnsureGitWorktreeFromBranchRecoversCorruptBranchRef(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	target := filepath.Join(root, "worktrees", "worker")

	git(t, "", "init", "-b", "main", repo)
	git(t, repo, "config", "user.name", "Test User")
	git(t, repo, "config", "user.email", "test@example.test")
	writeFile(t, filepath.Join(repo, "base.txt"), "main\n")
	git(t, repo, "add", "base.txt")
	git(t, repo, "commit", "-m", "base")
	git(t, repo, "checkout", "-b", "worker")
	writeFile(t, filepath.Join(repo, "worker.txt"), "worker\n")
	git(t, repo, "add", "worker.txt")
	git(t, repo, "commit", "-m", "worker")
	workerSHA := gitOut(t, repo, "rev-parse", "HEAD")
	git(t, repo, "checkout", "main")
	corruptLocalBranchRef(t, repo, "worker")

	if err := EnsureGitWorktreeFromBranch(repo, target, "worker", "", "main"); err != nil {
		t.Fatalf("EnsureGitWorktreeFromBranch() error = %v", err)
	}
	if got := gitOut(t, target, "rev-parse", "HEAD"); got != workerSHA {
		t.Fatalf("worktree HEAD = %s, want recovered reflog SHA %s", got, workerSHA)
	}
	if got := gitOut(t, target, "branch", "--show-current"); got != "worker" {
		t.Fatalf("worktree branch = %q, want worker", got)
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
	target := filepath.Join(root, "pr-worktrees", "repo", "pr-7", "first")

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

	gotSHA, err := EnsureDetachedGitWorktreeAtPRHead(context.Background(), repo, target, "origin", 7, headSHA)
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

	// A new review has its own checkout, leaving tracked edits and scratch files
	// from the first review untouched.
	sentinel := filepath.Join(target, "scratch.txt")
	writeFile(t, sentinel, "review notes\n")
	writeFile(t, filepath.Join(target, "pr.txt"), "edited by reviewer\n")
	second := filepath.Join(root, "pr-worktrees", "repo", "pr-7", "second")
	if _, err := EnsureDetachedGitWorktreeAtPRHead(context.Background(), repo, second, "origin", 7, headSHA); err != nil {
		t.Fatalf("create second review: %v", err)
	}
	if got, err := os.ReadFile(sentinel); err != nil || string(got) != "review notes\n" {
		t.Fatalf("first review scratch = %q, %v", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(target, "pr.txt")); err != nil || string(got) != "edited by reviewer\n" {
		t.Fatalf("first review edit = %q, %v", got, err)
	}
	if got := gitOutput(t, second, "rev-parse", "HEAD"); got != headSHA {
		t.Fatalf("second review HEAD = %s, want %s", got, headSHA)
	}
	if _, err := EnsureDetachedGitWorktreeAtPRHead(context.Background(), repo, target, "origin", 7, headSHA); err == nil {
		t.Fatal("reusing an existing review path succeeded")
	}
	if got, err := os.ReadFile(sentinel); err != nil || string(got) != "review notes\n" {
		t.Fatalf("reuse changed first review scratch = %q, %v", got, err)
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
	target := filepath.Join(root, "pr-worktrees", "repo", "pr-7", "first")

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
	if _, err := EnsureDetachedGitWorktreeAtPRHead(context.Background(), repo, target, "origin", 7, headA); err != nil {
		t.Fatalf("materialize head A: %v", err)
	}
	sentinel := filepath.Join(target, "stale-sentinel.txt")
	writeFile(t, sentinel, "leave untouched\n")

	writeFile(t, filepath.Join(seed, "pr.txt"), "B\n")
	git(t, seed, "add", "pr.txt")
	git(t, seed, "commit", "-m", "PR head B")
	headB := gitOutput(t, seed, "rev-parse", "HEAD")
	git(t, seed, "push", "origin", "HEAD:refs/pull/7/head")

	staleTarget := filepath.Join(root, "pr-worktrees", "repo", "pr-7", "stale")
	gotTip, err := EnsureDetachedGitWorktreeAtPRHead(context.Background(), repo, staleTarget, "origin", 7, " "+strings.ToUpper(headA)+" ")
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
	if _, err := os.Lstat(staleTarget); !os.IsNotExist(err) {
		t.Fatalf("stale outcome created a new review worktree: %v", err)
	}

	next := filepath.Join(root, "pr-worktrees", "repo", "pr-7", "second")
	gotSHA, err := EnsureDetachedGitWorktreeAtPRHead(context.Background(), repo, next, "origin", 7, "\n"+strings.ToUpper(headB)+"\t")
	if err != nil {
		t.Fatalf("ensure expected head B: %v", err)
	}
	if gotSHA != headB {
		t.Fatalf("expected-B ensure returned %q, want %q", gotSHA, headB)
	}
	if got := gitOutput(t, next, "rev-parse", "HEAD"); got != headB {
		t.Fatalf("new target HEAD after expected-B ensure = %s, want %s", got, headB)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("expected-B ensure changed old review scratch: %v", err)
	}
}

func TestPRReviewWorktreeAddFailureLeavesNoDirectory(t *testing.T) {
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	seed := filepath.Join(root, "seed")
	repo := filepath.Join(root, "repo")
	target := filepath.Join(root, "pr-worktrees", "repo", "pr-7", "failed")
	git(t, "", "init", "--bare", remote)
	git(t, "", "init", seed)
	git(t, seed, "config", "user.name", "Test User")
	git(t, seed, "config", "user.email", "test@example.test")
	writeFile(t, filepath.Join(seed, "file.txt"), "content\n")
	git(t, seed, "add", "file.txt")
	git(t, seed, "commit", "-m", "head")
	head := gitOutput(t, seed, "rev-parse", "HEAD")
	git(t, seed, "remote", "add", "origin", remote)
	git(t, seed, "push", "origin", "HEAD:refs/pull/7/head")
	git(t, "", "clone", remote, repo)
	hooks := filepath.Join(root, "hooks")
	if err := os.Mkdir(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	hook := filepath.Join(hooks, "post-checkout")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "config", "core.hooksPath", hooks)
	if _, err := EnsureDetachedGitWorktreeAtPRHead(context.Background(), repo, target, "origin", 7, head); err == nil {
		t.Fatal("worktree add unexpectedly succeeded")
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("partial worktree remains: %v", err)
	}
}

func TestPRReviewCrossProcessLock(t *testing.T) {
	if os.Getenv("LOOM_PR_REVIEW_LOCK_CHILD") == "1" {
		_, err := EnsureDetachedGitWorktreeAtPRHead(context.Background(), os.Getenv("LOOM_PR_REVIEW_REPO"), os.Getenv("LOOM_PR_REVIEW_TARGET"), "origin", 7, os.Getenv("LOOM_PR_REVIEW_HEAD"))
		if err != nil {
			t.Fatal(err)
		}
		return
	}
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	seed := filepath.Join(root, "seed")
	repo := filepath.Join(root, "repo")
	parent := filepath.Join(root, "pr-worktrees", "repo", "pr-7")
	git(t, "", "init", "--bare", remote)
	git(t, "", "init", seed)
	git(t, seed, "config", "user.name", "Test User")
	git(t, seed, "config", "user.email", "test@example.test")
	writeFile(t, filepath.Join(seed, "file.txt"), "content\n")
	git(t, seed, "add", "file.txt")
	git(t, seed, "commit", "-m", "head")
	head := gitOutput(t, seed, "rev-parse", "HEAD")
	git(t, seed, "remote", "add", "origin", remote)
	git(t, seed, "push", "origin", "HEAD:refs/pull/7/head")
	git(t, "", "clone", remote, repo)
	if err := os.MkdirAll(filepath.Dir(parent), 0o755); err != nil {
		t.Fatal(err)
	}
	lock, err := os.OpenFile(parent+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := lockfile.FlockExclusiveBlocking(lock); err != nil {
		t.Fatal(err)
	}
	locked := true
	defer func() {
		if locked {
			_ = lockfile.FlockUnlock(lock)
		}
	}()
	childCtx, cancelChildren := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelChildren()
	var children []*exec.Cmd
	for _, id := range []string{"serve", "daemon"} {
		target := filepath.Join(parent, id)
		cmd := exec.CommandContext(childCtx, os.Args[0], "-test.run=^TestPRReviewCrossProcessLock$") //nolint:norawexec // Child test process verifies the cross-process review lock.
		cmd.Env = append(os.Environ(), "LOOM_PR_REVIEW_LOCK_CHILD=1", "LOOM_PR_REVIEW_REPO="+repo, "LOOM_PR_REVIEW_TARGET="+target, "LOOM_PR_REVIEW_HEAD="+head)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		children = append(children, cmd)
		defer func() { _ = cmd.Process.Kill() }()
	}
	time.Sleep(150 * time.Millisecond)
	for _, id := range []string{"serve", "daemon"} {
		if _, err := os.Lstat(filepath.Join(parent, id)); !os.IsNotExist(err) {
			t.Fatalf("%s created a worktree while PR lock was held: %v", id, err)
		}
	}
	if err := lockfile.FlockUnlock(lock); err != nil {
		t.Fatal(err)
	}
	locked = false
	// A .git file appears before git worktree add finishes. Wait for both
	// creators to exit before inspecting their checkouts.
	for _, child := range children {
		if err := child.Wait(); err != nil {
			t.Fatalf("review process failed: %v", err)
		}
	}
	for _, id := range []string{"serve", "daemon"} {
		target := filepath.Join(parent, id)
		if got := gitOutput(t, target, "rev-parse", "HEAD"); got != head {
			t.Fatalf("%s HEAD = %s, want %s", id, got, head)
		}
	}
}

func TestPRHeadReviewWorktreePath(t *testing.T) {
	root := t.TempDir()
	got, err := PRReviewWorktreePath(root, "repo", 7, "review-1")
	if err != nil {
		t.Fatalf("PRReviewWorktreePath() error = %v", err)
	}
	want := filepath.Join(root, ".loom", "pr-worktrees", "repo", "pr-7", "review-1")
	if got != want {
		t.Fatalf("PRReviewWorktreePath() = %q, want %q", got, want)
	}
	if !PathContains(root, got) {
		t.Fatalf("PRReviewWorktreePath() = %q, want under %q", got, root)
	}

	if _, err := PRReviewWorktreePath("", "repo", 7, "review-1"); err == nil {
		t.Fatal("PRReviewWorktreePath() with empty workspace path returned nil error")
	}
	if _, err := PRReviewWorktreePath(root, "", 7, "review-1"); err == nil {
		t.Fatal("PRReviewWorktreePath() with empty repo name returned nil error")
	}
	if _, err := PRReviewWorktreePath(root, "repo", 0, "review-1"); err == nil {
		t.Fatal("PRReviewWorktreePath() with zero PR number returned nil error")
	}
	if _, err := PRReviewWorktreePath(root, "repo", -1, "review-1"); err == nil {
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

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitMaybe(dir, args...)
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, strings.TrimSpace(out))
	}
	return strings.TrimSpace(out)
}

func corruptLocalBranchRef(t *testing.T, repoPath, branch string) {
	t.Helper()
	common, err := gitbranch.CommonDir(repoPath)
	if err != nil {
		t.Fatalf("git common dir: %v", err)
	}
	refPath := filepath.Join(common, "refs", "heads", filepath.FromSlash(branch))
	if err := os.MkdirAll(filepath.Dir(refPath), 0o755); err != nil {
		t.Fatalf("mkdir branch ref parent: %v", err)
	}
	if err := os.WriteFile(refPath, nil, 0o644); err != nil {
		t.Fatalf("corrupt branch ref: %v", err)
	}
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
	if _, err := EnsureDetachedGitWorktreeAtPRHead(context.Background(), repo, target, "origin", 7, prHeadSHA); err != nil {
		t.Fatalf("worktree: %v", err)
	}

	got, err := RecordPRReviewContext(context.Background(), target, "origin", "main", map[string]string{"Pr": "7", "Title": "Add X"})
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

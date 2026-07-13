package localworkspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/gitbranch"
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

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:norawexec,gosec // Test helper creates real git repos.
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out))
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

package localworkspace

import (
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

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:gosec,norawexec // fixed test helper commands.
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

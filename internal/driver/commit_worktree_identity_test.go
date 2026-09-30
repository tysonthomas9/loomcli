package driver

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommitWorktreeUsesGitUserIdentity(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(config, []byte("[user]\nname = Worktree User\nemail = worktree@example.test\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", config)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...) //nolint:norawexec // Test uses a real temporary Git repository.
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "x.txt"), []byte("x\n"), 0600); err != nil {
		t.Fatal(err)
	}
	run("add", "x.txt")
	if err := CommitWorktree(context.Background(), dir, "worktree commit"); err != nil {
		t.Fatal(err)
	}
	if got := run("show", "-s", "--format=%an <%ae>|%cn <%ce>", "HEAD"); got != "Worktree User <worktree@example.test>|Worktree User <worktree@example.test>" {
		t.Fatal(got)
	}
}

func TestCommitWorktreeFallsBackWithoutGitIdentity(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_AUTHOR_NAME", "")
	t.Setenv("GIT_AUTHOR_EMAIL", "")
	t.Setenv("GIT_COMMITTER_NAME", "")
	t.Setenv("GIT_COMMITTER_EMAIL", "")
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...) //nolint:norawexec // Isolated real Git repository fixture.
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "x.txt"), []byte("x\n"), 0600); err != nil {
		t.Fatal(err)
	}
	run("add", "x.txt")
	if err := CommitWorktree(context.Background(), dir, "fallback commit"); err != nil {
		t.Fatal(err)
	}
	if got := run("show", "-s", "--format=%an <%ae>|%cn <%ce>", "HEAD"); got != "Loom <loom@localhost>|Loom <loom@localhost>" {
		t.Fatal(got)
	}
}

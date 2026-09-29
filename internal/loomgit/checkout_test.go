package loomgit

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckoutNewBranchIgnoresInheritedGitDirectory(t *testing.T) {
	repo := t.TempDir()
	runGit := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...) //nolint:norawexec // Synthetic repository setup for the Git boundary test.
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	runGit("init", "-q")
	if err := os.WriteFile(filepath.Join(repo, "file"), []byte("base"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit("add", "file")
	runGit("-c", "user.name=Loom", "-c", "user.email=loom@localhost", "commit", "-qm", "base")
	base := runGit("rev-parse", "HEAD")
	t.Setenv("GIT_DIR", filepath.Join(t.TempDir(), "invalid-git-dir"))
	if err := CheckoutNewBranch(context.Background(), repo, "loom/test", base); err != nil {
		t.Fatal(err)
	}
	head, err := os.ReadFile(filepath.Join(repo, ".git", "HEAD"))
	if err != nil || string(head) != "ref: refs/heads/loom/test\n" {
		t.Fatalf("clone HEAD = %q, %v", head, err)
	}
}

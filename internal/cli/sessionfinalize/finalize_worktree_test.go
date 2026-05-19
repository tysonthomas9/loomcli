package sessionfinalize

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestWithWorktreeNilSessionReturnsDiffResult(t *testing.T) {
	worktree := t.TempDir()
	runGit := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...) //nolint:norawexec // test creates an isolated git repo.
		cmd.Dir = worktree
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}

	runGit("init")
	runGit("config", "user.email", "test@example.com")
	runGit("config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(worktree, "file.txt"), []byte("one\n"), 0600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	runGit("add", "file.txt")
	runGit("commit", "-m", "initial")
	before := runGit("rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(worktree, "file.txt"), []byte("one\ntwo\n"), 0600); err != nil {
		t.Fatalf("update file: %v", err)
	}
	runGit("add", "file.txt")
	runGit("commit", "-m", "update")

	result, err := WithWorktree(nil, WithWorktreeOptions{
		WorktreePath: worktree,
		BeforeRef:    before[:len(before)-1],
	})
	if err != nil {
		t.Fatalf("WithWorktree nil session: %v", err)
	}
	if result.DiffStats.FilesChanged != 1 || result.DiffStats.LinesAdded != 1 {
		t.Fatalf("diff stats = %+v", result.DiffStats)
	}
	if len(result.FilesTouched) != 1 || result.FilesTouched[0] != "file.txt" {
		t.Fatalf("files touched = %v", result.FilesTouched)
	}
	if !result.HasDiffPatch {
		t.Fatal("expected diff patch for changed worktree")
	}
}

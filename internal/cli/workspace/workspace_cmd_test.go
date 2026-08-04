package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindMainRepoPath(t *testing.T) {
	mainRepo := filepath.Join(t.TempDir(), "main")
	worktree := filepath.Join(t.TempDir(), "worktree")
	gitDir := filepath.Join(mainRepo, ".git", "worktrees", "feature")
	if err := os.MkdirAll(gitDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(worktree, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: "+gitDir+"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	if got := findMainRepoPath(worktree); got != mainRepo {
		t.Errorf("findMainRepoPath() = %q, want %q", got, mainRepo)
	}
}

func TestFindMainRepoPath_RelativeGitDir(t *testing.T) {
	root := t.TempDir()
	mainRepo := filepath.Join(root, "main")
	worktree := filepath.Join(root, "worktree")
	gitDir := filepath.Join(mainRepo, ".git", "worktrees", "feature")
	if err := os.MkdirAll(gitDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(worktree, 0755); err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(worktree, gitDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: "+rel+"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	if got := findMainRepoPath(worktree); got != mainRepo {
		t.Errorf("findMainRepoPath() = %q, want %q", got, mainRepo)
	}
}

func TestFindMainRepoPath_InvalidWorktree(t *testing.T) {
	if got := findMainRepoPath(t.TempDir()); got != "" {
		t.Errorf("findMainRepoPath() = %q, want empty", got)
	}
}

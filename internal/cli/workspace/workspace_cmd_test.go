package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
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

func TestRemoveOneWorktreeSuccessAndForceFallback(t *testing.T) {
	worktree, mainRepo := createWorktreeGitFile(t)

	deps, _, _, _, _ := NewTestDeps(t)
	mock := NewCommandMock(t, []CommandStub{{}})
	mock.InstallOn(deps)
	if err := removeOneWorktree(deps, worktree, "api"); err != nil {
		t.Fatalf("removeOneWorktree success: %v", err)
	}
	if calls := mock.Calls(); len(calls) != 1 || calls[0].Dir != mainRepo || strings.Join(calls[0].Args, " ") != "worktree remove "+worktree {
		t.Fatalf("success calls = %+v, main=%s worktree=%s", calls, mainRepo, worktree)
	}

	oldForce := wsRemoveForce
	t.Cleanup(func() { wsRemoveForce = oldForce })
	wsRemoveForce = true

	worktree, mainRepo = createWorktreeGitFile(t)
	deps, _, _, _, _ = NewTestDeps(t)
	mock = NewCommandMock(t, []CommandStub{{Err: errors.New("dirty worktree")}, {}})
	mock.InstallOn(deps)
	if err := removeOneWorktree(deps, worktree, "api"); err != nil {
		t.Fatalf("removeOneWorktree force fallback: %v", err)
	}
	calls := mock.Calls()
	if len(calls) != 2 {
		t.Fatalf("force calls = %+v", calls)
	}
	if calls[0].Dir != mainRepo || strings.Join(calls[0].Args, " ") != "worktree remove "+worktree {
		t.Fatalf("first force call = %+v", calls[0])
	}
	if calls[1].Dir != mainRepo || strings.Join(calls[1].Args, " ") != "worktree remove --force "+worktree {
		t.Fatalf("second force call = %+v", calls[1])
	}
}

func createWorktreeGitFile(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	mainRepo := filepath.Join(root, "main")
	worktree := filepath.Join(root, "worktree")
	gitDir := filepath.Join(mainRepo, ".git", "worktrees", "api")
	if err := os.MkdirAll(gitDir, 0755); err != nil {
		t.Fatalf("mkdir gitdir: %v", err)
	}
	if err := os.MkdirAll(worktree, 0755); err != nil {
		t.Fatalf("mkdir worktree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: "+gitDir+"\n"), 0644); err != nil {
		t.Fatalf("write .git: %v", err)
	}
	return worktree, mainRepo
}

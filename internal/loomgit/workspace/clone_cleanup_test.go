package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func freshCloneFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	src := filepath.Join(root, "source")
	if err := os.Mkdir(src, 0o700); err != nil {
		t.Fatal(err)
	}
	gitDeleteTest(t, src, "init", "-b", "main")
	gitDeleteTest(t, src, "config", "user.name", "Tester")
	gitDeleteTest(t, src, "config", "user.email", "tester@example.test")
	if err := os.WriteFile(filepath.Join(src, "base.txt"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitDeleteTest(t, src, "add", "base.txt")
	gitDeleteTest(t, src, "commit", "-m", "base")
	bare := filepath.Join(root, "provider.git")
	gitDeleteTest(t, root, "init", "--bare", bare)
	gitDeleteTest(t, src, "remote", "add", "origin", bare)
	gitDeleteTest(t, src, "push", "origin", "main")
	gitDeleteTest(t, bare, "symbolic-ref", "HEAD", "refs/heads/main")
	clone := filepath.Join(root, "clone")
	gitDeleteTest(t, root, "clone", bare, clone)
	return clone
}

func TestP120CleanupFreshClonePreservesNewWork(t *testing.T) {
	for _, mutation := range []string{"untracked", "ignored", "branch", "unpushed-commit", "stash", "tag", "worktree", "detached-worktree"} {
		t.Run(mutation, func(t *testing.T) {
			clone := freshCloneFixture(t)
			gitDeleteTest(t, clone, "config", "user.name", "Tester")
			gitDeleteTest(t, clone, "config", "user.email", "tester@example.test")
			switch mutation {
			case "untracked":
				if err := os.WriteFile(filepath.Join(clone, "user.txt"), []byte("keep"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "ignored":
				if err := os.WriteFile(filepath.Join(clone, ".gitignore"), []byte("cache\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				gitDeleteTest(t, clone, "add", ".gitignore")
				gitDeleteTest(t, clone, "commit", "-m", "ignore cache")
				gitDeleteTest(t, clone, "push", "origin", "main")
				if err := os.WriteFile(filepath.Join(clone, "cache"), []byte("keep"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "branch":
				gitDeleteTest(t, clone, "checkout", "-b", "user")
			case "unpushed-commit":
				if err := os.WriteFile(filepath.Join(clone, "user.txt"), []byte("keep"), 0o600); err != nil {
					t.Fatal(err)
				}
				gitDeleteTest(t, clone, "add", "user.txt")
				gitDeleteTest(t, clone, "commit", "-m", "local work")
			case "stash":
				if err := os.WriteFile(filepath.Join(clone, "base.txt"), []byte("changed"), 0o600); err != nil {
					t.Fatal(err)
				}
				gitDeleteTest(t, clone, "stash")
			case "worktree":
				gitDeleteTest(t, clone, "worktree", "add", "-b", "other", filepath.Join(filepath.Dir(clone), "other"))
			case "detached-worktree":
				gitDeleteTest(t, clone, "worktree", "add", "--detach", filepath.Join(filepath.Dir(clone), "other"), "HEAD")
			}
			if err := CleanupFreshClone(clone); err == nil || !strings.Contains(err.Error(), clone) {
				t.Fatalf("cleanup error=%v", err)
			}
			if _, err := os.Stat(clone); err != nil {
				t.Fatalf("clone lost: %v", err)
			}
		})
	}
}

func TestP120CleanupFreshCloneRemovesOnlyFreshClone(t *testing.T) {
	clone := freshCloneFixture(t)
	gitDeleteTest(t, clone, "checkout", "-b", "loom/ws/WS/interactive/lead", "origin/main")
	if err := CleanupFreshClone(clone); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(clone); !os.IsNotExist(err) {
		t.Fatalf("clone still exists: %v", err)
	}
}

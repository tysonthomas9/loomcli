package loomgit_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:norawexec // Real temporary Git repository fixture.
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}
func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}
func fixture(t *testing.T) (string, *journal.SQLite) {
	t.Helper()
	dir := t.TempDir()
	config := filepath.Join(t.TempDir(), "gitconfig")
	write(t, filepath.Dir(config), filepath.Base(config), "[user]\nname = Real User\nemail = user@example.test\n")
	t.Setenv("GIT_CONFIG_GLOBAL", config)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	git(t, dir, "init", "-q")
	write(t, dir, "base.txt", "base\n")
	git(t, dir, "add", "base.txt")
	git(t, dir, "commit", "-qm", "base")
	store, err := journal.OpenSQLite(filepath.Join(t.TempDir(), "settings.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return dir, store
}
func request(paths ...string) loomgit.CommitRequest {
	return loomgit.CommitRequest{Workspace: "ws", Paths: paths, Message: "agent change", ChangeID: "change-1", Agent: "codex"}
}
func TestCommitNamedPathsPreservesUserIndexAndIdentity(t *testing.T) {
	dir, store := fixture(t)
	write(t, dir, "x.rs", "agent\n")
	write(t, dir, "y.rs", "user\n")
	git(t, dir, "add", "y.rs")
	before := git(t, dir, "ls-files", "--stage")
	sha, err := loomgit.Commit(context.Background(), dir, store, request("x.rs"))
	if err != nil {
		t.Fatal(err)
	}
	if got := git(t, dir, "ls-files", "--stage"); got != before {
		t.Fatalf("index changed:\n%s\nwas:\n%s", got, before)
	}
	if got := git(t, dir, "show", "--pretty=format:", "--name-only", sha); got != "x.rs" {
		t.Fatalf("commit paths: %q", got)
	}
	if got := git(t, dir, "show", "-s", "--format=%an <%ae>|%cn <%ce>|%B", sha); !strings.Contains(got, "Real User <user@example.test>|Real User <user@example.test>") || !strings.Contains(got, "Loom-Change-Id: change-1") || !strings.Contains(got, "Loom-Agent: codex") {
		t.Fatal(got)
	}
}
func TestCommitRefusalsAndGate(t *testing.T) {
	dir, store := fixture(t)
	ctx := context.Background()
	write(t, dir, "x.rs", "agent\n")
	original := git(t, dir, "rev-parse", "HEAD")
	for _, paths := range [][]string{nil, {"base.txt"}, {"../outside"}} {
		if _, err := loomgit.Commit(ctx, dir, store, request(paths...)); err == nil {
			t.Fatalf("accepted %v", paths)
		}
		if got := git(t, dir, "rev-parse", "HEAD"); got != original {
			t.Fatal("HEAD changed")
		}
	}
	if err := store.SetAutoCommit(ctx, "ws", false); err != nil {
		t.Fatal(err)
	}
	if enabled, err := store.AutoCommit(ctx, "another-workspace"); err != nil || !enabled {
		t.Fatalf("new workspace auto-commit = %v, %v", enabled, err)
	}
	if _, err := loomgit.Commit(ctx, dir, store, request("x.rs")); err == nil {
		t.Fatal("auto-commit accepted while off")
	}
	req := request("x.rs")
	req.UserRequested = true
	if _, err := loomgit.Commit(ctx, dir, store, req); err != nil {
		t.Fatal(err)
	}
}
func TestCommitRefusesMergeAndSubmodulePath(t *testing.T) {
	dir, store := fixture(t)
	write(t, dir, "x.rs", "agent\n")
	head := git(t, dir, "rev-parse", "HEAD")
	mergeHead := git(t, dir, "rev-parse", "--git-path", "MERGE_HEAD")
	if !filepath.IsAbs(mergeHead) {
		mergeHead = filepath.Join(dir, mergeHead)
	}
	if err := os.WriteFile(mergeHead, []byte(head+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loomgit.Commit(context.Background(), dir, store, request("x.rs")); err == nil {
		t.Fatal("merge accepted")
	}
	if err := os.Remove(mergeHead); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0700); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "sub/.git", "gitdir: ../modules/sub\n")
	write(t, dir, "sub/x.rs", "inner\n")
	if _, err := loomgit.Commit(context.Background(), dir, store, request("sub/x.rs")); err == nil {
		t.Fatal("submodule path accepted")
	}
}

func TestProductionGoHasNoClaudeCoauthorTrailer(t *testing.T) {
	root := filepath.Join("..", "..")
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(body), "Co-Authored-By: Claude") {
			t.Errorf("hard-coded Claude trailer in %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCommitOnScratchClone(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "loomcli")
	cmd := exec.Command("git", "clone", "--local", "-q", root, dir) //nolint:norawexec // Real local clone is required acceptance evidence.
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clone: %v: %s", err, out)
	}
	config := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(config, []byte("[user]\nname = Real User\nemail = user@example.test\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", config)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	readme := filepath.Join(dir, "README.md")
	f, err := os.OpenFile(readme, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("\nP2.5 scratch edit\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "p25-staged.txt", "user staged\n")
	git(t, dir, "add", "p25-staged.txt")
	beforeDiff := git(t, dir, "diff", "--stat")
	beforeCached := git(t, dir, "diff", "--cached", "--stat")
	store, err := journal.OpenSQLite(filepath.Join(t.TempDir(), "settings.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	if _, err := loomgit.Commit(context.Background(), dir, store, request("README.md")); err != nil {
		t.Fatal(err)
	}
	t.Logf("before diff: %s; before cached: %s; after diff: %s; after cached: %s", beforeDiff, beforeCached, git(t, dir, "diff", "--stat"), git(t, dir, "diff", "--cached", "--stat"))
}

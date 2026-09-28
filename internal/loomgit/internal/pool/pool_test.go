package pool

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func fixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	config := filepath.Join(root, ".gitconfig")
	if err := os.WriteFile(config, []byte("[user]\n\tname = Pool Test\n\temail = pool@example.test\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", root)
	repo := filepath.Join(root, "source")
	if err := os.Mkdir(repo, 0700); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "init", "-q")
	git(t, repo, "-c", "user.name=Pool Test", "-c", "user.email=pool@example.test", "commit", "--allow-empty", "-qm", "base")
	return repo, filepath.Join(root, "store.sqlite")
}

func openPool(t *testing.T, db string) *Pool {
	t.Helper()
	s, err := openSQLiteRetry(db)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return New(s, gitexec.Options{GlobalConfig: filepath.Join(filepath.Dir(db), ".gitconfig")})
}

func openSQLiteRetry(db string) (*journal.SQLite, error) {
	var s *journal.SQLite
	var err error
	for attempt := 0; attempt < 20; attempt++ {
		s, err = journal.OpenSQLite(db)
		if err == nil || !strings.Contains(err.Error(), "database is locked") {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	return s, err
}

func TestCreateRemoveAndFailedAdd(t *testing.T) {
	repo, db := fixture(t)
	ctx := context.Background()
	r, err := openPool(t, db).Admit(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	bad := r.LinkedWorktree(filepath.Join(filepath.Dir(repo), "bad"))
	if err := bad.Create(ctx, "missing-base-ref"); err == nil {
		t.Fatal("expected failed add")
	}
	if _, err := os.Lstat(bad.Path()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed add left directory: %v", err)
	}
	if strings.Contains(git(t, repo, "worktree", "list", "--porcelain"), "worktree "+bad.Path()) {
		t.Fatal("failed add left registration")
	}
	copy := r.LinkedWorktree(filepath.Join(filepath.Dir(repo), "good"))
	if err := copy.Create(ctx, "HEAD"); err != nil {
		t.Fatalf("lock not released after failed add: %v", err)
	}
	if err := copy.Remove(ctx, CaptureComplete{}); !errors.Is(err, ErrCaptureRequired) {
		t.Fatalf("remove without capture: %v", err)
	}
	if _, err := os.Stat(copy.Path()); err != nil {
		t.Fatalf("refused remove deleted directory: %v", err)
	}
	if err := copy.Remove(ctx, CompleteCapture("refs/loom/test/capture")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(copy.Path()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("remove left directory: %v", err)
	}
	if strings.Contains(git(t, repo, "worktree", "list", "--porcelain"), "worktree "+copy.Path()) {
		t.Fatal("remove left registration")
	}
}

func TestConcurrentGoroutines(t *testing.T) {
	repo, db := fixture(t)
	pool := openPool(t, db)
	r, err := pool.Admit(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			copy := r.LinkedWorktree(filepath.Join(filepath.Dir(repo), "copy-"+string(rune('a'+i))))
			errs <- copy.Create(context.Background(), "HEAD")
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := strings.Count(git(t, repo, "worktree", "list", "--porcelain"), "worktree "); got != 3 {
		t.Fatalf("registered worktrees = %d, want 3", got)
	}
}

func TestProcessHelper(t *testing.T) {
	mode := os.Getenv("POOL_HELPER")
	if mode == "" {
		return
	}
	db, repo, dest := os.Getenv("POOL_DB"), os.Getenv("POOL_REPO"), os.Getenv("POOL_DEST")
	s, err := openSQLiteRetry(db)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	r, err := New(s, gitexec.Options{GlobalConfig: filepath.Join(filepath.Dir(db), ".gitconfig")}).Admit(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if mode == "claim" {
		_, err := s.ClaimLease(context.Background(), "repo:"+r.common, "dead-process", 100*time.Millisecond)
		if err != nil {
			t.Fatal(err)
		}
		return
	}
	if err := r.LinkedWorktree(dest).Create(context.Background(), "HEAD"); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentProcesses(t *testing.T) {
	repo, db := fixture(t)
	cmds := make([]*exec.Cmd, 2)
	for i := range cmds {
		cmds[i] = exec.Command(os.Args[0], "-test.run=^TestProcessHelper$")
		cmds[i].Env = append(os.Environ(), "POOL_HELPER=1", "POOL_DB="+db, "POOL_REPO="+repo, "POOL_DEST="+filepath.Join(filepath.Dir(repo), "process-"+string(rune('a'+i))))
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, cmd := range cmds {
		wg.Add(1)
		go func(cmd *exec.Cmd) {
			defer wg.Done()
			out, err := cmd.CombinedOutput()
			if err != nil {
				errs <- errors.New(string(out) + ": " + err.Error())
			}
		}(cmd)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if got := strings.Count(git(t, repo, "worktree", "list", "--porcelain"), "worktree "); got != 3 {
		t.Fatalf("registered worktrees = %d, want 3", got)
	}
}

func TestStaleLeaseAfterProcessExit(t *testing.T) {
	repo, db := fixture(t)
	cmd := exec.Command(os.Args[0], "-test.run=^TestProcessHelper$")
	cmd.Env = append(os.Environ(), "POOL_HELPER=claim", "POOL_DB="+db, "POOL_REPO="+repo)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("lease holder: %v: %s", err, out)
	}
	s, err := journal.OpenSQLite(db)
	if err != nil {
		t.Fatal(err)
	}
	r, err := New(s, gitexec.Options{GlobalConfig: filepath.Join(filepath.Dir(db), ".gitconfig")}).Admit(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.LinkedWorktree(filepath.Join(filepath.Dir(repo), "recovered")).Create(context.Background(), "HEAD"); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
}

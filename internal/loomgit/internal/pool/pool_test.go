package pool

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:norawexec // Test fixture uses real Git for worktree operations.
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
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	lease, err := openPool(t, db).leases.ClaimLease(probeCtx, "repo:"+r.common, "failed-add-probe", time.Second)
	if err != nil {
		t.Fatalf("failed add left repository lease held: %v", err)
	}
	if err := r.pool.leases.ReleaseLease(ctx, lease); err != nil {
		t.Fatal(err)
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
	if mode == "contend" {
		trace, name := os.Getenv("POOL_TRACE"), os.Getenv("POOL_NAME")
		mark := func(event string) error {
			f, err := os.OpenFile(trace, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				return err
			}
			_, writeErr := f.WriteString(name + " " + event + "\n")
			return errors.Join(writeErr, f.Close())
		}
		if err := r.locked(context.Background(), func(context.Context) error {
			if err := mark("start"); err != nil {
				return err
			}
			time.Sleep(750 * time.Millisecond)
			return mark("end")
		}); err != nil {
			t.Fatal(err)
		}
		return
	}
	if err := r.LinkedWorktree(dest).Create(context.Background(), "HEAD"); err != nil {
		t.Fatal(err)
	}
}

func TestRepositoryLeaseSerializesProcesses(t *testing.T) {
	repo, db := fixture(t)
	trace := filepath.Join(filepath.Dir(repo), "lease-trace")
	child := func(name string) *exec.Cmd {
		cmd := exec.Command(os.Args[0], "-test.run=^TestProcessHelper$") //nolint:norawexec // Child processes verify the repository lease.
		cmd.Env = append(os.Environ(), "POOL_HELPER=contend", "POOL_DB="+db, "POOL_REPO="+repo, "POOL_TRACE="+trace, "POOL_NAME="+name)
		return cmd
	}
	first := child("first")
	if err := first.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Process.Kill(); _ = first.Wait() }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		data, err := os.ReadFile(trace)
		if err == nil && strings.Contains(string(data), "first start\n") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("first child did not acquire lease: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	second := child("second")
	if out, err := second.CombinedOutput(); err != nil {
		t.Fatalf("second child: %v: %s", err, out)
	}
	if err := first.Wait(); err != nil {
		t.Fatalf("first child: %v", err)
	}
	data, err := os.ReadFile(trace)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != "first start\nfirst end\nsecond start\nsecond end\n" {
		t.Fatalf("repository leases overlapped or were not released: %q", got)
	}
}

func TestConcurrentProcesses(t *testing.T) {
	repo, db := fixture(t)
	cmds := make([]*exec.Cmd, 2)
	for i := range cmds {
		cmds[i] = exec.Command(os.Args[0], "-test.run=^TestProcessHelper$") //nolint:norawexec // Child processes verify repository lock serialization.
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
	cmd := exec.Command(os.Args[0], "-test.run=^TestProcessHelper$") //nolint:norawexec // Child process verifies stale lease recovery.
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

func TestP118CoWTaskCopyStartsAtCleanBase(t *testing.T) {
	repo, db := fixture(t)
	ctx := context.Background()
	r, err := openPool(t, db).Admit(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "tracked"), []byte("base"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(".env\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", "tracked", ".gitignore")
	git(t, repo, "commit", "-qm", "tracked")
	base := git(t, repo, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(repo, "tracked"), []byte("dirty"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "notes.txt"), []byte("untracked"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".env"), []byte("ignored secret"), 0600); err != nil {
		t.Fatal(err)
	}
	copy := r.TaskCopy(filepath.Join(filepath.Dir(repo), "cow-copy"))
	if err := copy.Create(ctx, base); err != nil {
		t.Fatal(err)
	}
	if copy.Kind() != "cow" {
		if runtime.GOOS == "darwin" {
			t.Fatalf("APFS task copy did not use CoW: %s (%s)", copy.Kind(), copy.Reason())
		}
		t.Skipf("volume does not support CoW: %s (%s)", copy.Kind(), copy.Reason())
	}
	if got := git(t, copy.Path(), "rev-parse", "HEAD"); got != base {
		t.Fatalf("copy HEAD = %s", got)
	}
	if _, err := os.Stat(filepath.Join(copy.Path(), "notes.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("source untracked file copied: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(copy.Path(), "tracked")); err != nil || string(got) != "base" {
		t.Fatalf("task tree differs from base: %q %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(copy.Path(), ".env")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ignored source file copied: %v", err)
	}
	git(t, copy.Path(), "fsck", "--no-reflogs")
	if got := git(t, copy.Path(), "config", "remote.origin.url"); got != repo {
		t.Fatalf("origin = %q", got)
	}
	if got := git(t, copy.Path(), "config", "remote.origin.pushurl"); got != "loom-no-push://task-copy" {
		t.Fatalf("pushurl = %q", got)
	}
}

func TestP118UnsupportedCoWFallsBackToWorktree(t *testing.T) {
	repo, db := fixture(t)
	r, err := openPool(t, db).Admit(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	copy := &taskClone{repo: r, path: filepath.Join(filepath.Dir(repo), "fallback"), cloneGit: func(string, string) error { return errCoWUnsupported }}
	if err := copy.Create(context.Background(), "HEAD"); err != nil {
		t.Fatal(err)
	}
	if copy.Kind() != "worktree" || copy.Reason() == "" {
		t.Fatalf("fallback: %s %q", copy.Kind(), copy.Reason())
	}
	gitfile, err := os.ReadFile(filepath.Join(copy.Path(), ".git"))
	if err != nil || !strings.HasPrefix(string(gitfile), "gitdir: ") {
		t.Fatalf("fallback was not a linked worktree: %q %v", gitfile, err)
	}
}

func TestP118RealCloneFailureDoesNotFallBack(t *testing.T) {
	repo, db := fixture(t)
	r, err := openPool(t, db).Admit(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	copy := &taskClone{repo: r, path: filepath.Join(filepath.Dir(repo), "failed"), cloneGit: func(string, string) error { return syscall.ENOSPC }}
	if err := copy.Create(context.Background(), "HEAD"); err == nil || !errors.Is(err, syscall.ENOSPC) {
		t.Fatalf("expected disk-full error: %v", err)
	}
	if _, err := os.Stat(copy.Path()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial copy kept: %v", err)
	}
	if strings.Contains(git(t, repo, "worktree", "list", "--porcelain"), "worktree "+copy.Path()) {
		t.Fatal("real failure fell back")
	}
}

func TestP118BareSourceUsesSharedClone(t *testing.T) {
	repo, db := fixture(t)
	root := filepath.Dir(repo)
	bare := filepath.Join(root, "bare.git")
	git(t, root, "clone", "-q", "--bare", repo, bare)
	r, err := openPool(t, db).Admit(context.Background(), bare)
	if err != nil {
		t.Fatal(err)
	}
	copy := r.TaskCopy(filepath.Join(root, "shared"))
	if err := copy.Create(context.Background(), "HEAD"); err != nil {
		t.Fatal(err)
	}
	if copy.Kind() != "shared" || copy.Reason() == "" {
		t.Fatalf("shared fallback: %s %q", copy.Kind(), copy.Reason())
	}
	if got := git(t, copy.Path(), "rev-parse", "HEAD"); got != git(t, repo, "rev-parse", "HEAD") {
		t.Fatalf("shared HEAD = %s", got)
	}
}

func TestP118LinuxTmpfsFallsBackToWorktree(t *testing.T) {
	if runtime.GOOS != "linux" || os.Getenv("LOOM_P118_EXPECT_WORKTREE") != "1" {
		t.Skip("requires Linux tmpfs verification")
	}
	repo, db := fixture(t)
	r, err := openPool(t, db).Admit(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	copy := r.TaskCopy(filepath.Join(filepath.Dir(repo), "tmpfs-copy"))
	if err := copy.Create(context.Background(), "HEAD"); err != nil {
		t.Fatal(err)
	}
	if copy.Kind() != "worktree" || copy.Reason() == "" {
		t.Fatalf("tmpfs fallback = %s %q", copy.Kind(), copy.Reason())
	}
}

func TestP118CoWCopyWaitsForRepoLock(t *testing.T) {
	repo, db := fixture(t)
	r, err := openPool(t, db).Admit(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	lockDone := make(chan error, 1)
	go func() {
		lockDone <- r.WithLock(context.Background(), func(context.Context) error {
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered
	copy := r.TaskCopy(filepath.Join(filepath.Dir(repo), "waited"))
	copyDone := make(chan error, 1)
	go func() { copyDone <- copy.Create(context.Background(), "HEAD") }()
	select {
	case err := <-copyDone:
		t.Fatalf("clone bypassed repo lock: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if err := <-lockDone; err != nil {
		t.Fatal(err)
	}
	if err := <-copyDone; err != nil {
		t.Fatal(err)
	}
	if copy.Kind() != "cow" {
		t.Skipf("CoW unavailable: %s", copy.Kind())
	}
	git(t, copy.Path(), "fsck", "--no-reflogs")
}

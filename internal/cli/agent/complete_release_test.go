package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
)

// fakeReleaser wraps a MockIssueBackend with a ClaimReleaser implementation
// so we can assert ReleaseClaim is invoked with the right id.
type fakeReleaser struct {
	*clitest.MockIssueBackend

	released int32  // atomic call counter
	lastID   string // id of last ReleaseClaim invocation
	err      error  // error to return from ReleaseClaim
}

func (f *fakeReleaser) ReleaseClaim(ctx context.Context, id string) error {
	atomic.AddInt32(&f.released, 1)
	f.lastID = id
	return f.err
}

func writeLockInfo(t *testing.T, worktree string, info cli.LockInfo) {
	t.Helper()
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatalf("mkdir worktree: %v", err)
	}
	b, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		t.Fatalf("marshal lock info: %v", err)
	}
	lockPath := filepath.Join(worktree, cli.LockFileName)
	if err := os.WriteFile(lockPath, b, 0o600); err != nil {
		t.Fatalf("write lock file: %v", err)
	}
}

// TestReleaseClaimOnComplete_CallsBackend covers the happy path: lock
// present with TaskID, backend implements ClaimReleaser → ReleaseClaim is
// called with the task id.
func TestReleaseClaimOnComplete_CallsBackend(t *testing.T) {
	tmp := t.TempDir()
	writeLockInfo(t, tmp, cli.LockInfo{
		PID:       os.Getpid(),
		AgentName: "planner",
		TaskID:    "LOOM-99",
		StartedAt: time.Now(),
	})

	fake := &fakeReleaser{MockIssueBackend: clitest.NewMockIssueBackend()}
	cli.SetDefaultIssueBackend(fake)
	t.Cleanup(cli.ResetDefaultIssueBackend)

	releaseClaimOnComplete(tmp)

	if got := atomic.LoadInt32(&fake.released); got != 1 {
		t.Fatalf("ReleaseClaim call count = %d, want 1", got)
	}
	if fake.lastID != "LOOM-99" {
		t.Errorf("ReleaseClaim id = %q, want LOOM-99", fake.lastID)
	}
}

// TestReleaseClaimOnComplete_BackendNotReleaser covers the capability-gap
// case: a backend that does not implement ClaimReleaser must not crash.
func TestReleaseClaimOnComplete_BackendNotReleaser(t *testing.T) {
	tmp := t.TempDir()
	writeLockInfo(t, tmp, cli.LockInfo{
		PID:       os.Getpid(),
		AgentName: "planner",
		TaskID:    "LOOM-99",
		StartedAt: time.Now(),
	})

	plain := clitest.NewMockIssueBackend()
	cli.SetDefaultIssueBackend(plain)
	t.Cleanup(cli.ResetDefaultIssueBackend)

	// Should not panic; should not record any calls on the mock either,
	// since we type-assert before doing anything.
	releaseClaimOnComplete(tmp)

	for _, c := range plain.Calls {
		if c.Method == "ReleaseClaim" {
			t.Errorf("unexpected ReleaseClaim recorded on plain backend: %+v", c)
		}
	}
}

// TestReleaseClaimOnComplete_NoLockFile covers the missing-lock case:
// no .agent.lock present → no ReleaseClaim call, no error.
func TestReleaseClaimOnComplete_NoLockFile(t *testing.T) {
	tmp := t.TempDir()

	fake := &fakeReleaser{MockIssueBackend: clitest.NewMockIssueBackend()}
	cli.SetDefaultIssueBackend(fake)
	t.Cleanup(cli.ResetDefaultIssueBackend)

	releaseClaimOnComplete(tmp)

	if got := atomic.LoadInt32(&fake.released); got != 0 {
		t.Errorf("expected 0 ReleaseClaim calls when lock missing, got %d", got)
	}
}

// TestReleaseClaimOnComplete_EmptyTaskID covers the case where the lock
// is present but has no TaskID (agent exited before claiming).
func TestReleaseClaimOnComplete_EmptyTaskID(t *testing.T) {
	tmp := t.TempDir()
	writeLockInfo(t, tmp, cli.LockInfo{
		PID:       os.Getpid(),
		AgentName: "planner",
		TaskID:    "",
		StartedAt: time.Now(),
	})

	fake := &fakeReleaser{MockIssueBackend: clitest.NewMockIssueBackend()}
	cli.SetDefaultIssueBackend(fake)
	t.Cleanup(cli.ResetDefaultIssueBackend)

	releaseClaimOnComplete(tmp)

	if got := atomic.LoadInt32(&fake.released); got != 0 {
		t.Errorf("expected 0 ReleaseClaim calls when TaskID empty, got %d", got)
	}
}

// TestReleaseClaimOnComplete_ErrorIsLoggedNotPropagated covers the error
// path: ReleaseClaim returns an error → helper logs and returns silently
// so runComplete still writes the signal file.
func TestReleaseClaimOnComplete_ErrorIsLoggedNotPropagated(t *testing.T) {
	tmp := t.TempDir()
	writeLockInfo(t, tmp, cli.LockInfo{
		PID:       os.Getpid(),
		AgentName: "planner",
		TaskID:    "LOOM-99",
		StartedAt: time.Now(),
	})

	fake := &fakeReleaser{
		MockIssueBackend: clitest.NewMockIssueBackend(),
		err:              errors.New("simulated release failure"),
	}
	cli.SetDefaultIssueBackend(fake)
	t.Cleanup(cli.ResetDefaultIssueBackend)

	// Should not panic and should still call ReleaseClaim once.
	releaseClaimOnComplete(tmp)

	if got := atomic.LoadInt32(&fake.released); got != 1 {
		t.Fatalf("ReleaseClaim call count = %d, want 1", got)
	}
}

// TestRunComplete_WritesSignalEvenWhenReleaseFails verifies the full
// runComplete path still writes its signal file when ReleaseClaim errors.
// This is the integration-level guarantee that the LOOM-1 fix doesn't
// break the auto-mode parent's completion handshake.
func TestRunComplete_WritesSignalEvenWhenReleaseFails(t *testing.T) {
	tmp := t.TempDir()
	worktree := filepath.Join(tmp, "worktree")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatalf("mkdir worktree: %v", err)
	}
	writeLockInfo(t, worktree, cli.LockInfo{
		PID:       os.Getpid(),
		AgentName: "planner",
		TaskID:    "LOOM-99",
		StartedAt: time.Now(),
	})

	fake := &fakeReleaser{
		MockIssueBackend: clitest.NewMockIssueBackend(),
		err:              errors.New("simulated release failure"),
	}
	cli.SetDefaultIssueBackend(fake)
	t.Cleanup(cli.ResetDefaultIssueBackend)

	absPath, _ := filepath.Abs(worktree)
	resolved, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		resolved = absPath
	}

	t.Setenv("LOOM_WORKTREE_PATH", worktree)
	runComplete(nil, nil)

	signalFile := cli.GetSignalFilePath(resolved)
	t.Cleanup(func() { os.Remove(signalFile) })

	if _, err := os.Stat(signalFile); err != nil {
		t.Fatalf("signal file should still exist when ReleaseClaim fails: %v", err)
	}
	if got := atomic.LoadInt32(&fake.released); got != 1 {
		t.Errorf("ReleaseClaim call count = %d, want 1", got)
	}
}

// compile-time check: backend.ClaimReleaser is satisfied by fakeReleaser.
var _ backend.ClaimReleaser = (*fakeReleaser)(nil)

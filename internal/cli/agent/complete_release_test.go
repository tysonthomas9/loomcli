package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli"
)

// releaserStub is a MockIssueBackend that also implements backend.ClaimReleaser.
// MockIssueBackend (clitest) intentionally does not, since LOOM-1's design
// keeps the interface optional and capability-detected. This stub adds the
// method so we can assert releaseClaimOnComplete dispatches through it.
type releaserStub struct {
	*MockIssueBackend
	called    atomic.Int32
	lastID    atomic.Value // string
	lastActor atomic.Value // string
	releaseE  error
}

var _ backend.ClaimReleaser = (*releaserStub)(nil)

func newReleaserStub(releaseErr error) *releaserStub {
	s := &releaserStub{MockIssueBackend: NewMockIssueBackend(), releaseE: releaseErr}
	s.lastID.Store("")
	s.lastActor.Store("")
	return s
}

func (s *releaserStub) ReleaseClaim(_ context.Context, id, actor string) error {
	s.called.Add(1)
	s.lastID.Store(id)
	s.lastActor.Store(actor)
	return s.releaseE
}

// writeReleaseTestLock writes a synthetic .agent.lock JSON into worktreePath whose
// TaskID matches taskID. AgentName/PID are populated so cli.ReadLockFile
// parses cleanly.
func writeReleaseTestLock(t *testing.T, worktreePath, taskID string) {
	t.Helper()
	info := cli.LockInfo{
		PID:       os.Getpid(),
		Command:   "plan",
		AgentName: "test-planner",
		TaskID:    taskID,
		TaskTitle: "test task",
	}
	b, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		t.Fatalf("marshal lock: %v", err)
	}
	if err := os.WriteFile(filepath.Join(worktreePath, cli.LockFileName), b, 0600); err != nil {
		t.Fatalf("write lock: %v", err)
	}
}

func TestReleaseClaimOnComplete_DispatchesReleaseWithLockTaskID(t *testing.T) {
	stub := newReleaserStub(nil)
	cli.SetDefaultIssueBackend(stub)
	t.Cleanup(cli.ResetDefaultIssueBackend)

	wt := t.TempDir()
	writeReleaseTestLock(t, wt, "ISSUE-42")

	releaseClaimOnComplete(wt)

	if got := stub.called.Load(); got != 1 {
		t.Fatalf("ReleaseClaim call count = %d, want 1", got)
	}
	if got, _ := stub.lastID.Load().(string); got != "ISSUE-42" {
		t.Errorf("ReleaseClaim id = %q, want ISSUE-42", got)
	}
	if got, _ := stub.lastActor.Load().(string); got != "test-planner" {
		t.Errorf("ReleaseClaim actor = %q, want test-planner", got)
	}
}

func TestReleaseClaimOnComplete_NoLockFileIsNoop(t *testing.T) {
	stub := newReleaserStub(nil)
	cli.SetDefaultIssueBackend(stub)
	t.Cleanup(cli.ResetDefaultIssueBackend)

	wt := t.TempDir() // no .agent.lock written

	releaseClaimOnComplete(wt)

	if got := stub.called.Load(); got != 0 {
		t.Errorf("ReleaseClaim should not be called when lock is missing, got count=%d", got)
	}
}

func TestReleaseClaimOnComplete_EmptyTaskIDIsNoop(t *testing.T) {
	stub := newReleaserStub(nil)
	cli.SetDefaultIssueBackend(stub)
	t.Cleanup(cli.ResetDefaultIssueBackend)

	wt := t.TempDir()
	writeReleaseTestLock(t, wt, "") // lock present but TaskID empty

	releaseClaimOnComplete(wt)

	if got := stub.called.Load(); got != 0 {
		t.Errorf("ReleaseClaim should not be called when TaskID is empty, got count=%d", got)
	}
}

func TestReleaseClaimOnComplete_BackendWithoutClaimReleaserIsNoop(t *testing.T) {
	// Plain MockIssueBackend does NOT implement ClaimReleaser. Release-on-complete
	// should not try to reconstruct claim-release semantics from generic
	// Get/Update calls; actor-safe behavior belongs behind the capability.
	plain := NewMockIssueBackend()
	cli.SetDefaultIssueBackend(plain)
	t.Cleanup(cli.ResetDefaultIssueBackend)

	wt := t.TempDir()
	writeReleaseTestLock(t, wt, "ISSUE-42")

	releaseClaimOnComplete(wt)

	if plain.Called("Get") || plain.Called("Update") || plain.Called("ReleaseIssueLock") {
		t.Fatal("backend without ClaimReleaser should be a no-op")
	}
}

func TestReleaseClaimOnComplete_ReleaseErrorIsSwallowed(t *testing.T) {
	stub := newReleaserStub(errors.New("simulated release failure"))
	cli.SetDefaultIssueBackend(stub)
	t.Cleanup(cli.ResetDefaultIssueBackend)

	wt := t.TempDir()
	writeReleaseTestLock(t, wt, "ISSUE-42")

	// Returns no value — the contract is "best-effort, log on failure, never
	// propagate". This assertion guards against the helper accidentally
	// growing a return value (panic / fatal) for release failures, which
	// would block `loom complete` and defeat the LOOM-1 fix.
	releaseClaimOnComplete(wt)

	if got := stub.called.Load(); got != 1 {
		t.Fatalf("ReleaseClaim call count = %d, want 1 (release was attempted)", got)
	}
}

// TestRunComplete_WritesSignalEvenWhenReleaseFails asserts the full runComplete
// flow still writes its signal file when ReleaseClaim returns an error.
// LOOM-1's contract is "best-effort release, never block completion" — this
// integration test guards the boundary one level above releaseClaimOnComplete.
func TestRunComplete_WritesSignalEvenWhenReleaseFails(t *testing.T) {
	tmp := t.TempDir()
	worktree := filepath.Join(tmp, "worktree")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatalf("mkdir worktree: %v", err)
	}
	writeReleaseTestLock(t, worktree, "LOOM-99")

	stub := newReleaserStub(errors.New("simulated release failure"))
	cli.SetDefaultIssueBackend(stub)
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
	if got := stub.called.Load(); got != 1 {
		t.Errorf("ReleaseClaim call count = %d, want 1", got)
	}
}

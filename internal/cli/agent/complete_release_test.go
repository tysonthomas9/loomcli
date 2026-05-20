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
	called   atomic.Int32
	lastID   atomic.Value // string
	releaseE error
}

var _ backend.ClaimReleaser = (*releaserStub)(nil)

func newReleaserStub(releaseErr error) *releaserStub {
	s := &releaserStub{MockIssueBackend: NewMockIssueBackend(), releaseE: releaseErr}
	s.lastID.Store("")
	return s
}

func (s *releaserStub) ReleaseClaim(_ context.Context, id string) error {
	s.called.Add(1)
	s.lastID.Store(id)
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
	// Plain MockIssueBackend does NOT implement ClaimReleaser; the type
	// assertion in releaseClaimOnComplete must short-circuit gracefully.
	plain := NewMockIssueBackend()
	cli.SetDefaultIssueBackend(plain)
	t.Cleanup(cli.ResetDefaultIssueBackend)

	wt := t.TempDir()
	writeReleaseTestLock(t, wt, "ISSUE-42")

	// Must not panic; nothing on plain to assert other than absence of crash.
	releaseClaimOnComplete(wt)
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

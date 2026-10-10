package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// P1.26: a daemon-managed agent's `loom data` mutations of its own task go
// through the daemon (so `loom data close` gets the code-review hold and the
// label guard); other issues still go straight to the HTTP backend.
func TestDaemonAgentBackendRoutesOnlyTheAgentsOwnTaskThroughIPC(t *testing.T) {
	var viaIPC []string
	ipc := &mockIPCMutator{
		updateFn: func(id string, _ backend.UpdateParams) error { viaIPC = append(viaIPC, "update:"+id); return nil },
		claimFn:  func(id string, _ time.Duration) error { viaIPC = append(viaIPC, "claim:"+id); return nil },
		completeFn: func(id string, _ backend.CloseParams) (*backend.CloseResult, error) {
			viaIPC = append(viaIPC, "close:"+id)
			return &backend.CloseResult{}, nil
		},
		releaseLockFn: func(id string) error { viaIPC = append(viaIPC, "release-lock:"+id); return nil },
	}
	direct := NewMockIssueBackend()
	b := &ownTaskIPCBackend{IssueBackend: direct, ipc: newIPCIssueBackend(ipc, direct), ownTask: func() string { return "T-OWN" }}
	ctx := context.Background()

	for _, id := range []string{"T-OWN", "T-OTHER"} {
		if err := b.Update(ctx, id, backend.UpdateParams{}); err != nil {
			t.Fatal(err)
		}
		if err := b.ClaimIssue(ctx, id, time.Minute); err != nil {
			t.Fatal(err)
		}
		if err := b.ReleaseIssueLock(ctx, id, "agent"); err != nil {
			t.Fatal(err)
		}
		if _, err := b.Close(ctx, id, backend.CloseParams{}); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"update:T-OWN", "claim:T-OWN", "release-lock:T-OWN", "close:T-OWN"}
	if len(viaIPC) != len(want) {
		t.Fatalf("via IPC = %v, want %v", viaIPC, want)
	}
	for i := range want {
		if viaIPC[i] != want[i] {
			t.Fatalf("via IPC = %v, want %v", viaIPC, want)
		}
	}
	for _, op := range []string{"Update", "ClaimIssue", "ReleaseIssueLock", "Close"} {
		if !direct.Called(op) {
			t.Errorf("direct %s was not called for another issue", op)
		}
	}

	b.ownTask = func() string { return "" }
	viaIPC = nil
	if _, err := b.Close(ctx, "T-OWN", backend.CloseParams{}); err != nil || len(viaIPC) != 0 {
		t.Fatalf("with no own task, close went via IPC %v (%v)", viaIPC, err)
	}
}

func TestDaemonAgentBackendIsDirectOutsideDaemonSupervision(t *testing.T) {
	t.Setenv("LOOM_DAEMON_SOCKET", "")
	direct := NewMockIssueBackend()
	if got := DaemonAgentIssueBackend(direct); got != backend.IssueBackend(direct) {
		t.Fatalf("without a daemon socket the backend must be direct, got %T", got)
	}
	t.Setenv("LOOM_DAEMON_SOCKET", "/tmp/none.sock")
	if _, ok := DaemonAgentIssueBackend(direct).(*ownTaskIPCBackend); !ok {
		t.Fatal("under a daemon socket the backend must route the agent's own task through IPC")
	}
}

// The agent's own task is the one the daemon assigned it, else the one its
// worktree lock records.
func TestAgentOwnTaskPrefersTheAssignmentThenTheWorktreeLock(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	prev, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
	t.Setenv("LOOM_ASSIGNED_TASK_ID", "")
	if got := agentOwnTask(); got != "" {
		t.Fatalf("no assignment and no lock: %q", got)
	}
	if err := AcquireLock(dir, "test", "agent"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ReleaseLock(dir) })
	if err := UpdateLockTask(dir, "T-LOCK", "own task"); err != nil {
		t.Fatal(err)
	}
	if got := agentOwnTask(); got != "T-LOCK" {
		t.Fatalf("own task from lock = %q", got)
	}
	t.Setenv("LOOM_ASSIGNED_TASK_ID", "T-ASSIGNED")
	if got := agentOwnTask(); got != "T-ASSIGNED" {
		t.Fatalf("own task from assignment = %q", got)
	}
}

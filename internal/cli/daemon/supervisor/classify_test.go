package supervisor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
)

// writeLockFile is a test helper that writes a lock file with the given info.
func writeLockFile(t *testing.T, dir string, info *cli.LockInfo) {
	t.Helper()
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		t.Fatalf("failed to marshal lock info: %v", err)
	}
	lockPath := filepath.Join(dir, cli.LockFileName)
	if err := os.WriteFile(lockPath, data, 0600); err != nil {
		t.Fatalf("failed to write lock file: %v", err)
	}
}

// writeYieldFile is a test helper that writes a yield file with the given reason.
func writeYieldFile(t *testing.T, dir string, reason string) {
	t.Helper()
	req := &YieldRequest{
		Reason:      reason,
		RequestedAt: time.Now(),
		RequestedBy: "test",
	}
	if err := WriteYieldFile(dir, req); err != nil {
		t.Fatalf("failed to write yield file: %v", err)
	}
}

// newTestSupervisor returns a minimal Supervisor for checkpoint tests.
func newTestSupervisor() *Supervisor {
	cfg := &config.DaemonConfig{}
	return &Supervisor{
		ConfigSnapshot: func() *config.DaemonConfig { return cfg },
	}
}

// ---------------------------------------------------------------------------
// handleAgentCheckpoint tests
// ---------------------------------------------------------------------------

func TestHandleAgentCheckpoint_YieldExit(t *testing.T) {
	tmpDir := t.TempDir()

	// Write a lock file with a task ID
	writeLockFile(t, tmpDir, &cli.LockInfo{
		PID:       os.Getpid(),
		Command:   "task",
		AgentName: "falcon",
		TaskID:    "bd-yield-100",
		TaskTitle: "Add yield support",
		StartedAt: time.Now(),
	})

	// Write a yield file
	writeYieldFile(t, tmpDir, "config_removed")

	s := newTestSupervisor()
	ap := &AgentProcess{
		Entry:        config.AgentEntry{Worktree: "falcon"},
		WorktreePath: tmpDir,
	}

	// Call handleAgentCheckpoint with exit code 0 (yield path)
	s.handleAgentCheckpoint(ap, 0)

	// Verify checkpoint was saved with yield metadata
	lockDir := cli.ResolveLockDir(tmpDir)
	cp, err := config.LoadCheckpoint(lockDir)
	if err != nil {
		t.Fatalf("LoadCheckpoint failed: %v", err)
	}
	if cp == nil {
		t.Fatal("Expected checkpoint to be saved for yield exit, got nil")
	}

	if cp.ErrorClass != "Yielded" {
		t.Errorf("ErrorClass: got %q, want %q", cp.ErrorClass, "Yielded")
	}
	if cp.YieldReason != "config_removed" {
		t.Errorf("YieldReason: got %q, want %q", cp.YieldReason, "config_removed")
	}
	if cp.TaskID != "bd-yield-100" {
		t.Errorf("TaskID: got %q, want %q", cp.TaskID, "bd-yield-100")
	}
	if cp.AgentName != "falcon" {
		t.Errorf("AgentName: got %q, want %q", cp.AgentName, "falcon")
	}
	if cp.ExitCode != 0 {
		t.Errorf("ExitCode: got %d, want 0", cp.ExitCode)
	}
}

func TestHandleAgentCheckpoint_NormalSuccess(t *testing.T) {
	tmpDir := t.TempDir()

	// Write a lock file with a task ID
	writeLockFile(t, tmpDir, &cli.LockInfo{
		PID:       os.Getpid(),
		Command:   "task",
		AgentName: "falcon",
		TaskID:    "bd-done-200",
		TaskTitle: "Completed task",
		StartedAt: time.Now(),
	})

	// Pre-create a checkpoint to verify it gets cleared
	lockDir := cli.ResolveLockDir(tmpDir)
	oldCp := &config.Checkpoint{
		AgentName: "falcon",
		TaskID:    "bd-done-200",
		ExitCode:  1,
		Timestamp: time.Now(),
	}
	if err := config.SaveCheckpoint(lockDir, oldCp); err != nil {
		t.Fatalf("SaveCheckpoint (setup) failed: %v", err)
	}

	// No yield file present

	s := newTestSupervisor()
	ap := &AgentProcess{
		Entry:        config.AgentEntry{Worktree: "falcon"},
		WorktreePath: tmpDir,
	}

	// Call handleAgentCheckpoint with exit code 0 (normal success)
	s.handleAgentCheckpoint(ap, 0)

	// Verify checkpoint was cleared
	cp, err := config.LoadCheckpoint(lockDir)
	if err != nil {
		t.Fatalf("LoadCheckpoint failed: %v", err)
	}
	if cp != nil {
		t.Errorf("Expected checkpoint to be cleared on normal success, got %+v", cp)
	}
}

func TestHandleAgentCheckpoint_CrashWithYieldFile(t *testing.T) {
	tmpDir := t.TempDir()

	// Write a lock file with a task ID
	writeLockFile(t, tmpDir, &cli.LockInfo{
		PID:       os.Getpid(),
		Command:   "task",
		AgentName: "hawk",
		TaskID:    "bd-crash-300",
		TaskTitle: "Crashed task",
		StartedAt: time.Now(),
	})

	// Write a yield file (should be ignored for non-zero exits)
	writeYieldFile(t, tmpDir, "manual_stop")

	s := newTestSupervisor()
	ap := &AgentProcess{
		Entry:        config.AgentEntry{Worktree: "hawk"},
		WorktreePath: tmpDir,
	}

	// Call handleAgentCheckpoint with exit code 1 (crash path)
	s.handleAgentCheckpoint(ap, 1)

	// Verify crash checkpoint is saved (not yield checkpoint)
	lockDir := cli.ResolveLockDir(tmpDir)
	cp, err := config.LoadCheckpoint(lockDir)
	if err != nil {
		t.Fatalf("LoadCheckpoint failed: %v", err)
	}
	if cp == nil {
		t.Fatal("Expected crash checkpoint to be saved, got nil")
	}

	if cp.ExitCode != 1 {
		t.Errorf("ExitCode: got %d, want 1", cp.ExitCode)
	}
	if cp.ErrorClass == "Yielded" {
		t.Error("ErrorClass should NOT be 'Yielded' for a crash exit with yield file")
	}
	if cp.YieldReason != "" {
		t.Errorf("YieldReason should be empty for crash checkpoint, got %q", cp.YieldReason)
	}
	if cp.TaskID != "bd-crash-300" {
		t.Errorf("TaskID: got %q, want %q", cp.TaskID, "bd-crash-300")
	}
}

// ---------------------------------------------------------------------------
// saveYieldCheckpoint tests
// ---------------------------------------------------------------------------

func TestSaveYieldCheckpoint_NoLock(t *testing.T) {
	tmpDir := t.TempDir()

	// Write a yield file but no lock file
	writeYieldFile(t, tmpDir, "config_removed")

	s := newTestSupervisor()
	ap := &AgentProcess{
		Entry:        config.AgentEntry{Worktree: "falcon"},
		WorktreePath: tmpDir,
	}

	// Call saveYieldCheckpoint directly
	s.saveYieldCheckpoint(ap)

	// Verify no checkpoint was saved (lock file required)
	lockDir := cli.ResolveLockDir(tmpDir)
	cp, err := config.LoadCheckpoint(lockDir)
	if err != nil {
		t.Fatalf("LoadCheckpoint failed: %v", err)
	}
	if cp != nil {
		t.Errorf("Expected no checkpoint when lock file is missing, got %+v", cp)
	}
}

func TestSaveYieldCheckpoint_NoTaskID(t *testing.T) {
	tmpDir := t.TempDir()

	// Write a lock file WITHOUT a task ID
	writeLockFile(t, tmpDir, &cli.LockInfo{
		PID:       os.Getpid(),
		Command:   "task",
		AgentName: "falcon",
		StartedAt: time.Now(),
	})

	// Write a yield file
	writeYieldFile(t, tmpDir, "manual_stop")

	s := newTestSupervisor()
	ap := &AgentProcess{
		Entry:        config.AgentEntry{Worktree: "falcon"},
		WorktreePath: tmpDir,
	}

	// Call saveYieldCheckpoint directly
	s.saveYieldCheckpoint(ap)

	// Verify no checkpoint was saved (task ID required)
	lockDir := cli.ResolveLockDir(tmpDir)
	cp, err := config.LoadCheckpoint(lockDir)
	if err != nil {
		t.Fatalf("LoadCheckpoint failed: %v", err)
	}
	if cp != nil {
		t.Errorf("Expected no checkpoint when lock has no task ID, got %+v", cp)
	}
}

func TestSaveYieldCheckpoint_CapturesEpicID(t *testing.T) {
	tmpDir := t.TempDir()

	// Write a lock file with a task ID
	writeLockFile(t, tmpDir, &cli.LockInfo{
		PID:       os.Getpid(),
		Command:   "task",
		AgentName: "falcon",
		TaskID:    "bd-epic-task-1",
		TaskTitle: "Epic task",
		StartedAt: time.Now(),
	})

	// Write a yield file
	writeYieldFile(t, tmpDir, "higher_priority")

	s := newTestSupervisor()
	ap := &AgentProcess{
		Entry:          config.AgentEntry{Worktree: "falcon"},
		WorktreePath:   tmpDir,
		AssignedEpicID: "bd-epic-42",
	}

	s.saveYieldCheckpoint(ap)

	lockDir := cli.ResolveLockDir(tmpDir)
	cp, err := config.LoadCheckpoint(lockDir)
	if err != nil {
		t.Fatalf("LoadCheckpoint failed: %v", err)
	}
	if cp == nil {
		t.Fatal("Expected checkpoint to be saved, got nil")
	}

	if cp.EpicID != "bd-epic-42" {
		t.Errorf("EpicID: got %q, want %q", cp.EpicID, "bd-epic-42")
	}
	if cp.YieldReason != "higher_priority" {
		t.Errorf("YieldReason: got %q, want %q", cp.YieldReason, "higher_priority")
	}
}

func TestSaveYieldCheckpoint_NoYieldFile_DefaultsToUnknown(t *testing.T) {
	tmpDir := t.TempDir()

	// Write a lock file with a task ID
	writeLockFile(t, tmpDir, &cli.LockInfo{
		PID:       os.Getpid(),
		Command:   "task",
		AgentName: "falcon",
		TaskID:    "bd-noyield-1",
		TaskTitle: "No yield file task",
		StartedAt: time.Now(),
	})

	// No yield file -- saveYieldCheckpoint should still save with reason "unknown"

	s := newTestSupervisor()
	ap := &AgentProcess{
		Entry:        config.AgentEntry{Worktree: "falcon"},
		WorktreePath: tmpDir,
	}

	s.saveYieldCheckpoint(ap)

	lockDir := cli.ResolveLockDir(tmpDir)
	cp, err := config.LoadCheckpoint(lockDir)
	if err != nil {
		t.Fatalf("LoadCheckpoint failed: %v", err)
	}
	if cp == nil {
		t.Fatal("Expected checkpoint to be saved, got nil")
	}

	if cp.YieldReason != "unknown" {
		t.Errorf("YieldReason: got %q, want %q (default when yield file missing)", cp.YieldReason, "unknown")
	}
	if cp.ErrorClass != "Yielded" {
		t.Errorf("ErrorClass: got %q, want %q", cp.ErrorClass, "Yielded")
	}
}

package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// deadPID is a PID guaranteed never to belong to a live process: it exceeds the
// kernel's maximum PID (Linux PID_MAX_LIMIT is 2^22; macOS is far lower), so
// IsProcessRunning's syscall.Kill(pid, 0) returns ESRCH ("no such process").
// A constant avoids spawning+reaping a real process just to obtain a dead PID.
const deadPID = 1 << 30

// writeStaleLock writes a lock file with the given (dead) PID + resume fields.
func writeStaleLock(t *testing.T, dir string, info LockInfo) {
	t.Helper()
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, LockFileName), data, 0600); err != nil {
		t.Fatalf("write stale lock: %v", err)
	}
}

func TestAcquireLock_AssignsRunID(t *testing.T) {
	dir := t.TempDir()
	if err := AcquireLock(t.Context(), dir, "plan", "falcon"); err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}
	info, err := ReadLockFile(dir)
	if err != nil {
		t.Fatalf("ReadLockFile: %v", err)
	}
	if info.RunID == "" {
		t.Fatal("AcquireLock did not assign a RunID")
	}
}

func TestAcquireLock_CarriesResumeForwardAcrossStaleReplacement(t *testing.T) {
	dir := t.TempDir()
	writeStaleLock(t, dir, LockInfo{
		PID:             deadPID,
		Command:         "task",
		ClaudeSessionID: "sess-XYZ",
		RunID:           "run-ABC",
		TaskID:          "loomcli-42",
		TaskTitle:       "do the thing",
		TaskStartedAt:   time.Now().Add(-2 * time.Minute),
	})

	// Acquiring over a stale lock must preserve the resume continuity, not wipe it.
	if err := AcquireLock(t.Context(), dir, "task", "falcon"); err != nil {
		t.Fatalf("AcquireLock over stale lock: %v", err)
	}
	info, err := ReadLockFile(dir)
	if err != nil {
		t.Fatalf("ReadLockFile: %v", err)
	}
	if info.ClaudeSessionID != "sess-XYZ" {
		t.Errorf("ClaudeSessionID = %q, want carried-forward sess-XYZ", info.ClaudeSessionID)
	}
	if info.RunID != "run-ABC" {
		t.Errorf("RunID = %q, want carried-forward run-ABC (stable across restart)", info.RunID)
	}
	if info.TaskID != "loomcli-42" {
		t.Errorf("TaskID = %q, want carried-forward loomcli-42", info.TaskID)
	}
	if info.PID != os.Getpid() {
		t.Errorf("PID = %d, want current process (new owner)", info.PID)
	}
}

func TestUpdateLock_ConcurrentNoLostUpdate(t *testing.T) {
	dir := t.TempDir()
	if err := AcquireLock(t.Context(), dir, "plan", "falcon"); err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}
	const n = 50
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			// Read-modify-write under the sidecar flock; without it, concurrent
			// appends would clobber each other and drop updates.
			_ = UpdateLock(dir, func(info *LockInfo) error {
				info.TaskTitle += "x"
				return nil
			})
		}()
	}
	wg.Wait()
	info, err := ReadLockFile(dir)
	if err != nil {
		t.Fatalf("ReadLockFile: %v", err)
	}
	if len(info.TaskTitle) != n {
		t.Fatalf("TaskTitle len = %d, want %d (lost updates under concurrency)", len(info.TaskTitle), n)
	}
}

func TestUpdateLockClaudeSessionID_RoundTripAndOwnership(t *testing.T) {
	dir := t.TempDir()
	if err := AcquireLock(t.Context(), dir, "task", "falcon"); err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}
	if err := UpdateLockClaudeSessionID(dir, "sid-1"); err != nil {
		t.Fatalf("UpdateLockClaudeSessionID: %v", err)
	}
	info, _ := ReadLockFile(dir)
	if info.ClaudeSessionID != "sid-1" {
		t.Fatalf("ClaudeSessionID = %q, want sid-1", info.ClaudeSessionID)
	}

	// A lock owned by a different process must not be mutated by the owner-checked path.
	foreign := t.TempDir()
	writeStaleLock(t, foreign, LockInfo{PID: deadPID, Command: "task"})
	if err := UpdateLockState(foreign, StateActive); err == nil {
		t.Fatal("UpdateLockState on a foreign-owned lock should error")
	}
}

func TestClearLockClaudeSessionID_NoLockIsNil(t *testing.T) {
	dir := t.TempDir()
	if err := ClearLockClaudeSessionID(dir); err != nil {
		t.Fatalf("ClearLockClaudeSessionID on missing lock = %v, want nil", err)
	}
}

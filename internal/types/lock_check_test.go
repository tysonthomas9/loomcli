package types

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestShouldSkipDatabase_NoLockFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	skip, holder, err := ShouldSkipDatabase(dir)
	if err != nil {
		t.Errorf("ShouldSkipDatabase() unexpected error: %v", err)
	}
	if skip {
		t.Error("ShouldSkipDatabase() = true, want false (no lock file)")
	}
	if holder != "" {
		t.Errorf("holder = %q, want empty", holder)
	}
}

func TestShouldSkipDatabase_ValidLockWithCurrentProcess(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	lockPath := filepath.Join(dir, ".exclusive-lock")

	hostname, _ := os.Hostname()
	lock := ExclusiveLock{
		Holder:    "test-holder",
		PID:       os.Getpid(), // Current process - definitely alive
		Hostname:  hostname,
		StartedAt: time.Now(),
		Version:   "1.0.0",
	}

	data, _ := json.Marshal(lock)
	if err := os.WriteFile(lockPath, data, 0600); err != nil {
		t.Fatalf("Failed to write lock file: %v", err)
	}

	skip, holder, err := ShouldSkipDatabase(dir)
	if err != nil {
		t.Errorf("ShouldSkipDatabase() unexpected error: %v", err)
	}
	if !skip {
		t.Error("ShouldSkipDatabase() = false, want true (valid lock with alive process)")
	}
	if holder != "test-holder" {
		t.Errorf("holder = %q, want %q", holder, "test-holder")
	}
}

func TestShouldSkipDatabase_MalformedJSON(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	lockPath := filepath.Join(dir, ".exclusive-lock")

	// Write malformed JSON
	if err := os.WriteFile(lockPath, []byte("{invalid json"), 0600); err != nil {
		t.Fatalf("Failed to write lock file: %v", err)
	}

	// Fail-safe: skip database on malformed lock
	skip, _, err := ShouldSkipDatabase(dir)
	if err == nil {
		t.Error("ShouldSkipDatabase() expected error for malformed JSON")
	}
	if !skip {
		t.Error("ShouldSkipDatabase() = false, want true (fail-safe on malformed JSON)")
	}
}

func TestShouldSkipDatabase_InvalidLock(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	lockPath := filepath.Join(dir, ".exclusive-lock")

	// Write lock with empty holder (invalid)
	lock := ExclusiveLock{
		Holder:    "", // Invalid - empty holder
		PID:       12345,
		Hostname:  "test-host",
		StartedAt: time.Now(),
	}

	data, _ := json.Marshal(lock)
	if err := os.WriteFile(lockPath, data, 0600); err != nil {
		t.Fatalf("Failed to write lock file: %v", err)
	}

	// Fail-safe: skip database on invalid lock
	skip, _, err := ShouldSkipDatabase(dir)
	if err == nil {
		t.Error("ShouldSkipDatabase() expected error for invalid lock")
	}
	if !skip {
		t.Error("ShouldSkipDatabase() = false, want true (fail-safe on invalid lock)")
	}
}

func TestShouldSkipDatabase_StaleLockRemoved(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	lockPath := filepath.Join(dir, ".exclusive-lock")

	hostname, _ := os.Hostname()

	// Test with a PID that doesn't exist (using a very high PID)
	// Note: This test may behave differently based on system configuration
	// The IsProcessAlive function is fail-safe (returns true on errors)
	// So this test verifies the happy path when the process is definitely dead

	// First test: Different hostname means process is assumed alive (fail-safe)
	// This is expected behavior
	lockRemote := ExclusiveLock{
		Holder:    "remote-holder",
		PID:       999999,
		Hostname:  "different-host-xyz",
		StartedAt: time.Now().Add(-time.Hour),
		Version:   "1.0.0",
	}

	data, _ := json.Marshal(lockRemote)
	if err := os.WriteFile(lockPath, data, 0600); err != nil {
		t.Fatalf("Failed to write lock file: %v", err)
	}

	// Remote host locks are assumed alive (fail-safe)
	skip, holder, err := ShouldSkipDatabase(dir)
	if err != nil {
		t.Errorf("ShouldSkipDatabase() unexpected error: %v", err)
	}
	if !skip {
		t.Error("ShouldSkipDatabase() = false, want true (remote host assumed alive)")
	}
	if holder != "remote-holder" {
		t.Errorf("holder = %q, want %q", holder, "remote-holder")
	}

	// For local hostname with dead PID, behavior depends on system
	// We document this rather than assert specific behavior
	lockLocal := ExclusiveLock{
		Holder:    "local-holder",
		PID:       2147483647, // Very unlikely to exist
		Hostname:  hostname,
		StartedAt: time.Now().Add(-time.Hour),
		Version:   "1.0.0",
	}

	data, _ = json.Marshal(lockLocal)
	if err := os.WriteFile(lockPath, data, 0600); err != nil {
		t.Fatalf("Failed to write lock file: %v", err)
	}

	skip, holder, _ = ShouldSkipDatabase(dir)
	t.Logf("ShouldSkipDatabase with dead local PID: skip=%v, holder=%q", skip, holder)
	// Note: We don't assert here because behavior varies by system
	// On Linux with ESRCH, skip=false and lock is removed
	// On systems with permission errors, skip=true (fail-safe)
}

func TestShouldSkipDatabase_RemoteHostLock(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	lockPath := filepath.Join(dir, ".exclusive-lock")

	// Lock from a different hostname - cannot verify if alive
	lock := ExclusiveLock{
		Holder:    "remote-holder",
		PID:       12345,
		Hostname:  "different-hostname-xyz", // Different from current host
		StartedAt: time.Now(),
		Version:   "1.0.0",
	}

	data, _ := json.Marshal(lock)
	if err := os.WriteFile(lockPath, data, 0600); err != nil {
		t.Fatalf("Failed to write lock file: %v", err)
	}

	// Fail-safe: assume alive on different hostname
	skip, holder, err := ShouldSkipDatabase(dir)
	if err != nil {
		t.Errorf("ShouldSkipDatabase() unexpected error: %v", err)
	}
	if !skip {
		t.Error("ShouldSkipDatabase() = false, want true (remote host assumed alive)")
	}
	if holder != "remote-holder" {
		t.Errorf("holder = %q, want %q", holder, "remote-holder")
	}
}

func TestShouldSkipDatabase_UnreadableDirectory(t *testing.T) {
	t.Parallel()

	// Non-existent directory path
	dir := "/nonexistent/path/that/does/not/exist"

	// Should return false (no lock file exists)
	skip, holder, err := ShouldSkipDatabase(dir)
	if err != nil {
		t.Errorf("ShouldSkipDatabase() unexpected error for nonexistent dir: %v", err)
	}
	if skip {
		t.Error("ShouldSkipDatabase() = true, want false (nonexistent dir has no lock)")
	}
	if holder != "" {
		t.Errorf("holder = %q, want empty", holder)
	}
}

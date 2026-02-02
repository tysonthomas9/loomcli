package types

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/lockfile"
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

func TestShouldSkipDatabase_FlockHeld(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	lockPath := filepath.Join(dir, ".exclusive-lock")

	hostname, _ := os.Hostname()
	lock := ExclusiveLock{
		Holder:    "test-holder",
		PID:       os.Getpid(),
		Hostname:  hostname,
		StartedAt: time.Now(),
		Version:   "1.0.0",
	}

	data, _ := json.Marshal(lock)
	if err := os.WriteFile(lockPath, data, 0600); err != nil {
		t.Fatalf("Failed to write lock file: %v", err)
	}

	// Hold the flock to simulate an active lock holder
	f, err := os.OpenFile(lockPath, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("Failed to open lock file: %v", err)
	}
	defer f.Close()

	if err := lockfile.FlockExclusiveBlocking(f); err != nil {
		t.Fatalf("Failed to acquire flock: %v", err)
	}
	defer func() { _ = lockfile.FlockUnlock(f) }()

	// Now test ShouldSkipDatabase - it should detect the lock is held
	skip, holder, err := ShouldSkipDatabase(dir)
	if err != nil {
		t.Errorf("ShouldSkipDatabase() unexpected error: %v", err)
	}
	if !skip {
		t.Error("ShouldSkipDatabase() = false, want true (flock is held)")
	}
	if holder != "test-holder" {
		t.Errorf("holder = %q, want %q", holder, "test-holder")
	}
}

func TestShouldSkipDatabase_StaleLockRemoved(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	lockPath := filepath.Join(dir, ".exclusive-lock")

	hostname, _ := os.Hostname()
	lock := ExclusiveLock{
		Holder:    "stale-holder",
		PID:       999999999, // Non-existent PID
		Hostname:  hostname,
		StartedAt: time.Now().Add(-time.Hour),
		Version:   "1.0.0",
	}

	data, _ := json.Marshal(lock)
	if err := os.WriteFile(lockPath, data, 0600); err != nil {
		t.Fatalf("Failed to write lock file: %v", err)
	}

	// No flock held - lock file exists but nobody holds the flock
	// ShouldSkipDatabase should detect this as stale and remove it
	skip, holder, err := ShouldSkipDatabase(dir)
	if err != nil {
		t.Errorf("ShouldSkipDatabase() unexpected error: %v", err)
	}
	if skip {
		t.Error("ShouldSkipDatabase() = true, want false (stale lock should be removed)")
	}
	if holder != "stale-holder" {
		t.Errorf("holder = %q, want %q (should report previous holder)", holder, "stale-holder")
	}

	// Verify lock file was removed
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Error("Lock file should have been removed")
	}
}

func TestShouldSkipDatabase_MalformedContentRemoved(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	lockPath := filepath.Join(dir, ".exclusive-lock")

	// Write malformed JSON - no flock held
	if err := os.WriteFile(lockPath, []byte("{invalid json"), 0600); err != nil {
		t.Fatalf("Failed to write lock file: %v", err)
	}

	// No flock held, so file should be considered stale and removed
	// Even though content is malformed, we can still proceed
	skip, holder, err := ShouldSkipDatabase(dir)
	if err != nil {
		t.Errorf("ShouldSkipDatabase() unexpected error: %v", err)
	}
	if skip {
		t.Error("ShouldSkipDatabase() = true, want false (stale malformed lock should be removed)")
	}
	if holder != "" {
		t.Errorf("holder = %q, want empty (malformed JSON cannot provide holder)", holder)
	}

	// Verify lock file was removed
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Error("Lock file should have been removed")
	}
}

func TestShouldSkipDatabase_FlockHeldMalformedContent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	lockPath := filepath.Join(dir, ".exclusive-lock")

	// Write malformed JSON
	if err := os.WriteFile(lockPath, []byte("{invalid json"), 0600); err != nil {
		t.Fatalf("Failed to write lock file: %v", err)
	}

	// Hold the flock even though content is malformed
	f, err := os.OpenFile(lockPath, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("Failed to open lock file: %v", err)
	}
	defer f.Close()

	if err := lockfile.FlockExclusiveBlocking(f); err != nil {
		t.Fatalf("Failed to acquire flock: %v", err)
	}
	defer func() { _ = lockfile.FlockUnlock(f) }()

	// Flock is held, so we should skip even if content is malformed
	skip, holder, err := ShouldSkipDatabase(dir)
	if err != nil {
		t.Errorf("ShouldSkipDatabase() unexpected error: %v", err)
	}
	if !skip {
		t.Error("ShouldSkipDatabase() = false, want true (flock is held)")
	}
	if holder != "" {
		t.Errorf("holder = %q, want empty (malformed JSON cannot provide holder)", holder)
	}
}

func TestShouldSkipDatabase_EmptyFileStaleRemoved(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	lockPath := filepath.Join(dir, ".exclusive-lock")

	// Write empty file - no flock held
	if err := os.WriteFile(lockPath, []byte{}, 0600); err != nil {
		t.Fatalf("Failed to write lock file: %v", err)
	}

	// No flock held, so file should be considered stale and removed
	skip, holder, err := ShouldSkipDatabase(dir)
	if err != nil {
		t.Errorf("ShouldSkipDatabase() unexpected error: %v", err)
	}
	if skip {
		t.Error("ShouldSkipDatabase() = true, want false (stale empty lock should be removed)")
	}
	if holder != "" {
		t.Errorf("holder = %q, want empty", holder)
	}

	// Verify lock file was removed
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Error("Lock file should have been removed")
	}
}

func TestShouldSkipDatabase_NonexistentDirectory(t *testing.T) {
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

func TestShouldSkipDatabase_ConcurrentFlockCheck(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	lockPath := filepath.Join(dir, ".exclusive-lock")

	hostname, _ := os.Hostname()
	lock := ExclusiveLock{
		Holder:    "concurrent-holder",
		PID:       os.Getpid(),
		Hostname:  hostname,
		StartedAt: time.Now(),
		Version:   "1.0.0",
	}

	data, _ := json.Marshal(lock)
	if err := os.WriteFile(lockPath, data, 0600); err != nil {
		t.Fatalf("Failed to write lock file: %v", err)
	}

	// Hold the flock in a goroutine
	lockReady := make(chan struct{})
	testDone := make(chan struct{})
	goroutineDone := make(chan struct{})

	f, err := os.OpenFile(lockPath, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("Failed to open lock file: %v", err)
	}

	go func() {
		defer close(goroutineDone)
		if err := lockfile.FlockExclusiveBlocking(f); err != nil {
			t.Errorf("Failed to acquire flock: %v", err)
			return
		}
		defer func() { _ = lockfile.FlockUnlock(f) }()

		close(lockReady)

		select {
		case <-testDone:
		case <-time.After(5 * time.Second):
		}
	}()

	// Wait for lock to be held
	<-lockReady

	// Multiple concurrent checks should all see the lock as held
	for i := 0; i < 3; i++ {
		skip, holder, err := ShouldSkipDatabase(dir)
		if err != nil {
			t.Errorf("Iteration %d: ShouldSkipDatabase() unexpected error: %v", i, err)
		}
		if !skip {
			t.Errorf("Iteration %d: ShouldSkipDatabase() = false, want true", i)
		}
		if holder != "concurrent-holder" {
			t.Errorf("Iteration %d: holder = %q, want %q", i, holder, "concurrent-holder")
		}
	}

	// Cleanup
	close(testDone)
	<-goroutineDone
	f.Close()
}

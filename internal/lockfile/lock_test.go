package lockfile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

// TestTryDaemonLock tests the TryDaemonLock function for various scenarios
func TestTryDaemonLock(t *testing.T) {
	t.Run("no lock file exists, no PID file", func(t *testing.T) {
		dir := t.TempDir()

		running, pid := TryDaemonLock(dir)
		if running {
			t.Error("expected running=false when no files exist")
		}
		if pid != 0 {
			t.Errorf("expected pid=0 when no files exist, got %d", pid)
		}
	})

	t.Run("no lock file, valid PID file with running process", func(t *testing.T) {
		dir := t.TempDir()

		// Write current process PID (known to be running)
		currentPID := os.Getpid()
		pidFile := filepath.Join(dir, "daemon.pid")
		if err := os.WriteFile(pidFile, []byte(strconv.Itoa(currentPID)), 0644); err != nil {
			t.Fatal(err)
		}

		running, pid := TryDaemonLock(dir)
		if !running {
			t.Error("expected running=true when PID file points to running process")
		}
		if pid != currentPID {
			t.Errorf("expected pid=%d, got %d", currentPID, pid)
		}
	})

	t.Run("no lock file, PID file with dead process", func(t *testing.T) {
		dir := t.TempDir()

		// Write a PID that doesn't exist
		pidFile := filepath.Join(dir, "daemon.pid")
		if err := os.WriteFile(pidFile, []byte("999999999"), 0644); err != nil {
			t.Fatal(err)
		}

		running, pid := TryDaemonLock(dir)
		if running {
			t.Error("expected running=false when PID file points to dead process")
		}
		if pid != 0 {
			t.Errorf("expected pid=0 when process is dead, got %d", pid)
		}
	})

	t.Run("no lock file, PID file with invalid content", func(t *testing.T) {
		dir := t.TempDir()

		pidFile := filepath.Join(dir, "daemon.pid")
		if err := os.WriteFile(pidFile, []byte("not-a-number"), 0644); err != nil {
			t.Fatal(err)
		}

		running, pid := TryDaemonLock(dir)
		if running {
			t.Error("expected running=false when PID file has invalid content")
		}
		if pid != 0 {
			t.Errorf("expected pid=0 when PID file is invalid, got %d", pid)
		}
	})

	t.Run("lock file exists, lock NOT held, JSON content", func(t *testing.T) {
		dir := t.TempDir()

		// Create lock file with valid JSON but don't hold the lock
		lockPath := filepath.Join(dir, "daemon.lock")
		lockInfo := LockInfo{
			PID:       12345,
			Database:  "test.db",
			Version:   "1.0",
			StartedAt: time.Now(),
		}
		data, err := json.Marshal(lockInfo)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(lockPath, data, 0644); err != nil {
			t.Fatal(err)
		}

		running, pid := TryDaemonLock(dir)
		if running {
			t.Error("expected running=false when lock file exists but not held")
		}
		if pid != 0 {
			t.Errorf("expected pid=0 when lock not held, got %d", pid)
		}
	})

	t.Run("lock file exists, lock held by another goroutine, JSON content", func(t *testing.T) {
		dir := t.TempDir()
		lockPath := filepath.Join(dir, "daemon.lock")

		// Create lock file with JSON content
		currentPID := os.Getpid()
		lockInfo := LockInfo{
			PID:       currentPID,
			Database:  "test.db",
			Version:   "1.0",
			StartedAt: time.Now(),
		}
		data, err := json.Marshal(lockInfo)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(lockPath, data, 0644); err != nil {
			t.Fatal(err)
		}

		// Hold the lock in another goroutine
		lockFile, err := os.OpenFile(lockPath, os.O_RDWR, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer lockFile.Close()

		if err := FlockExclusiveBlocking(lockFile); err != nil {
			t.Fatalf("failed to acquire lock: %v", err)
		}
		defer func() { _ = FlockUnlock(lockFile) }()

		running, pid := TryDaemonLock(dir)
		if !running {
			t.Error("expected running=true when lock is held")
		}
		if pid != currentPID {
			t.Errorf("expected pid=%d, got %d", currentPID, pid)
		}
	})

	t.Run("lock file exists, lock held, old format plain integer PID", func(t *testing.T) {
		dir := t.TempDir()
		lockPath := filepath.Join(dir, "daemon.lock")

		currentPID := os.Getpid()
		// Write plain integer PID (old format)
		if err := os.WriteFile(lockPath, []byte(strconv.Itoa(currentPID)), 0644); err != nil {
			t.Fatal(err)
		}

		// Hold the lock
		lockFile, err := os.OpenFile(lockPath, os.O_RDWR, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer lockFile.Close()

		if err := FlockExclusiveBlocking(lockFile); err != nil {
			t.Fatalf("failed to acquire lock: %v", err)
		}
		defer func() { _ = FlockUnlock(lockFile) }()

		running, pid := TryDaemonLock(dir)
		if !running {
			t.Error("expected running=true when lock is held")
		}
		if pid != currentPID {
			t.Errorf("expected pid=%d (from old format), got %d", currentPID, pid)
		}
	})

	t.Run("lock file exists, lock held, unreadable content falls back to PID file", func(t *testing.T) {
		dir := t.TempDir()
		lockPath := filepath.Join(dir, "daemon.lock")

		// Write garbage content
		if err := os.WriteFile(lockPath, []byte("garbage-not-json-not-number"), 0644); err != nil {
			t.Fatal(err)
		}

		// Create PID file with running process
		currentPID := os.Getpid()
		pidFile := filepath.Join(dir, "daemon.pid")
		if err := os.WriteFile(pidFile, []byte(strconv.Itoa(currentPID)), 0644); err != nil {
			t.Fatal(err)
		}

		// Hold the lock
		lockFile, err := os.OpenFile(lockPath, os.O_RDWR, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer lockFile.Close()

		if err := FlockExclusiveBlocking(lockFile); err != nil {
			t.Fatalf("failed to acquire lock: %v", err)
		}
		defer func() { _ = FlockUnlock(lockFile) }()

		running, pid := TryDaemonLock(dir)
		if !running {
			t.Error("expected running=true when lock is held")
		}
		if pid != currentPID {
			t.Errorf("expected pid=%d (from PID file fallback), got %d", currentPID, pid)
		}
	})

	t.Run("lock file exists but empty, not locked", func(t *testing.T) {
		dir := t.TempDir()
		lockPath := filepath.Join(dir, "daemon.lock")

		// Create empty lock file
		if err := os.WriteFile(lockPath, []byte{}, 0644); err != nil {
			t.Fatal(err)
		}

		running, pid := TryDaemonLock(dir)
		if running {
			t.Error("expected running=false when lock file is empty and not locked")
		}
		if pid != 0 {
			t.Errorf("expected pid=0, got %d", pid)
		}
	})
}

// TestReadLockInfo tests the ReadLockInfo function
func TestReadLockInfo(t *testing.T) {
	t.Run("valid JSON lock file", func(t *testing.T) {
		dir := t.TempDir()
		lockPath := filepath.Join(dir, "daemon.lock")

		now := time.Now().Truncate(time.Second)
		lockInfo := LockInfo{
			PID:       12345,
			ParentPID: 1000,
			Database:  "/path/to/test.db",
			Version:   "2.0.0",
			StartedAt: now,
		}
		data, err := json.Marshal(lockInfo)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(lockPath, data, 0644); err != nil {
			t.Fatal(err)
		}

		info, err := ReadLockInfo(dir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if info.PID != 12345 {
			t.Errorf("expected PID=12345, got %d", info.PID)
		}
		if info.ParentPID != 1000 {
			t.Errorf("expected ParentPID=1000, got %d", info.ParentPID)
		}
		if info.Database != "/path/to/test.db" {
			t.Errorf("expected Database=/path/to/test.db, got %s", info.Database)
		}
		if info.Version != "2.0.0" {
			t.Errorf("expected Version=2.0.0, got %s", info.Version)
		}
		if !info.StartedAt.Equal(now) {
			t.Errorf("expected StartedAt=%v, got %v", now, info.StartedAt)
		}
	})

	t.Run("lock file doesn't exist", func(t *testing.T) {
		dir := t.TempDir()

		_, err := ReadLockInfo(dir)
		if err == nil {
			t.Error("expected error when lock file doesn't exist")
		}
		if !os.IsNotExist(err) {
			t.Errorf("expected os.IsNotExist error, got: %v", err)
		}
	})

	t.Run("invalid JSON content", func(t *testing.T) {
		dir := t.TempDir()
		lockPath := filepath.Join(dir, "daemon.lock")

		if err := os.WriteFile(lockPath, []byte("{invalid json}"), 0644); err != nil {
			t.Fatal(err)
		}

		_, err := ReadLockInfo(dir)
		if err == nil {
			t.Error("expected error for invalid JSON")
		}
	})

	t.Run("old format plain integer PID", func(t *testing.T) {
		dir := t.TempDir()
		lockPath := filepath.Join(dir, "daemon.lock")

		if err := os.WriteFile(lockPath, []byte("54321"), 0644); err != nil {
			t.Fatal(err)
		}

		info, err := ReadLockInfo(dir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if info.PID != 54321 {
			t.Errorf("expected PID=54321 from old format, got %d", info.PID)
		}
	})

	t.Run("old format with trailing whitespace", func(t *testing.T) {
		dir := t.TempDir()
		lockPath := filepath.Join(dir, "daemon.lock")

		if err := os.WriteFile(lockPath, []byte("54321\n  "), 0644); err != nil {
			t.Fatal(err)
		}

		info, err := ReadLockInfo(dir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if info.PID != 54321 {
			t.Errorf("expected PID=54321 from old format with whitespace, got %d", info.PID)
		}
	})

	t.Run("old format with leading whitespace", func(t *testing.T) {
		dir := t.TempDir()
		lockPath := filepath.Join(dir, "daemon.lock")

		// fmt.Sscanf handles leading whitespace
		if err := os.WriteFile(lockPath, []byte("  54321"), 0644); err != nil {
			t.Fatal(err)
		}

		info, err := ReadLockInfo(dir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if info.PID != 54321 {
			t.Errorf("expected PID=54321 from old format with leading whitespace, got %d", info.PID)
		}
	})

	t.Run("old format with non-numeric content", func(t *testing.T) {
		dir := t.TempDir()
		lockPath := filepath.Join(dir, "daemon.lock")

		if err := os.WriteFile(lockPath, []byte("not-a-pid"), 0644); err != nil {
			t.Fatal(err)
		}

		_, err := ReadLockInfo(dir)
		if err == nil {
			t.Error("expected error for non-numeric content")
		}
	})

	t.Run("empty file", func(t *testing.T) {
		dir := t.TempDir()
		lockPath := filepath.Join(dir, "daemon.lock")

		if err := os.WriteFile(lockPath, []byte{}, 0644); err != nil {
			t.Fatal(err)
		}

		_, err := ReadLockInfo(dir)
		if err == nil {
			t.Error("expected error for empty file")
		}
	})

	t.Run("zero PID in JSON", func(t *testing.T) {
		dir := t.TempDir()
		lockPath := filepath.Join(dir, "daemon.lock")

		lockInfo := LockInfo{
			PID:      0,
			Database: "test.db",
		}
		data, err := json.Marshal(lockInfo)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(lockPath, data, 0644); err != nil {
			t.Fatal(err)
		}

		info, err := ReadLockInfo(dir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Note: no validation for PID > 0 as documented in code review
		if info.PID != 0 {
			t.Errorf("expected PID=0 (no validation), got %d", info.PID)
		}
	})
}

// TestCheckPIDFile tests the checkPIDFile function indirectly via TryDaemonLock
func TestCheckPIDFile(t *testing.T) {
	t.Run("PID file doesn't exist", func(t *testing.T) {
		dir := t.TempDir()

		// No lock file, no PID file - will go through checkPIDFile
		running, pid := TryDaemonLock(dir)
		if running {
			t.Error("expected running=false when no PID file")
		}
		if pid != 0 {
			t.Errorf("expected pid=0, got %d", pid)
		}
	})

	t.Run("PID file with running PID", func(t *testing.T) {
		dir := t.TempDir()

		currentPID := os.Getpid()
		pidFile := filepath.Join(dir, "daemon.pid")
		if err := os.WriteFile(pidFile, []byte(strconv.Itoa(currentPID)), 0644); err != nil {
			t.Fatal(err)
		}

		running, pid := TryDaemonLock(dir)
		if !running {
			t.Error("expected running=true when PID is alive")
		}
		if pid != currentPID {
			t.Errorf("expected pid=%d, got %d", currentPID, pid)
		}
	})

	t.Run("PID file with dead PID", func(t *testing.T) {
		dir := t.TempDir()

		pidFile := filepath.Join(dir, "daemon.pid")
		if err := os.WriteFile(pidFile, []byte("999999999"), 0644); err != nil {
			t.Fatal(err)
		}

		running, pid := TryDaemonLock(dir)
		if running {
			t.Error("expected running=false when PID is dead")
		}
		if pid != 0 {
			t.Errorf("expected pid=0, got %d", pid)
		}
	})

	t.Run("PID file with non-numeric content", func(t *testing.T) {
		dir := t.TempDir()

		pidFile := filepath.Join(dir, "daemon.pid")
		if err := os.WriteFile(pidFile, []byte("abc123"), 0644); err != nil {
			t.Fatal(err)
		}

		running, pid := TryDaemonLock(dir)
		if running {
			t.Error("expected running=false for non-numeric PID")
		}
		if pid != 0 {
			t.Errorf("expected pid=0, got %d", pid)
		}
	})

	t.Run("PID file with extra whitespace", func(t *testing.T) {
		dir := t.TempDir()

		currentPID := os.Getpid()
		pidFile := filepath.Join(dir, "daemon.pid")
		if err := os.WriteFile(pidFile, []byte("  "+strconv.Itoa(currentPID)+"  \n"), 0644); err != nil {
			t.Fatal(err)
		}

		running, pid := TryDaemonLock(dir)
		if !running {
			t.Error("expected running=true when PID is alive (with whitespace)")
		}
		if pid != currentPID {
			t.Errorf("expected pid=%d, got %d", currentPID, pid)
		}
	})

	t.Run("PID file with whitespace-only content", func(t *testing.T) {
		dir := t.TempDir()

		pidFile := filepath.Join(dir, "daemon.pid")
		if err := os.WriteFile(pidFile, []byte("   \n  "), 0644); err != nil {
			t.Fatal(err)
		}

		running, pid := TryDaemonLock(dir)
		if running {
			t.Error("expected running=false for whitespace-only PID file")
		}
		if pid != 0 {
			t.Errorf("expected pid=0, got %d", pid)
		}
	})
}

// TestIsProcessRunning tests the isProcessRunning function
func TestIsProcessRunning(t *testing.T) {
	t.Run("current process is running", func(t *testing.T) {
		if !isProcessRunning(os.Getpid()) {
			t.Error("expected current process to be running")
		}
	})

	t.Run("very high PID is not running", func(t *testing.T) {
		if isProcessRunning(999999999) {
			t.Error("expected PID 999999999 to not be running")
		}
	})

	t.Run("PID 0 is not running", func(t *testing.T) {
		// PID 0 has special semantics on Unix (signals to process group)
		if isProcessRunning(0) {
			t.Error("expected PID 0 to return false (special case)")
		}
	})

	t.Run("negative PID is not running", func(t *testing.T) {
		// Negative PIDs have special semantics on Unix
		if isProcessRunning(-1) {
			t.Error("expected negative PID to return false")
		}
	})
}

// TestFlockFunctions tests the flock wrapper functions
func TestFlockFunctions(t *testing.T) {
	t.Run("acquire exclusive lock, release, re-acquire", func(t *testing.T) {
		dir := t.TempDir()
		lockPath := filepath.Join(dir, "test.lock")

		// Create lock file
		if err := os.WriteFile(lockPath, []byte("test"), 0644); err != nil {
			t.Fatal(err)
		}

		f, err := os.OpenFile(lockPath, os.O_RDWR, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()

		// Acquire lock
		if err := FlockExclusiveBlocking(f); err != nil {
			t.Fatalf("failed to acquire lock: %v", err)
		}

		// Release lock
		if err := FlockUnlock(f); err != nil {
			t.Fatalf("failed to release lock: %v", err)
		}

		// Re-acquire lock
		if err := FlockExclusiveBlocking(f); err != nil {
			t.Fatalf("failed to re-acquire lock: %v", err)
		}

		// Final release
		if err := FlockUnlock(f); err != nil {
			t.Fatalf("failed to release lock: %v", err)
		}
	})

	t.Run("unlock without prior lock does not panic", func(t *testing.T) {
		dir := t.TempDir()
		lockPath := filepath.Join(dir, "test.lock")

		if err := os.WriteFile(lockPath, []byte("test"), 0644); err != nil {
			t.Fatal(err)
		}

		f, err := os.OpenFile(lockPath, os.O_RDWR, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()

		// Unlock without prior lock should not panic
		// The error behavior varies by OS, so we just verify no panic
		_ = FlockUnlock(f)
	})

	t.Run("non-blocking lock detects held lock", func(t *testing.T) {
		dir := t.TempDir()
		lockPath := filepath.Join(dir, "test.lock")

		if err := os.WriteFile(lockPath, []byte("test"), 0644); err != nil {
			t.Fatal(err)
		}

		// Open and hold lock
		f1, err := os.OpenFile(lockPath, os.O_RDWR, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer f1.Close()

		if err := FlockExclusiveBlocking(f1); err != nil {
			t.Fatalf("failed to acquire lock: %v", err)
		}
		defer func() { _ = FlockUnlock(f1) }()

		// Try non-blocking lock from different file descriptor
		f2, err := os.OpenFile(lockPath, os.O_RDWR, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer f2.Close()

		err = FlockExclusiveNonBlocking(f2)
		if err != ErrLockHeld {
			t.Errorf("expected ErrLockHeld, got: %v", err)
		}
	})
}

// TestConcurrentAccess tests concurrent access patterns
func TestConcurrentAccess(t *testing.T) {
	t.Run("goroutine holds lock, TryDaemonLock detects running", func(t *testing.T) {
		dir := t.TempDir()
		lockPath := filepath.Join(dir, "daemon.lock")

		currentPID := os.Getpid()
		lockInfo := LockInfo{
			PID:       currentPID,
			Database:  "test.db",
			Version:   "1.0",
			StartedAt: time.Now(),
		}
		data, err := json.Marshal(lockInfo)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(lockPath, data, 0644); err != nil {
			t.Fatal(err)
		}

		// Hold lock in goroutine
		lockFile, err := os.OpenFile(lockPath, os.O_RDWR, 0)
		if err != nil {
			t.Fatal(err)
		}

		lockReady := make(chan struct{})
		testDone := make(chan struct{})
		goroutineDone := make(chan struct{})

		go func() {
			defer close(goroutineDone)
			if err := FlockExclusiveBlocking(lockFile); err != nil {
				t.Errorf("goroutine failed to acquire lock: %v", err)
				return
			}
			defer func() { _ = FlockUnlock(lockFile) }()

			close(lockReady)

			// Hold lock until test signals completion or timeout
			select {
			case <-testDone:
			case <-time.After(5 * time.Second):
			}
		}()

		// Wait for lock to be held
		<-lockReady

		// TryDaemonLock should detect the daemon as running
		running, pid := TryDaemonLock(dir)
		if !running {
			t.Error("expected running=true when lock is held")
		}
		if pid != currentPID {
			t.Errorf("expected pid=%d, got %d", currentPID, pid)
		}

		// Signal goroutine to exit and wait for it to complete before closing file
		close(testDone)
		<-goroutineDone
		lockFile.Close()
	})

	t.Run("concurrent TryDaemonLock calls don't crash or hang", func(t *testing.T) {
		dir := t.TempDir()
		lockPath := filepath.Join(dir, "daemon.lock")

		// Create empty lock file
		if err := os.WriteFile(lockPath, []byte{}, 0644); err != nil {
			t.Fatal(err)
		}

		const numGoroutines = 10
		var wg sync.WaitGroup

		// Just verify concurrent calls complete without crashing or hanging
		// Note: Some may see running=true briefly while another goroutine's
		// TryDaemonLock is in progress (TOCTOU race), which is expected behavior
		for i := 0; i < numGoroutines; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, _ = TryDaemonLock(dir)
			}()
		}

		// Use a timeout to detect hangs
		done := make(chan struct{})
		go func() {
			wg.Wait()
			close(done)
		}()

		select {
		case <-done:
			// Success - all goroutines completed
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent TryDaemonLock calls timed out")
		}
	})
}

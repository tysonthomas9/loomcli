package lockfile

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestReadLockInfo(t *testing.T) {
	tmpDir := t.TempDir()

	t.Run("JSON format", func(t *testing.T) {
		lockPath := filepath.Join(tmpDir, "daemon.lock")
		lockInfo := &LockInfo{
			PID:       12345,
			ParentPID: 1,
			Database:  "/path/to/db",
			Version:   "1.0.0",
			StartedAt: time.Now(),
		}

		data, err := json.Marshal(lockInfo)
		if err != nil {
			t.Fatalf("failed to marshal lock info: %v", err)
		}

		if err := os.WriteFile(lockPath, data, 0644); err != nil {
			t.Fatalf("failed to write lock file: %v", err)
		}

		result, err := ReadLockInfo(tmpDir)
		if err != nil {
			t.Fatalf("ReadLockInfo failed: %v", err)
		}

		if result.PID != lockInfo.PID {
			t.Errorf("PID mismatch: got %d, want %d", result.PID, lockInfo.PID)
		}

		if result.Database != lockInfo.Database {
			t.Errorf("Database mismatch: got %s, want %s", result.Database, lockInfo.Database)
		}

		if result.ParentPID != lockInfo.ParentPID {
			t.Errorf("ParentPID mismatch: got %d, want %d", result.ParentPID, lockInfo.ParentPID)
		}

		if result.Version != lockInfo.Version {
			t.Errorf("Version mismatch: got %s, want %s", result.Version, lockInfo.Version)
		}

		if result.StartedAt.IsZero() {
			t.Error("StartedAt should not be zero")
		}
	})

	t.Run("file not found", func(t *testing.T) {
		nonExistentDir := filepath.Join(tmpDir, "nonexistent")
		_, err := ReadLockInfo(nonExistentDir)
		if err == nil {
			t.Error("expected error for non-existent file")
		}
	})

	t.Run("invalid format", func(t *testing.T) {
		lockPath := filepath.Join(tmpDir, "daemon.lock")
		if err := os.WriteFile(lockPath, []byte("invalid json"), 0644); err != nil {
			t.Fatalf("failed to write lock file: %v", err)
		}

		_, err := ReadLockInfo(tmpDir)
		if err == nil {
			t.Error("expected error for invalid format")
		}
	})

	t.Run("empty file", func(t *testing.T) {
		lockPath := filepath.Join(tmpDir, "daemon.lock")
		if err := os.WriteFile(lockPath, []byte(""), 0644); err != nil {
			t.Fatalf("failed to write lock file: %v", err)
		}

		_, err := ReadLockInfo(tmpDir)
		if err == nil {
			t.Error("expected error for empty file")
		}
	})

	t.Run("INT_MAX PID in JSON", func(t *testing.T) {
		lockPath := filepath.Join(tmpDir, "daemon.lock")
		lockInfo := &LockInfo{
			PID:      math.MaxInt32,
			Database: "/path/to/db",
			Version:  "1.0.0",
		}
		data, err := json.Marshal(lockInfo)
		if err != nil {
			t.Fatalf("failed to marshal lock info: %v", err)
		}
		if err := os.WriteFile(lockPath, data, 0644); err != nil {
			t.Fatalf("failed to write lock file: %v", err)
		}

		result, err := ReadLockInfo(tmpDir)
		if err != nil {
			t.Fatalf("ReadLockInfo failed: %v", err)
		}

		if result.PID != math.MaxInt32 {
			t.Errorf("PID mismatch: got %d, want %d", result.PID, math.MaxInt32)
		}
	})

	t.Run("zero PID in JSON", func(t *testing.T) {
		// Documents that ReadLockInfo does not validate PID > 0
		// (finding from code review loomcli-6fl.3)
		lockPath := filepath.Join(tmpDir, "daemon.lock")
		lockInfo := &LockInfo{
			PID:      0,
			Database: "/path/to/db",
			Version:  "1.0.0",
		}
		data, err := json.Marshal(lockInfo)
		if err != nil {
			t.Fatalf("failed to marshal lock info: %v", err)
		}
		if err := os.WriteFile(lockPath, data, 0644); err != nil {
			t.Fatalf("failed to write lock file: %v", err)
		}

		result, err := ReadLockInfo(tmpDir)
		if err != nil {
			t.Fatalf("ReadLockInfo failed: %v", err)
		}

		if result.PID != 0 {
			t.Errorf("PID mismatch: got %d, want 0", result.PID)
		}
	})
}

func TestTryDaemonLock(t *testing.T) {
	t.Run("no lock file exists", func(t *testing.T) {
		tmpDir := t.TempDir()

		running, pid := TryDaemonLock(tmpDir)
		if running {
			t.Error("expected running=false when no lock file exists")
		}
		if pid != 0 {
			t.Errorf("expected pid=0, got %d", pid)
		}
	})

	t.Run("lock file exists but not locked - daemon not running", func(t *testing.T) {
		tmpDir := t.TempDir()
		lockPath := filepath.Join(tmpDir, "daemon.lock")

		lockInfo := LockInfo{
			PID:       12345,
			Database:  "/path/to/db",
			Version:   "1.0.0",
			StartedAt: time.Now(),
		}
		data, err := json.Marshal(lockInfo)
		if err != nil {
			t.Fatalf("failed to marshal lock info: %v", err)
		}
		if err := os.WriteFile(lockPath, data, 0644); err != nil {
			t.Fatalf("failed to write lock file: %v", err)
		}

		running, _ := TryDaemonLock(tmpDir)
		if running {
			t.Error("expected running=false when lock file exists but is not locked")
		}
	})

	t.Run("lock file held by another process - daemon running", func(t *testing.T) {
		tmpDir := t.TempDir()
		lockPath := filepath.Join(tmpDir, "daemon.lock")

		lockInfo := LockInfo{
			PID:       os.Getpid(),
			Database:  "/path/to/db",
			Version:   "1.0.0",
			StartedAt: time.Now(),
		}
		data, err := json.Marshal(lockInfo)
		if err != nil {
			t.Fatalf("failed to marshal lock info: %v", err)
		}
		if err := os.WriteFile(lockPath, data, 0644); err != nil {
			t.Fatalf("failed to write lock file: %v", err)
		}

		f, err := os.OpenFile(lockPath, os.O_RDWR, 0644)
		if err != nil {
			t.Fatalf("failed to open lock file: %v", err)
		}
		defer f.Close()

		if err := FlockExclusiveBlocking(f); err != nil {
			t.Fatalf("failed to acquire lock: %v", err)
		}
		defer FlockUnlock(f)

		running, pid := TryDaemonLock(tmpDir)
		if !running {
			t.Error("expected running=true when lock is held")
		}
		if pid != os.Getpid() {
			t.Errorf("expected pid=%d, got %d", os.Getpid(), pid)
		}
	})

	t.Run("held lock with invalid content reports running without PID", func(t *testing.T) {
		tmpDir := t.TempDir()
		lockPath := filepath.Join(tmpDir, "daemon.lock")

		if err := os.WriteFile(lockPath, []byte("invalid content"), 0644); err != nil {
			t.Fatalf("failed to write lock file: %v", err)
		}

		f, err := os.OpenFile(lockPath, os.O_RDWR, 0644)
		if err != nil {
			t.Fatalf("failed to open lock file: %v", err)
		}
		defer f.Close()

		if err := FlockExclusiveBlocking(f); err != nil {
			t.Fatalf("failed to acquire lock: %v", err)
		}
		defer FlockUnlock(f)

		running, pid := TryDaemonLock(tmpDir)
		if !running {
			t.Error("expected running=true when lock is held")
		}
		if pid != 0 {
			t.Errorf("expected pid=0 when lock content is invalid, got %d", pid)
		}
	})

	t.Run("empty lock file not locked", func(t *testing.T) {
		tmpDir := t.TempDir()
		lockPath := filepath.Join(tmpDir, "daemon.lock")

		if err := os.WriteFile(lockPath, []byte(""), 0644); err != nil {
			t.Fatalf("failed to write lock file: %v", err)
		}

		running, pid := TryDaemonLock(tmpDir)
		if running {
			t.Error("expected running=false when lock file is empty and not locked")
		}
		if pid != 0 {
			t.Errorf("expected pid=0, got %d", pid)
		}
	})
}

func TestFlockFunctions(t *testing.T) {
	t.Run("FlockExclusiveBlocking and FlockUnlock", func(t *testing.T) {
		tmpDir := t.TempDir()
		lockPath := filepath.Join(tmpDir, "test.lock")

		if err := os.WriteFile(lockPath, []byte("test"), 0644); err != nil {
			t.Fatalf("failed to create lock file: %v", err)
		}

		f, err := os.OpenFile(lockPath, os.O_RDWR, 0644)
		if err != nil {
			t.Fatalf("failed to open lock file: %v", err)
		}
		defer f.Close()

		if err := FlockExclusiveBlocking(f); err != nil {
			t.Errorf("FlockExclusiveBlocking failed: %v", err)
		}

		if err := FlockUnlock(f); err != nil {
			t.Errorf("FlockUnlock failed: %v", err)
		}
	})

	t.Run("flockExclusive non-blocking succeeds on unlocked file", func(t *testing.T) {
		tmpDir := t.TempDir()
		lockPath := filepath.Join(tmpDir, "test.lock")

		if err := os.WriteFile(lockPath, []byte("test"), 0644); err != nil {
			t.Fatalf("failed to create lock file: %v", err)
		}

		f, err := os.OpenFile(lockPath, os.O_RDWR, 0644)
		if err != nil {
			t.Fatalf("failed to open lock file: %v", err)
		}
		defer f.Close()

		if err := flockExclusive(f); err != nil {
			t.Errorf("flockExclusive should succeed on unlocked file: %v", err)
		}

		FlockUnlock(f)
	})

	t.Run("FlockUnlock without prior lock does not panic", func(t *testing.T) {
		tmpDir := t.TempDir()
		lockPath := filepath.Join(tmpDir, "test.lock")

		if err := os.WriteFile(lockPath, []byte("test"), 0644); err != nil {
			t.Fatalf("failed to create lock file: %v", err)
		}

		f, err := os.OpenFile(lockPath, os.O_RDWR, 0644)
		if err != nil {
			t.Fatalf("failed to open lock file: %v", err)
		}
		defer f.Close()

		// Should not panic or return a fatal error
		_ = FlockUnlock(f)
	})

	t.Run("flockExclusive returns errDaemonLocked when already locked", func(t *testing.T) {
		tmpDir := t.TempDir()
		lockPath := filepath.Join(tmpDir, "test.lock")

		if err := os.WriteFile(lockPath, []byte("test"), 0644); err != nil {
			t.Fatalf("failed to create lock file: %v", err)
		}

		f1, err := os.OpenFile(lockPath, os.O_RDWR, 0644)
		if err != nil {
			t.Fatalf("failed to open lock file: %v", err)
		}
		defer f1.Close()

		if err := FlockExclusiveBlocking(f1); err != nil {
			t.Fatalf("failed to acquire first lock: %v", err)
		}
		defer FlockUnlock(f1)

		f2, err := os.OpenFile(lockPath, os.O_RDWR, 0644)
		if err != nil {
			t.Fatalf("failed to open second lock file handle: %v", err)
		}
		defer f2.Close()

		err = flockExclusive(f2)
		if err != errDaemonLocked {
			t.Errorf("expected errDaemonLocked, got %v", err)
		}
	})
}

func TestIsProcessRunning(t *testing.T) {
	t.Run("current process is running", func(t *testing.T) {
		if !IsProcessRunning(os.Getpid()) {
			t.Error("expected current process to be running")
		}
	})

	t.Run("non-existent process is not running", func(t *testing.T) {
		if IsProcessRunning(99999) {
			t.Error("expected non-existent process to not be running")
		}
	})

	t.Run("parent process is running", func(t *testing.T) {
		ppid := os.Getppid()
		if ppid > 0 && !IsProcessRunning(ppid) {
			t.Error("expected parent process to be running")
		}
	})

	t.Run("very high PID is not running", func(t *testing.T) {
		if IsProcessRunning(999999999) {
			t.Error("expected PID 999999999 to not be running")
		}
	})

	t.Run("PID at math.MaxInt32", func(t *testing.T) {
		if IsProcessRunning(math.MaxInt32) {
			t.Errorf("expected PID %d (math.MaxInt32) to not be running", math.MaxInt32)
		}
	})

	t.Run("PID 0 is not running", func(t *testing.T) {
		if IsProcessRunning(0) {
			t.Error("expected PID 0 to not be running")
		}
	})

	t.Run("negative PID is not running", func(t *testing.T) {
		if IsProcessRunning(-1) {
			t.Error("expected PID -1 to not be running")
		}
	})
}

func TestTryLockExclusive_Success(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "test.lock")

	f, err := os.Create(lockPath)
	if err != nil {
		t.Fatalf("failed to create lock file: %v", err)
	}
	defer f.Close()

	if err := TryLockExclusive(f); err != nil {
		t.Fatalf("TryLockExclusive should succeed on a new file, got: %v", err)
	}

	// Clean up: release the lock
	if err := FlockUnlock(f); err != nil {
		t.Fatalf("FlockUnlock failed: %v", err)
	}
}

func TestTryLockExclusive_AlreadyLocked(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "test.lock")

	if err := os.WriteFile(lockPath, []byte("lock"), 0644); err != nil {
		t.Fatalf("failed to create lock file: %v", err)
	}

	// Open the first file descriptor and acquire the exclusive lock.
	f1, err := os.OpenFile(lockPath, os.O_RDWR, 0644)
	if err != nil {
		t.Fatalf("failed to open first fd: %v", err)
	}
	defer f1.Close()

	if err := TryLockExclusive(f1); err != nil {
		t.Fatalf("TryLockExclusive on first fd should succeed, got: %v", err)
	}
	defer FlockUnlock(f1)

	// Open a second file descriptor and attempt to lock the same file.
	f2, err := os.OpenFile(lockPath, os.O_RDWR, 0644)
	if err != nil {
		t.Fatalf("failed to open second fd: %v", err)
	}
	defer f2.Close()

	err = TryLockExclusive(f2)
	if err == nil {
		t.Fatal("TryLockExclusive on second fd should fail when lock is already held")
	}
	if err != ErrLocked {
		t.Fatalf("expected ErrLocked, got: %v", err)
	}
}

func TestTryLockExclusive_ReleasedAfterClose(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "test.lock")

	if err := os.WriteFile(lockPath, []byte("lock"), 0644); err != nil {
		t.Fatalf("failed to create lock file: %v", err)
	}

	// Acquire lock on a file descriptor, then close it to release the lock.
	func() {
		f, err := os.OpenFile(lockPath, os.O_RDWR, 0644)
		if err != nil {
			t.Fatalf("failed to open lock file: %v", err)
		}

		if err := TryLockExclusive(f); err != nil {
			f.Close()
			t.Fatalf("TryLockExclusive should succeed, got: %v", err)
		}

		// Closing the file descriptor should release the flock.
		f.Close()
	}()

	// Open a new file descriptor and verify the lock can be re-acquired.
	f2, err := os.OpenFile(lockPath, os.O_RDWR, 0644)
	if err != nil {
		t.Fatalf("failed to reopen lock file: %v", err)
	}
	defer f2.Close()

	if err := TryLockExclusive(f2); err != nil {
		t.Fatalf("TryLockExclusive should succeed after previous fd was closed, got: %v", err)
	}

	if err := FlockUnlock(f2); err != nil {
		t.Fatalf("FlockUnlock failed: %v", err)
	}
}

func TestErrLocked_IsExported(t *testing.T) {
	t.Run("ErrLocked is not nil", func(t *testing.T) {
		if ErrLocked == nil {
			t.Fatal("ErrLocked should not be nil")
		}
	})

	t.Run("ErrLocked matches errDaemonLocked", func(t *testing.T) {
		// ErrLocked is set to errDaemonLocked; verify they are the same value.
		if ErrLocked != errDaemonLocked {
			t.Fatalf("ErrLocked (%v) should be identical to errDaemonLocked (%v)", ErrLocked, errDaemonLocked)
		}
	})

	t.Run("ErrLocked has expected message", func(t *testing.T) {
		expected := "daemon lock already held by another process"
		if ErrLocked.Error() != expected {
			t.Fatalf("ErrLocked.Error() = %q, want %q", ErrLocked.Error(), expected)
		}
	})

	t.Run("TryLockExclusive returns ErrLocked on contention", func(t *testing.T) {
		tmpDir := t.TempDir()
		lockPath := filepath.Join(tmpDir, "test.lock")

		if err := os.WriteFile(lockPath, []byte("lock"), 0644); err != nil {
			t.Fatalf("failed to create lock file: %v", err)
		}

		f1, err := os.OpenFile(lockPath, os.O_RDWR, 0644)
		if err != nil {
			t.Fatalf("failed to open first fd: %v", err)
		}
		defer f1.Close()

		if err := TryLockExclusive(f1); err != nil {
			t.Fatalf("first TryLockExclusive should succeed: %v", err)
		}
		defer FlockUnlock(f1)

		f2, err := os.OpenFile(lockPath, os.O_RDWR, 0644)
		if err != nil {
			t.Fatalf("failed to open second fd: %v", err)
		}
		defer f2.Close()

		lockErr := TryLockExclusive(f2)
		if lockErr != ErrLocked {
			t.Fatalf("expected TryLockExclusive to return ErrLocked, got: %v", lockErr)
		}
	})
}

func TestConcurrentLockAccess(t *testing.T) {
	t.Run("goroutine holds flock while another calls TryDaemonLock", func(t *testing.T) {
		tmpDir := t.TempDir()
		lockPath := filepath.Join(tmpDir, "daemon.lock")

		currentPID := os.Getpid()
		lockInfo := LockInfo{
			PID:       currentPID,
			Database:  "/path/to/db",
			Version:   "1.0.0",
			StartedAt: time.Now(),
		}
		data, err := json.Marshal(lockInfo)
		if err != nil {
			t.Fatalf("failed to marshal lock info: %v", err)
		}
		if err := os.WriteFile(lockPath, data, 0644); err != nil {
			t.Fatalf("failed to write lock file: %v", err)
		}

		// Acquire lock in a goroutine, hold it while another goroutine probes
		f, err := os.OpenFile(lockPath, os.O_RDWR, 0644)
		if err != nil {
			t.Fatalf("failed to open lock file: %v", err)
		}
		defer f.Close()

		if err := FlockExclusiveBlocking(f); err != nil {
			t.Fatalf("failed to acquire lock: %v", err)
		}
		defer FlockUnlock(f)

		// Multiple concurrent probes should all see daemon as running
		var wg sync.WaitGroup
		const numProbes = 5
		results := make([]bool, numProbes)
		pids := make([]int, numProbes)

		for i := 0; i < numProbes; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				results[idx], pids[idx] = TryDaemonLock(tmpDir)
			}(i)
		}

		wg.Wait()

		for i := 0; i < numProbes; i++ {
			if !results[i] {
				t.Errorf("probe %d: expected running=true, got false", i)
			}
			if pids[i] != currentPID {
				t.Errorf("probe %d: expected pid=%d, got %d", i, currentPID, pids[i])
			}
		}
	})

	t.Run("concurrent TryDaemonLock with no lock held", func(t *testing.T) {
		tmpDir := t.TempDir()
		lockPath := filepath.Join(tmpDir, "daemon.lock")

		// Create lock file but don't hold the lock
		if err := os.WriteFile(lockPath, []byte(`{"pid":12345}`), 0644); err != nil {
			t.Fatalf("failed to write lock file: %v", err)
		}

		// Run probes serially — concurrent probes interfere because
		// TryDaemonLock briefly acquires the flock to test availability,
		// causing other goroutines to see it as held.
		const numProbes = 5
		for i := 0; i < numProbes; i++ {
			running, _ := TryDaemonLock(tmpDir)
			if running {
				t.Errorf("probe %d: expected running=false when lock is not held", i)
			}
		}
	})
}

func TestNFSLocking(t *testing.T) {
	nfsPath := os.Getenv("LOCKFILE_TEST_NFS_PATH")
	if nfsPath == "" {
		t.Skip("LOCKFILE_TEST_NFS_PATH not set — skipping NFS integration tests")
	}

	t.Run("TryDaemonLock on NFS", func(t *testing.T) {
		testDir := filepath.Join(nfsPath, "nfs_lock_test")
		if err := os.MkdirAll(testDir, 0755); err != nil {
			t.Fatalf("failed to create NFS test dir: %v", err)
		}
		defer os.RemoveAll(testDir)

		lockPath := filepath.Join(testDir, "daemon.lock")
		lockInfo := LockInfo{
			PID:       os.Getpid(),
			Database:  "/path/to/db",
			Version:   "1.0.0",
			StartedAt: time.Now(),
		}
		data, err := json.Marshal(lockInfo)
		if err != nil {
			t.Fatalf("failed to marshal lock info: %v", err)
		}
		if err := os.WriteFile(lockPath, data, 0644); err != nil {
			t.Fatalf("failed to write lock file on NFS: %v", err)
		}

		// Hold flock, then probe
		f, err := os.OpenFile(lockPath, os.O_RDWR, 0644)
		if err != nil {
			t.Fatalf("failed to open lock file on NFS: %v", err)
		}
		defer f.Close()

		if err := FlockExclusiveBlocking(f); err != nil {
			t.Logf("NFS flock acquisition failed (may be expected on NFS v2/v3): %v", err)
			return
		}
		defer FlockUnlock(f)

		running, pid := TryDaemonLock(testDir)
		t.Logf("NFS TryDaemonLock result: running=%v, pid=%d", running, pid)
		if !running {
			t.Log("WARNING: flock may not work on this NFS mount — lock not detected as held")
		}
	})

	t.Run("concurrent lock on NFS", func(t *testing.T) {
		testDir := filepath.Join(nfsPath, "nfs_concurrent_test")
		if err := os.MkdirAll(testDir, 0755); err != nil {
			t.Fatalf("failed to create NFS test dir: %v", err)
		}
		defer os.RemoveAll(testDir)

		lockPath := filepath.Join(testDir, "daemon.lock")
		if err := os.WriteFile(lockPath, []byte("test"), 0644); err != nil {
			t.Fatalf("failed to write lock file on NFS: %v", err)
		}

		// Acquire lock on first fd
		f1, err := os.OpenFile(lockPath, os.O_RDWR, 0644)
		if err != nil {
			t.Fatalf("failed to open first fd on NFS: %v", err)
		}
		defer f1.Close()

		if err := FlockExclusiveBlocking(f1); err != nil {
			t.Logf("NFS flock acquisition failed: %v", err)
			return
		}
		defer FlockUnlock(f1)

		// Try non-blocking lock on second fd
		f2, err := os.OpenFile(lockPath, os.O_RDWR, 0644)
		if err != nil {
			t.Fatalf("failed to open second fd on NFS: %v", err)
		}
		defer f2.Close()

		err = flockExclusive(f2)
		if err == errDaemonLocked {
			t.Log("NFS flock exclusion works correctly — second lock attempt was blocked")
		} else if err == nil {
			t.Log("WARNING: NFS flock did NOT provide exclusion — second lock succeeded (known NFS v2/v3 limitation)")
			FlockUnlock(f2)
		} else {
			t.Logf("NFS flock returned unexpected error: %v", err)
		}
	})

	t.Run("FlockExclusiveBlocking on NFS", func(t *testing.T) {
		testDir := filepath.Join(nfsPath, "nfs_blocking_test")
		if err := os.MkdirAll(testDir, 0755); err != nil {
			t.Fatalf("failed to create NFS test dir: %v", err)
		}
		defer os.RemoveAll(testDir)

		lockPath := filepath.Join(testDir, "daemon.lock")
		if err := os.WriteFile(lockPath, []byte("test"), 0644); err != nil {
			t.Fatalf("failed to write lock file on NFS: %v", err)
		}

		f, err := os.OpenFile(lockPath, os.O_RDWR, 0644)
		if err != nil {
			t.Fatalf("failed to open lock file on NFS: %v", err)
		}
		defer f.Close()

		if err := FlockExclusiveBlocking(f); err != nil {
			t.Logf("FlockExclusiveBlocking failed on NFS (may be expected): %v", err)
			return
		}
		t.Log("FlockExclusiveBlocking succeeded on NFS")

		if err := FlockUnlock(f); err != nil {
			t.Logf("FlockUnlock failed on NFS: %v", err)
		}
	})
}

package lockfile

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestConfigLock_AcquireRelease(t *testing.T) {
	dir := t.TempDir()

	unlock, err := ConfigLock(dir)
	if err != nil {
		t.Fatalf("ConfigLock: %v", err)
	}

	lockPath := filepath.Join(dir, ConfigLockFileName)
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("lock file should exist: %v", err)
	}

	unlock()

	// Re-acquire should succeed immediately after release.
	unlock2, err := ConfigLock(dir)
	if err != nil {
		t.Fatalf("re-acquire ConfigLock: %v", err)
	}
	unlock2()
}

func TestConfigLock_CreatesDirectory(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "subdir", "nested")

	unlock, err := ConfigLock(dir)
	if err != nil {
		t.Fatalf("ConfigLock: %v", err)
	}
	defer unlock()

	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("directory should be created: %v", err)
	}
	lockPath := filepath.Join(dir, ConfigLockFileName)
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("lock file should exist: %v", err)
	}
}

func TestConfigLock_EmptyDir(t *testing.T) {
	_, err := ConfigLock("")
	if err == nil {
		t.Fatal("expected error for empty dir")
	}
}

func TestConfigLock_BlocksUntilRelease(t *testing.T) {
	dir := t.TempDir()

	unlock, err := ConfigLock(dir)
	if err != nil {
		t.Fatalf("ConfigLock: %v", err)
	}

	acquired := make(chan struct{})
	go func() {
		unlock2, err := ConfigLock(dir)
		if err != nil {
			t.Errorf("goroutine ConfigLock: %v", err)
			return
		}
		close(acquired)
		unlock2()
	}()

	// The goroutine should be blocked.
	select {
	case <-acquired:
		t.Fatal("goroutine acquired lock while it should be blocked")
	case <-time.After(50 * time.Millisecond):
		// expected: still blocked
	}

	unlock()

	// Now the goroutine should acquire.
	select {
	case <-acquired:
		// success
	case <-time.After(2 * time.Second):
		t.Fatal("goroutine did not acquire lock after release")
	}
}

func TestConfigLock_ConcurrentAccess(t *testing.T) {
	dir := t.TempDir()

	var mu sync.Mutex
	counter := 0

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unlock, err := ConfigLock(dir)
			if err != nil {
				t.Errorf("ConfigLock: %v", err)
				return
			}
			mu.Lock()
			counter++
			mu.Unlock()
			unlock()
		}()
	}
	wg.Wait()

	if counter != 10 {
		t.Fatalf("expected counter=10, got %d", counter)
	}
}

func TestWithLock_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	sentinel := fmt.Errorf("intentional error from fn")

	err := WithConfigLock(dir, func() error {
		return sentinel
	})
	if err != sentinel {
		t.Fatalf("WithConfigLock returned %v, want %v", err, sentinel)
	}
}

func TestWithLock_CreatesLockFile(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, ConfigLockFileName)

	// Lock file should not exist yet.
	if _, err := os.Stat(lockPath); err == nil {
		t.Fatal("lock file should not exist before WithConfigLock")
	}

	err := WithConfigLock(dir, func() error {
		// Inside fn, the lock file should exist.
		if _, statErr := os.Stat(lockPath); statErr != nil {
			t.Errorf("lock file should exist inside fn: %v", statErr)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WithConfigLock: %v", err)
	}
}

func TestWithLock_ReleasesAfterFn(t *testing.T) {
	dir := t.TempDir()

	// First call should succeed.
	err := WithConfigLock(dir, func() error { return nil })
	if err != nil {
		t.Fatalf("first WithConfigLock: %v", err)
	}

	// Second call should succeed because the lock was released.
	acquired := make(chan struct{})
	go func() {
		err := WithConfigLock(dir, func() error { return nil })
		if err != nil {
			t.Errorf("second WithConfigLock: %v", err)
		}
		close(acquired)
	}()

	select {
	case <-acquired:
		// success — lock was released
	case <-time.After(2 * time.Second):
		t.Fatal("second WithConfigLock did not complete; lock was not released")
	}
}

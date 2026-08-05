//go:build unix

package lockfile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestTryLockExclusiveReportsContentionAndUnlocks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.lock")
	first, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()

	if err := TryLockExclusive(first); err != nil {
		t.Fatalf("first lock: %v", err)
	}
	if err := TryLockExclusive(second); !errors.Is(err, ErrLocked) {
		t.Fatalf("contended lock error = %v, want ErrLocked", err)
	}
	if err := FlockUnlock(first); err != nil {
		t.Fatalf("unlock first: %v", err)
	}
	if err := TryLockExclusive(second); err != nil {
		t.Fatalf("lock after release: %v", err)
	}
}

func TestIsProcessRunningRejectsInvalidPID(t *testing.T) {
	for _, pid := range []int{-1, 0} {
		if IsProcessRunning(pid) {
			t.Fatalf("IsProcessRunning(%d) = true", pid)
		}
	}
}

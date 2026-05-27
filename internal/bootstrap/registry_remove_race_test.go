//go:build unix || linux || darwin

package bootstrap

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestRemoveActiveRegistryIfOwnerDoesNotDeleteSuccessorDuringLockedWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "active.json")
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}

	old := ActiveRegistryEntry{PID: 111, URL: "http://127.0.0.1:1111"}
	successor := ActiveRegistryEntry{PID: 222, URL: "http://127.0.0.1:2222"}
	oldJSON := `{"pid":111,"url":"http://127.0.0.1:1111","started_at":"2026-01-01T00:00:00Z"}`

	lock, err := acquireActiveRegistryLock(path)
	if err != nil {
		t.Fatalf("acquire registry lock: %v", err)
	}
	defer func() { _ = lock.Release() }()

	removeDone := make(chan struct{})
	go func() {
		RemoveActiveRegistryIfOwner(path, old.PID, old.URL)
		close(removeDone)
	}()

	select {
	case <-removeDone:
		if err := WriteActiveRegistry(path, successor); err != nil {
			t.Fatalf("write successor: %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		// Pre-fix behavior: RemoveActiveRegistryIfOwner ignores active.lock
		// and blocks reading active.json. Feed it the old entry, then write
		// a successor while still holding the lock; the stale remover must
		// not delete that successor.
		writerOpened := make(chan struct{})
		writerClose := make(chan struct{})
		writerErr := make(chan error, 1)
		go func() {
			f, err := os.OpenFile(path, os.O_WRONLY, 0)
			if err != nil {
				writerErr <- err
				return
			}
			close(writerOpened)
			if _, err := f.WriteString(oldJSON); err != nil {
				_ = f.Close()
				writerErr <- err
				return
			}
			<-writerClose
			writerErr <- f.Close()
		}()

		select {
		case <-writerOpened:
		case err := <-writerErr:
			t.Fatalf("fifo writer: %v", err)
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for fifo writer to pair with reader")
		}

		if err := WriteActiveRegistry(path, successor); err != nil {
			close(writerClose)
			t.Fatalf("write successor: %v", err)
		}
		close(writerClose)
		if err := <-writerErr; err != nil {
			t.Fatalf("fifo writer close: %v", err)
		}

		select {
		case <-removeDone:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for owner removal")
		}
	}

	got, err := ReadActiveRegistry(path)
	if err != nil {
		t.Fatalf("read registry: %v", err)
	}
	if got == nil {
		t.Fatal("successor registry entry was deleted by stale owner removal")
	}
	if got.PID != successor.PID || got.URL != successor.URL {
		t.Fatalf("registry entry = %+v, want successor %+v", got, successor)
	}
}

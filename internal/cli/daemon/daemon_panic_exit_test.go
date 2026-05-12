package daemon

import (
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli/daemon/supervisor"
)

// TestAwaitDaemonExit_GracefulShutdownReturnsZero verifies the happy path:
// closing the shutdown channel returns exit code 0.
func TestAwaitDaemonExit_GracefulShutdownReturnsZero(t *testing.T) {
	shutdown := make(chan struct{})
	fatalCh := make(chan error, 1)

	exitCh := make(chan int, 1)
	go func() {
		exitCh <- awaitDaemonExit(shutdown, fatalCh)
	}()

	close(shutdown)

	select {
	case code := <-exitCh:
		if code != 0 {
			t.Errorf("graceful shutdown returned code %d, want 0", code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("awaitDaemonExit did not return after shutdown closed")
	}
}

// TestAwaitDaemonExit_FatalChReturnsTwo verifies the fatal path: a fatal error
// delivered on FatalCh returns exit code 2.
func TestAwaitDaemonExit_FatalChReturnsTwo(t *testing.T) {
	shutdown := make(chan struct{})
	fatalCh := make(chan error, 1)

	exitCh := make(chan int, 1)
	go func() {
		exitCh <- awaitDaemonExit(shutdown, fatalCh)
	}()

	fatalCh <- errors.New("synthetic fatal")

	select {
	case code := <-exitCh:
		if code != 2 {
			t.Errorf("fatal path returned code %d, want 2", code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("awaitDaemonExit did not return after FatalCh received")
	}
}

// TestPanicInCriticalGoroutineExitsDaemon is **Acceptance #1**:
//
//	Inject a panic into the reconcile loop in a test build: daemon exits
//	within 5s with a non-zero code, not hangs.
//
// We exercise the full chain end-to-end without spawning a subprocess:
//
//  1. A supervisor.RunCritical-wrapped goroutine panics.
//  2. The harness recovers, logs, and signals FatalCh.
//  3. The daemon main loop's awaitDaemonExit observes FatalCh and returns 2.
//
// Total elapsed time must be well under the 5-second budget.
func TestPanicInCriticalGoroutineExitsDaemon(t *testing.T) {
	sup := &supervisor.Supervisor{
		Shutdown: make(chan struct{}),
		FatalCh:  make(chan error, 1),
	}
	shutdown := make(chan struct{})

	exitCh := make(chan int, 1)
	start := time.Now()
	go func() {
		exitCh <- awaitDaemonExit(shutdown, sup.FatalChannel())
	}()

	// Inject a panic in a goroutine the daemon expects to keep running. The
	// 50ms delay mimics a panic that triggers after startup (e.g., after the
	// first tick).
	sup.RunCritical("test_reconcile", func() {
		time.Sleep(50 * time.Millisecond)
		panic("injected panic — reconcile loop went bad")
	})

	const exitBudget = 5 * time.Second
	select {
	case code := <-exitCh:
		elapsed := time.Since(start)
		if code != 2 {
			t.Errorf("expected exit code 2 on panic, got %d", code)
		}
		if elapsed > exitBudget {
			t.Errorf("daemon took %s to exit (> %s budget)", elapsed, exitBudget)
		}
		t.Logf("panic-to-exit elapsed=%s code=%d", elapsed, code)
	case <-time.After(exitBudget + 1*time.Second):
		t.Fatalf("daemon did not exit within %s after panic", exitBudget)
	}
}

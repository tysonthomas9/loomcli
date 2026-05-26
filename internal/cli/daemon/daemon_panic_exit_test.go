package daemon

import (
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli/daemon/supervisor"
)

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

// TestPanicInCriticalGoroutineExitsDaemon: a panic in a RunCritical-wrapped
// goroutine routes a fatal error to FatalCh, which awaitDaemonExit observes
// and returns exit code 2 within the 5-second budget.
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

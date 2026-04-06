package backends

import (
	"os/exec"
	"sync"
	"syscall"
	"testing"
)

func TestProcessGuard_SignalAfterExit(t *testing.T) {
	cmd := exec.Command("true") //nolint:norawexec
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start: %v", err)
	}
	guard := newProcessGuard(cmd.Process)

	if err := cmd.Wait(); err != nil {
		t.Fatalf("unexpected wait error: %v", err)
	}
	guard.WaitAndMark()

	if guard.Signal(syscall.SIGTERM) {
		t.Error("Signal returned true after WaitAndMark")
	}
}

func TestProcessGuard_SignalDuringRun(t *testing.T) {
	cmd := exec.Command("sleep", "60") //nolint:norawexec
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start: %v", err)
	}
	guard := newProcessGuard(cmd.Process)

	if !guard.Signal(syscall.SIGTERM) {
		t.Error("Signal returned false for running process")
	}

	if err := cmd.Wait(); err == nil {
		t.Fatal("expected error from Wait after SIGTERM")
	}
	guard.WaitAndMark()
}

func TestProcessGuard_ConcurrentSignalAndWait(t *testing.T) {
	cmd := exec.Command("sleep", "60") //nolint:norawexec
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start: %v", err)
	}
	guard := newProcessGuard(cmd.Process)

	// Kill the process so cmd.Wait will return
	_ = cmd.Process.Signal(syscall.SIGTERM)
	_ = cmd.Wait()

	// Hammer Signal and WaitAndMark concurrently to verify no data races.
	// WaitAndMark must be called exactly once, so only one goroutine does it.
	var wg sync.WaitGroup
	wg.Add(11) // 10 signalers + 1 waiter

	for i := 0; i < 10; i++ {
		go func() {
			defer wg.Done()
			guard.Signal(syscall.SIGTERM)
		}()
	}
	go func() {
		defer wg.Done()
		guard.WaitAndMark()
	}()

	wg.Wait()
}

func TestProcessGuard_WaitAndMarkIdempotent(t *testing.T) {
	cmd := exec.Command("true") //nolint:norawexec
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start: %v", err)
	}
	guard := newProcessGuard(cmd.Process)

	if err := cmd.Wait(); err != nil {
		t.Fatalf("unexpected wait error: %v", err)
	}

	// Must not panic on multiple calls.
	guard.WaitAndMark()
	guard.WaitAndMark()
}

func TestProcessGuard_DoneChannel(t *testing.T) {
	cmd := exec.Command("true") //nolint:norawexec
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start: %v", err)
	}
	guard := newProcessGuard(cmd.Process)

	// Done should not be closed yet.
	select {
	case <-guard.Done():
		t.Fatal("Done channel closed before WaitAndMark")
	default:
	}

	if err := cmd.Wait(); err != nil {
		t.Fatalf("unexpected wait error: %v", err)
	}
	guard.WaitAndMark()

	// Done should be closed now.
	select {
	case <-guard.Done():
		// expected
	default:
		t.Fatal("Done channel not closed after WaitAndMark")
	}
}

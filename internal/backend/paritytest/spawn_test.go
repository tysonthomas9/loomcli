//go:build parity

package paritytest

import (
	"os/exec" //nolint:norawexec // test helper spawns sleep to exercise terminateProcess
	"testing"
	"time"
)

// TestTerminateProcess_GracefulExit starts a process that exits cleanly on
// SIGTERM, then asserts terminateProcess returns within the graceful window
// and calls Wait() exactly once. This guards the B1 regression: before the
// fix, exec.CommandContext's internal goroutine also called Wait(), and
// double-Wait panicked with "wait: no child processes" on Go 1.20+.
func TestTerminateProcess_GracefulExit(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Skipf("start sleep: %v", err)
	}

	start := time.Now()
	terminateProcess(cmd, 2*time.Second)
	elapsed := time.Since(start)

	// sleep exits promptly on SIGTERM — should be well under the graceful
	// window. 1s is conservative for CI.
	if elapsed > 1*time.Second {
		t.Errorf("terminateProcess took %v, expected <1s for SIGTERM-responsive process", elapsed)
	}

	// Calling terminateProcess twice must be safe (cleanup paths can run
	// multiple times via t.Cleanup registration overlap).
	terminateProcess(cmd, 100*time.Millisecond)
}

// TestTerminateProcess_NilCmd asserts terminateProcess is nil-safe so
// cleanup paths on failed spawns don't panic.
func TestTerminateProcess_NilCmd(t *testing.T) {
	// Must not panic.
	terminateProcess(nil, time.Second)

	// An exec.Cmd whose Start() never succeeded has Process == nil. We
	// mustn't dereference it.
	cmd := exec.Command("bogus-binary-does-not-exist")
	terminateProcess(cmd, time.Second)
}

// TestTerminateProcess_StubbornProcess starts a process that ignores
// SIGTERM (via `sh -c 'trap "" TERM; sleep 30'`) and asserts the fallback
// SIGKILL path reaps it. This guards B2: on graceful-window timeout, we
// must actually kill the process rather than leak it as a zombie.
func TestTerminateProcess_StubbornProcess(t *testing.T) {
	// trap "" TERM ignores SIGTERM; sleep 30 keeps the shell alive until
	// terminateProcess escalates to SIGKILL.
	cmd := exec.Command("sh", "-c", "trap '' TERM; sleep 30")
	if err := cmd.Start(); err != nil {
		t.Skipf("start sh: %v", err)
	}

	start := time.Now()
	terminateProcess(cmd, 300*time.Millisecond)
	elapsed := time.Since(start)

	// Should take the graceful window (300ms) plus the SIGKILL reap window
	// (500ms) — so at most ~1s in the worst case. We assert <1.5s as a
	// CI-safe upper bound.
	if elapsed > 1500*time.Millisecond {
		t.Errorf("terminateProcess took %v — SIGKILL fallback appears broken", elapsed)
	}

	// ProcessState should eventually be populated since cmd.Wait() ran
	// inside the goroutine. Allow a short settle window.
	for i := 0; i < 10; i++ {
		if cmd.ProcessState != nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if cmd.ProcessState == nil {
		t.Error("cmd.ProcessState is nil after terminateProcess — Wait() did not run")
	}
}

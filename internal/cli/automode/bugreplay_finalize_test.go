//go:build daemon_bugreplay

// Bug-replay fault test for the finalization group (failure reported as
// success), auto-mode half of #245. The daemon half is in
// internal/cli/daemon/supervisor/bugreplay_finalize_test.go.
//
// Expected to FAIL on v5 (1c6dabfc8) and PASS on the #245 fix head.
package automode

import (
	"errors"
	"strings"
	"testing"
)

// TestBugReplay_PR245_WorkScanErrorEndsWithFailureNotIdle replays #245.
//
// Bug: v5 waitForAvailableTasks (automode.go:361-369) prints a ready-queue scan
// error, sleeps the poll interval and retries, forever. The process never
// reports the failure: to the daemon it looks like an agent that found no work,
// and its eventual task-less exit is classified NoWork. The fix bounds the
// retries and exits with a "loom: work scan failed: <cause>" reason.
//
// A non-retryable error is used so the fixed code exits on the first scan and
// no real sleep runs; Interval 0 keeps v5's retry loop instant too.
//
// No model invariant: exit classification is not modeled.
func TestBugReplay_PR245_WorkScanErrorEndsWithFailureNotIdle(t *testing.T) {
	scanErr := errors.New("failed to check ready tasks: HTTP 401 unauthorized")
	scans := 0
	ctx := &autoLoopCtx{
		opts:  AutoModeOptions{AgentType: "plan", Interval: 0},
		state: &AutoModeState{},
		hasAvailableTasks: func() (bool, error) {
			scans++
			return false, scanErr
		},
	}
	shutdown := make(chan struct{})

	const maxScans = 10
	stopped := false
	for i := 0; i < maxScans; i++ {
		if !waitForAvailableTasks(ctx, shutdown) {
			stopped = true
			break
		}
	}

	if !stopped || !ctx.state.ShouldExit {
		t.Fatalf("after %d failed ready-queue scans the loop is still retrying silently "+
			"(ShouldExit=%v): the daemon sees an idle agent, not a scan failure", scans, ctx.state.ShouldExit)
	}
	if !strings.Contains(ctx.state.ExitReason, "work scan failed") {
		t.Errorf("ExitReason = %q, want the work-scan failure marker", ctx.state.ExitReason)
	}
	if !strings.Contains(ctx.state.ExitReason, "HTTP 401 unauthorized") {
		t.Errorf("ExitReason = %q, want the original scan cause", ctx.state.ExitReason)
	}
}

package automode

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/backend"
)

func TestWaitForAvailableTasks_RetryableReadyErrorEmitsWorkScanFailure(t *testing.T) {
	resetDefaultIssueBackend()
	t.Cleanup(resetDefaultIssueBackend)

	readyCalls := 0
	mock := NewMockIssueBackend()
	mock.ReadyFn = func(_ context.Context, _ backend.ReadyOpts) ([]backend.IssueData, error) {
		readyCalls++
		return nil, backend.ErrUnavailable("Ready", "HTTP 429 rate limit exceeded; Retry-After: 7", nil)
	}
	setDefaultIssueBackend(mock)

	var retryDelays []time.Duration
	ctx := &autoLoopCtx{
		opts:  AutoModeOptions{AgentType: "plan"},
		state: &AutoModeState{},
		hasAvailableTasks: func() (bool, error) {
			return HasAvailablePlanningTasks("", "")
		},
		scanSleep: func(delay time.Duration, _ <-chan struct{}) bool {
			retryDelays = append(retryDelays, delay)
			return false
		},
	}

	shutdown := make(chan struct{})
	for attempt := 1; attempt <= workScanMaxAttempts; attempt++ {
		continueLoop := waitForAvailableTasks(ctx, shutdown)
		if attempt < workScanMaxAttempts && !continueLoop {
			t.Fatalf("attempt %d ended the loop before retries exhausted", attempt)
		}
		if attempt == workScanMaxAttempts && continueLoop {
			t.Fatal("retry exhaustion continued as clean idle, want a scan-failure exit")
		}
	}

	if readyCalls != workScanMaxAttempts {
		t.Errorf("Ready calls = %d, want %d", readyCalls, workScanMaxAttempts)
	}
	if len(retryDelays) != workScanMaxAttempts-1 {
		t.Errorf("retry delays = %d, want %d", len(retryDelays), workScanMaxAttempts-1)
	}
	for _, delay := range retryDelays {
		if delay != 7*time.Second {
			t.Errorf("retry delay = %s, want Retry-After 7s", delay)
		}
	}
	if !ctx.state.ShouldExit {
		t.Fatal("ShouldExit = false, want true after scan retries exhaust")
	}
	if !strings.Contains(ctx.state.ExitReason, agenterr.WorkScanFailureMarker) {
		t.Errorf("ExitReason = %q, want work-scan marker", ctx.state.ExitReason)
	}
	if !strings.Contains(ctx.state.ExitReason, "HTTP 429 rate limit exceeded") {
		t.Errorf("ExitReason = %q, want original ready-query cause", ctx.state.ExitReason)
	}
}

func TestWaitForAvailableTasks_GenuineNoWorkRemainsIdle(t *testing.T) {
	ctx := &autoLoopCtx{
		opts:              AutoModeOptions{Interval: 0},
		state:             &AutoModeState{},
		hasAvailableTasks: func() (bool, error) { return false, nil },
		scanSleep: func(time.Duration, <-chan struct{}) bool {
			t.Fatal("scan retry sleep called for genuine no-work")
			return false
		},
	}

	if !waitForAvailableTasks(ctx, make(chan struct{})) {
		t.Fatal("genuine no-work ended the loop, want idle polling")
	}
	if ctx.state.ShouldExit {
		t.Fatalf("ShouldExit = true (%q), want false for genuine no-work", ctx.state.ExitReason)
	}
	if ctx.workScanFailures != 0 {
		t.Errorf("workScanFailures = %d, want 0 after successful empty scan", ctx.workScanFailures)
	}
}

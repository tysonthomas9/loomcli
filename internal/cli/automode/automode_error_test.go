package automode

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/events"
)

// newTestAutoLoopCtx constructs a minimal autoLoopCtx for unit-testing
// handleAutoTaskError and handleAutoTaskSuccess without the full auto-mode loop.
func newTestAutoLoopCtx() *autoLoopCtx {
	return &autoLoopCtx{
		opts: AutoModeOptions{
			Interval:    0,
			BackoffBase: 10 * time.Millisecond,
			TaskPause:   10 * time.Millisecond,
			EventBus:    events.NopBus{},
		},
		state:    &AutoModeState{},
		readLock: func() (*cli.LockInfo, error) { return &cli.LockInfo{}, nil },
	}
}

func TestHandleAutoTaskError_FatalError(t *testing.T) {
	t.Parallel()
	ctx := newTestAutoLoopCtx()
	shutdown := make(chan struct{})

	ae := &agenterr.AgentError{
		Class:   agenterr.AuthFailure,
		Message: "invalid api key",
		Backend: "claude",
	}
	cont := handleAutoTaskError(ctx, ae, fmt.Errorf("exit code 1"), shutdown)
	if cont {
		t.Error("handleAutoTaskError should return false for fatal error")
	}
	if !ctx.state.ShouldExit {
		t.Error("ShouldExit should be true for fatal error")
	}
	if !strings.Contains(ctx.state.ExitReason, "fatal error") {
		t.Errorf("ExitReason = %q, want to contain 'fatal error'", ctx.state.ExitReason)
	}
}

func TestHandleAutoTaskError_ModelNotFound(t *testing.T) {
	t.Parallel()
	ctx := newTestAutoLoopCtx()
	shutdown := make(chan struct{})

	ae := &agenterr.AgentError{
		Class:   agenterr.ModelNotFound,
		Message: "model claude-99 not found",
		Backend: "claude",
	}
	cont := handleAutoTaskError(ctx, ae, fmt.Errorf("exit code 1"), shutdown)
	if cont {
		t.Error("handleAutoTaskError should return false for ModelNotFound")
	}
	if !ctx.state.ShouldExit {
		t.Error("ShouldExit should be true for ModelNotFound")
	}
	if !strings.Contains(ctx.state.ExitReason, "fatal error") {
		t.Errorf("ExitReason = %q, want to contain 'fatal error'", ctx.state.ExitReason)
	}
}

func TestHandleAutoTaskError_RateLimit(t *testing.T) {
	t.Parallel()
	ctx := newTestAutoLoopCtx()
	shutdown := make(chan struct{})

	ae := &agenterr.AgentError{
		Class:      agenterr.RateLimited,
		Message:    "rate limit exceeded",
		Backend:    "claude",
		RetryAfter: 1 * time.Millisecond,
	}
	cont := handleAutoTaskError(ctx, ae, fmt.Errorf("exit code 1"), shutdown)
	if !cont {
		t.Error("handleAutoTaskError should return true for rate-limit (continue)")
	}
	if ctx.state.ConsecutiveRateLimits != 1 {
		t.Errorf("ConsecutiveRateLimits = %d, want 1", ctx.state.ConsecutiveRateLimits)
	}
	// ConsecutiveErrors should NOT be incremented for rate-limit errors.
	if ctx.state.ConsecutiveErrors != 0 {
		t.Errorf("ConsecutiveErrors = %d, want 0 (rate limits use separate counter)", ctx.state.ConsecutiveErrors)
	}
}

func TestHandleAutoTaskError_RateLimit_ExitsAfterFive(t *testing.T) {
	t.Parallel()
	ctx := newTestAutoLoopCtx()
	shutdown := make(chan struct{})

	ae := &agenterr.AgentError{
		Class:      agenterr.RateLimited,
		Message:    "rate limit exceeded",
		Backend:    "claude",
		RetryAfter: 1 * time.Millisecond,
	}

	// First 4 should continue.
	for i := 0; i < 4; i++ {
		cont := handleAutoTaskError(ctx, ae, fmt.Errorf("exit code 1"), shutdown)
		if !cont {
			t.Fatalf("iteration %d: expected continue=true, got false", i+1)
		}
	}
	if ctx.state.ConsecutiveRateLimits != 4 {
		t.Fatalf("ConsecutiveRateLimits = %d, want 4", ctx.state.ConsecutiveRateLimits)
	}

	// 5th should exit.
	cont := handleAutoTaskError(ctx, ae, fmt.Errorf("exit code 1"), shutdown)
	if cont {
		t.Error("5th rate-limit error should cause exit (return false)")
	}
	if !ctx.state.ShouldExit {
		t.Error("ShouldExit should be true after 5 consecutive rate limits")
	}
	if !strings.Contains(ctx.state.ExitReason, "rate limit") {
		t.Errorf("ExitReason = %q, want to contain 'rate limit'", ctx.state.ExitReason)
	}
}

func TestHandleAutoTaskError_TransientBackoff(t *testing.T) {
	t.Parallel()
	ctx := newTestAutoLoopCtx()
	shutdown := make(chan struct{})

	ae := &agenterr.AgentError{
		Class:   agenterr.Transient,
		Message: "server error",
		Backend: "claude",
	}
	cont := handleAutoTaskError(ctx, ae, fmt.Errorf("exit code 1"), shutdown)
	if !cont {
		t.Error("handleAutoTaskError should return true for transient error (continue)")
	}
	if ctx.state.ConsecutiveErrors != 1 {
		t.Errorf("ConsecutiveErrors = %d, want 1", ctx.state.ConsecutiveErrors)
	}
}

func TestHandleAutoTaskError_SuccessResetsAll(t *testing.T) {
	t.Parallel()
	ctx := newTestAutoLoopCtx()
	shutdown := make(chan struct{})

	// Set up some error counters.
	ctx.state.ConsecutiveErrors = 2
	ctx.state.ConsecutiveRateLimits = 3

	// handleAutoTaskSuccess should reset both counters.
	handleAutoTaskSuccess(ctx, "", time.Now(), time.Now(), shutdown)

	if ctx.state.ConsecutiveErrors != 0 {
		t.Errorf("ConsecutiveErrors = %d, want 0 after success", ctx.state.ConsecutiveErrors)
	}
	if ctx.state.ConsecutiveRateLimits != 0 {
		t.Errorf("ConsecutiveRateLimits = %d, want 0 after success", ctx.state.ConsecutiveRateLimits)
	}
}

func TestHandleAutoTaskError_MixedErrors(t *testing.T) {
	t.Parallel()
	ctx := newTestAutoLoopCtx()
	shutdown := make(chan struct{})

	// First: rate-limit error.
	aeRL := &agenterr.AgentError{
		Class:      agenterr.RateLimited,
		Message:    "rate limit exceeded",
		Backend:    "claude",
		RetryAfter: 1 * time.Millisecond,
	}
	handleAutoTaskError(ctx, aeRL, fmt.Errorf("rate limit"), shutdown)
	if ctx.state.ConsecutiveRateLimits != 1 {
		t.Errorf("ConsecutiveRateLimits = %d, want 1", ctx.state.ConsecutiveRateLimits)
	}
	if ctx.state.ConsecutiveErrors != 0 {
		t.Errorf("ConsecutiveErrors = %d, want 0", ctx.state.ConsecutiveErrors)
	}

	// Second: transient error.
	aeTrans := &agenterr.AgentError{
		Class:   agenterr.Transient,
		Message: "server error",
		Backend: "claude",
	}
	handleAutoTaskError(ctx, aeTrans, fmt.Errorf("transient"), shutdown)
	if ctx.state.ConsecutiveRateLimits != 1 {
		t.Errorf("ConsecutiveRateLimits = %d, want 1 (unchanged)", ctx.state.ConsecutiveRateLimits)
	}
	if ctx.state.ConsecutiveErrors != 1 {
		t.Errorf("ConsecutiveErrors = %d, want 1", ctx.state.ConsecutiveErrors)
	}

	// Third: another rate-limit error.
	handleAutoTaskError(ctx, aeRL, fmt.Errorf("rate limit again"), shutdown)
	if ctx.state.ConsecutiveRateLimits != 2 {
		t.Errorf("ConsecutiveRateLimits = %d, want 2", ctx.state.ConsecutiveRateLimits)
	}
	if ctx.state.ConsecutiveErrors != 1 {
		t.Errorf("ConsecutiveErrors = %d, want 1 (unchanged by rate limit)", ctx.state.ConsecutiveErrors)
	}
}

func TestHandleAutoTaskError_NoWork(t *testing.T) {
	t.Parallel()
	ctx := newTestAutoLoopCtx()
	shutdown := make(chan struct{})

	ae := &agenterr.AgentError{
		Class:   agenterr.NoWork,
		Message: "no work available",
		Backend: "claude",
	}
	cont := handleAutoTaskError(ctx, ae, fmt.Errorf("no work"), shutdown)
	if cont {
		t.Error("handleAutoTaskError should return false for NoWork")
	}
	if !ctx.state.ShouldExit {
		t.Error("ShouldExit should be true for NoWork")
	}
	if !strings.Contains(ctx.state.ExitReason, "no work") {
		t.Errorf("ExitReason = %q, want to contain 'no work'", ctx.state.ExitReason)
	}
}

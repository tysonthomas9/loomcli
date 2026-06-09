package automode

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/olesho/harness-wrapper/pkg/wrapper"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/backends"
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
		Class:   agenterr.OutcomeFromHarness(wrapper.ErrAuth),
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
		Class:   agenterr.OutcomeFromHarness(wrapper.ErrModelNotFound),
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
		Class:      agenterr.OutcomeFromHarness(wrapper.ErrRateLimited),
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
		Class:      agenterr.OutcomeFromHarness(wrapper.ErrRateLimited),
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
		Class:   agenterr.OutcomeFromHarness(wrapper.ErrTransient),
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
		Class:      agenterr.OutcomeFromHarness(wrapper.ErrRateLimited),
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
		Class:   agenterr.OutcomeFromHarness(wrapper.ErrTransient),
		Message: "server error",
		Backend: "claude",
	}
	handleAutoTaskError(ctx, aeTrans, fmt.Errorf("transient"), shutdown)
	if ctx.state.ConsecutiveRateLimits != 0 {
		t.Errorf("ConsecutiveRateLimits = %d, want 0 after non-rate-limit error", ctx.state.ConsecutiveRateLimits)
	}
	if ctx.state.ConsecutiveErrors != 1 {
		t.Errorf("ConsecutiveErrors = %d, want 1", ctx.state.ConsecutiveErrors)
	}

	// Third: another rate-limit error.
	handleAutoTaskError(ctx, aeRL, fmt.Errorf("rate limit again"), shutdown)
	if ctx.state.ConsecutiveRateLimits != 1 {
		t.Errorf("ConsecutiveRateLimits = %d, want 1 after reset", ctx.state.ConsecutiveRateLimits)
	}
	if ctx.state.ConsecutiveErrors != 0 {
		t.Errorf("ConsecutiveErrors = %d, want 0 after rate limit", ctx.state.ConsecutiveErrors)
	}
}

func TestHandleAutoTaskError_NoWork(t *testing.T) {
	t.Parallel()
	ctx := newTestAutoLoopCtx()
	shutdown := make(chan struct{})

	ae := &agenterr.AgentError{
		Class:   agenterr.OutcomeFromDomain(agenterr.NoWorkOutcome),
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

func TestClassifyInvokeError_UsesInvocationLocalOutput(t *testing.T) {
	err := &backends.InvocationError{
		Err:        errors.New("agent failed"),
		OutputTail: "Error: 429 Too Many Requests\nretry-after: 60",
		ExitCode:   1,
	}

	got := classifyInvokeError(err, "claude")
	if got.Class != agenterr.OutcomeFromHarness(wrapper.ErrRateLimited) {
		t.Fatalf("Class = %s, want %s", got.Class, agenterr.OutcomeFromHarness(wrapper.ErrRateLimited))
	}
	if got.RetryAfter.Seconds() != 60 {
		t.Fatalf("RetryAfter = %s, want 60s", got.RetryAfter)
	}
	if !strings.Contains(got.RawOutput, "429") {
		t.Fatalf("RawOutput = %q, want invocation-local output", got.RawOutput)
	}
}

func TestClassifyInvokeError_FallsBackToRawErrorText(t *testing.T) {
	got := classifyInvokeError(errors.New("OPENAI_API_KEY not set"), "codex")
	if got.Class != agenterr.OutcomeFromHarness(wrapper.ErrAuth) {
		t.Fatalf("Class = %s, want %s", got.Class, agenterr.OutcomeFromHarness(wrapper.ErrAuth))
	}
}

func TestHandleTransientError_ResetsRateLimitCounter(t *testing.T) {
	t.Parallel()

	ctx := &autoLoopCtx{
		opts: AutoModeOptions{BackoffBase: 10 * time.Millisecond},
		state: &AutoModeState{
			ConsecutiveRateLimits: 2,
		},
	}

	if !handleTransientError(ctx, make(chan struct{})) {
		t.Fatalf("handleTransientError() returned false, want retry")
	}
	if ctx.state.ConsecutiveRateLimits != 0 {
		t.Fatalf("ConsecutiveRateLimits = %d, want 0", ctx.state.ConsecutiveRateLimits)
	}
	if ctx.state.ConsecutiveErrors != 1 {
		t.Fatalf("ConsecutiveErrors = %d, want 1", ctx.state.ConsecutiveErrors)
	}
}

func TestHandleRateLimitError_ResetsErrorCounter(t *testing.T) {
	t.Parallel()

	ctx := &autoLoopCtx{
		opts: AutoModeOptions{BackoffBase: 10 * time.Millisecond},
		state: &AutoModeState{
			ConsecutiveErrors: 2,
		},
	}

	ae := &agenterr.AgentError{Class: agenterr.OutcomeFromHarness(wrapper.ErrRateLimited), RetryAfter: time.Millisecond}
	if !handleRateLimitError(ctx, ae, make(chan struct{})) {
		t.Fatalf("handleRateLimitError() returned false, want retry")
	}
	if ctx.state.ConsecutiveErrors != 0 {
		t.Fatalf("ConsecutiveErrors = %d, want 0", ctx.state.ConsecutiveErrors)
	}
	if ctx.state.ConsecutiveRateLimits != 1 {
		t.Fatalf("ConsecutiveRateLimits = %d, want 1", ctx.state.ConsecutiveRateLimits)
	}
}

func TestHandleDefaultError_ResetsRateLimitCounter(t *testing.T) {
	t.Parallel()

	ctx := &autoLoopCtx{
		opts: AutoModeOptions{Interval: 0},
		state: &AutoModeState{
			ConsecutiveRateLimits: 2,
		},
	}

	if !handleDefaultError(ctx, make(chan struct{})) {
		t.Fatalf("handleDefaultError() returned false, want retry")
	}
	if ctx.state.ConsecutiveRateLimits != 0 {
		t.Fatalf("ConsecutiveRateLimits = %d, want 0", ctx.state.ConsecutiveRateLimits)
	}
	if ctx.state.ConsecutiveErrors != 1 {
		t.Fatalf("ConsecutiveErrors = %d, want 1", ctx.state.ConsecutiveErrors)
	}
}

func TestTrackResumeFailures_IgnoresNonResumeErrors(t *testing.T) {
	t.Parallel()

	ctx := &autoLoopCtx{
		lastClaudeSessionID: "sess-123",
		resumeFailures:      1,
		resumeAttempted:     false,
	}

	trackResumeFailures(ctx)
	if ctx.resumeFailures != 1 {
		t.Fatalf("resumeFailures = %d, want unchanged", ctx.resumeFailures)
	}
	if ctx.lastClaudeSessionID != "sess-123" {
		t.Fatalf("lastClaudeSessionID = %q, want unchanged", ctx.lastClaudeSessionID)
	}
}

func TestTrackResumeFailures_OnlyCountsAttemptedResume(t *testing.T) {
	t.Parallel()

	ctx := &autoLoopCtx{
		lastClaudeSessionID: "sess-123",
		resumeAttempted:     true,
	}

	trackResumeFailures(ctx)
	if ctx.resumeFailures != 1 {
		t.Fatalf("resumeFailures = %d, want 1", ctx.resumeFailures)
	}
}

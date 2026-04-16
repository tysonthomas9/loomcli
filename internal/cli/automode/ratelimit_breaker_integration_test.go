package automode

import (
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/events"
)

// newBreakerTestCtx constructs an autoLoopCtx wired with a configured
// rateLimitBreaker and a captureBus so tests can assert on emitted events.
func newBreakerTestCtx(window, cooldown time.Duration, threshold int, bus *captureBus) *autoLoopCtx {
	return &autoLoopCtx{
		opts: AutoModeOptions{
			AgentName:          "test-agent",
			Interval:           0,
			BackoffBase:        1 * time.Millisecond,
			TaskPause:          1 * time.Millisecond,
			EventBus:           bus,
			RateLimitWindow:    window,
			RateLimitCooldown:  cooldown,
			RateLimitThreshold: threshold,
		},
		state:            &AutoModeState{},
		readLock:         func() (*cli.LockInfo, error) { return &cli.LockInfo{}, nil },
		rateLimitBreaker: newRateLimitBreaker(window, cooldown, threshold),
	}
}

func TestHandleRateLimitError_TripsBreaker_EmitsCircuitOpened(t *testing.T) {
	t.Parallel()
	bus := &captureBus{}
	// Threshold 3, short cooldown so test timing is quick.
	ctx := newBreakerTestCtx(1*time.Hour, 1*time.Millisecond, 3, bus)
	shutdown := make(chan struct{})

	ae := &agenterr.AgentError{
		Class:      agenterr.RateLimited,
		Message:    "rate limit",
		RetryAfter: 1 * time.Millisecond,
	}
	rawErr := errors.New("exit 1")

	// First two rate limits: no trip yet.
	for i := 1; i <= 2; i++ {
		if !handleAutoTaskError(ctx, ae, rawErr, shutdown) {
			t.Fatalf("iteration %d: handleAutoTaskError returned false", i)
		}
	}
	for _, e := range bus.snapshot() {
		if e.Type == events.CircuitOpened {
			t.Fatal("CircuitOpened emitted before threshold reached")
		}
	}
	if ctx.rateLimitBreaker.State() != breakerClosed {
		t.Fatalf("breaker state = %s, want closed", ctx.rateLimitBreaker.State())
	}

	// Third rate limit: trips the breaker.
	if !handleAutoTaskError(ctx, ae, rawErr, shutdown) {
		t.Fatal("3rd handleAutoTaskError returned false, want true (retry)")
	}

	if ctx.rateLimitBreaker.State() != breakerOpen {
		t.Fatalf("breaker state after 3rd rate limit = %s, want open", ctx.rateLimitBreaker.State())
	}
	if ctx.rateLimitBreaker.totalTrips != 1 {
		t.Fatalf("totalTrips = %d, want 1", ctx.rateLimitBreaker.totalTrips)
	}

	var openedCount int
	var opened events.Event
	for _, e := range bus.snapshot() {
		if e.Type == events.CircuitOpened {
			openedCount++
			opened = e
		}
	}
	if openedCount != 1 {
		t.Fatalf("CircuitOpened event count = %d, want 1", openedCount)
	}
	decoded, err := opened.DecodeData()
	if err != nil {
		t.Fatalf("DecodeData: %v", err)
	}
	data, ok := decoded.(*events.CircuitOpenedData)
	if !ok {
		t.Fatalf("decoded data = %T, want *CircuitOpenedData", decoded)
	}
	if data.RateLimitCount != 3 {
		t.Errorf("RateLimitCount = %d, want 3", data.RateLimitCount)
	}
	if data.WindowDuration.Duration != 1*time.Hour {
		t.Errorf("WindowDuration = %s, want 1h", data.WindowDuration.Duration)
	}
	if data.CooldownDuration.Duration != 1*time.Millisecond {
		t.Errorf("CooldownDuration = %s, want 1ms", data.CooldownDuration.Duration)
	}
}

func TestHandleRateLimitError_DoesNotReEmitWhileOpen(t *testing.T) {
	t.Parallel()
	bus := &captureBus{}
	ctx := newBreakerTestCtx(1*time.Hour, 1*time.Millisecond, 2, bus)
	shutdown := make(chan struct{})

	ae := &agenterr.AgentError{
		Class:      agenterr.RateLimited,
		Message:    "rate limit",
		RetryAfter: 1 * time.Millisecond,
	}
	rawErr := errors.New("exit 1")

	// Trip the breaker.
	handleAutoTaskError(ctx, ae, rawErr, shutdown)
	handleAutoTaskError(ctx, ae, rawErr, shutdown)
	if ctx.rateLimitBreaker.State() != breakerOpen {
		t.Fatalf("state = %s, want open", ctx.rateLimitBreaker.State())
	}

	// Further rate-limit errors while Open should not re-emit CircuitOpened.
	handleAutoTaskError(ctx, ae, rawErr, shutdown)

	var openedCount int
	for _, e := range bus.snapshot() {
		if e.Type == events.CircuitOpened {
			openedCount++
		}
	}
	if openedCount != 1 {
		t.Fatalf("CircuitOpened emitted %d times, want 1 (should not re-emit while open)", openedCount)
	}
}

func TestHandleAutoTaskSuccess_ClosesBreaker_EmitsCircuitClosed(t *testing.T) {
	t.Parallel()
	bus := &captureBus{}
	ctx := newBreakerTestCtx(1*time.Hour, 1*time.Millisecond, 2, bus)
	shutdown := make(chan struct{})

	ae := &agenterr.AgentError{
		Class:      agenterr.RateLimited,
		Message:    "rate limit",
		RetryAfter: 1 * time.Millisecond,
	}
	rawErr := errors.New("exit 1")

	// Trip the breaker.
	handleAutoTaskError(ctx, ae, rawErr, shutdown)
	handleAutoTaskError(ctx, ae, rawErr, shutdown)
	if ctx.rateLimitBreaker.State() != breakerOpen {
		t.Fatalf("state = %s, want open", ctx.rateLimitBreaker.State())
	}

	// Simulate cooldown elapsing and transition to half-open via ShouldBlock.
	time.Sleep(2 * time.Millisecond)
	blocked, _ := ctx.rateLimitBreaker.ShouldBlock()
	if blocked {
		t.Fatalf("ShouldBlock = true, want false after cooldown")
	}
	if ctx.rateLimitBreaker.State() != breakerHalfOpen {
		t.Fatalf("state = %s, want half-open", ctx.rateLimitBreaker.State())
	}

	// handleAutoTaskSuccess with a cleared task ID → handleNoProgress path,
	// but the breaker transition should still happen via recordProbeSuccessOnBreaker.
	// We call recordProbeSuccessOnBreaker directly to avoid handleAutoTaskSuccess's
	// no-progress-exit side effect (ConsecutiveNoProgress >= 3).
	recordProbeSuccessOnBreaker(ctx)

	if ctx.rateLimitBreaker.State() != breakerClosed {
		t.Fatalf("state = %s, want closed after probe success", ctx.rateLimitBreaker.State())
	}

	var closedCount int
	var closed events.Event
	for _, e := range bus.snapshot() {
		if e.Type == events.CircuitClosed {
			closedCount++
			closed = e
		}
	}
	if closedCount != 1 {
		t.Fatalf("CircuitClosed event count = %d, want 1", closedCount)
	}
	decoded, err := closed.DecodeData()
	if err != nil {
		t.Fatalf("DecodeData: %v", err)
	}
	data, ok := decoded.(*events.CircuitClosedData)
	if !ok {
		t.Fatalf("decoded data = %T, want *CircuitClosedData", decoded)
	}
	if data.Reason != "probe_success" {
		t.Errorf("Reason = %q, want probe_success", data.Reason)
	}
}

func TestRecordProbeSuccessOnBreaker_NoOpWhenClosed(t *testing.T) {
	t.Parallel()
	bus := &captureBus{}
	ctx := newBreakerTestCtx(1*time.Hour, 1*time.Millisecond, 5, bus)

	// Breaker is Closed.
	recordProbeSuccessOnBreaker(ctx)

	for _, e := range bus.snapshot() {
		if e.Type == events.CircuitClosed {
			t.Fatal("CircuitClosed emitted in Closed state (should be no-op)")
		}
	}
}

func TestWaitForCircuitBreaker_ClosedPassesThrough(t *testing.T) {
	t.Parallel()
	bus := &captureBus{}
	ctx := newBreakerTestCtx(1*time.Hour, 1*time.Millisecond, 5, bus)

	if !waitForCircuitBreaker(ctx, make(chan struct{})) {
		t.Fatal("waitForCircuitBreaker returned false when breaker closed")
	}
}

func TestWaitForCircuitBreaker_OpenWaitsUntilCooldown(t *testing.T) {
	t.Parallel()
	bus := &captureBus{}
	ctx := newBreakerTestCtx(1*time.Hour, 10*time.Millisecond, 2, bus)
	shutdown := make(chan struct{})

	ae := &agenterr.AgentError{Class: agenterr.RateLimited, RetryAfter: 1 * time.Millisecond}
	handleAutoTaskError(ctx, ae, errors.New("exit 1"), shutdown)
	handleAutoTaskError(ctx, ae, errors.New("exit 1"), shutdown)
	if ctx.rateLimitBreaker.State() != breakerOpen {
		t.Fatalf("state = %s, want open", ctx.rateLimitBreaker.State())
	}

	start := time.Now()
	if !waitForCircuitBreaker(ctx, shutdown) {
		t.Fatal("waitForCircuitBreaker returned false, want true after cooldown")
	}
	elapsed := time.Since(start)
	if elapsed < 5*time.Millisecond {
		t.Errorf("waitForCircuitBreaker returned too quickly: %s, want >= 5ms", elapsed)
	}
	// After waiting, breaker should be HalfOpen.
	if ctx.rateLimitBreaker.State() != breakerHalfOpen {
		t.Errorf("state after wait = %s, want half-open", ctx.rateLimitBreaker.State())
	}
}

func TestWaitForCircuitBreaker_ShutdownDuringCooldown(t *testing.T) {
	t.Parallel()
	bus := &captureBus{}
	// Long cooldown to ensure shutdown fires first.
	ctx := newBreakerTestCtx(1*time.Hour, 10*time.Second, 2, bus)
	shutdown := make(chan struct{})

	ae := &agenterr.AgentError{Class: agenterr.RateLimited, RetryAfter: 1 * time.Millisecond}
	handleAutoTaskError(ctx, ae, errors.New("exit 1"), shutdown)
	handleAutoTaskError(ctx, ae, errors.New("exit 1"), shutdown)

	// Fire shutdown after a brief delay.
	go func() {
		time.Sleep(5 * time.Millisecond)
		close(shutdown)
	}()

	if waitForCircuitBreaker(ctx, shutdown) {
		t.Fatal("waitForCircuitBreaker returned true, want false after shutdown")
	}
	if !ctx.state.ShouldExit {
		t.Error("ShouldExit should be true after shutdown")
	}
}

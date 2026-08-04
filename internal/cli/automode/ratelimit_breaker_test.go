package automode

import (
	"testing"
	"time"
)

// fakeClock returns a now func bound to a mutable *time.Time so tests can
// advance time without real sleeps.
func fakeClock(t0 *time.Time) func() time.Time {
	return func() time.Time { return *t0 }
}

func TestRateLimitBreaker_ClosedState(t *testing.T) {
	t.Parallel()

	now := time.Now()
	b := newRateLimitBreaker(10*time.Minute, 5*time.Minute, 3)
	b.now = fakeClock(&now)

	b.RecordRateLimit()
	b.RecordRateLimit()

	if b.State() != breakerClosed {
		t.Fatalf("State = %s, want closed", b.State())
	}
	blocked, _ := b.ShouldBlock()
	if blocked {
		t.Fatalf("ShouldBlock = true, want false below threshold")
	}
	if b.WindowCount() != 2 {
		t.Fatalf("WindowCount = %d, want 2", b.WindowCount())
	}
}

func TestRateLimitBreaker_TripsAtThreshold(t *testing.T) {
	t.Parallel()

	now := time.Now()
	b := newRateLimitBreaker(10*time.Minute, 5*time.Minute, 3)
	b.now = fakeClock(&now)

	b.RecordRateLimit()
	b.RecordRateLimit()
	state := b.RecordRateLimit()

	if state != breakerOpen {
		t.Fatalf("RecordRateLimit returned %s, want open", state)
	}
	blocked, remaining := b.ShouldBlock()
	if !blocked {
		t.Fatalf("ShouldBlock = false, want true when open")
	}
	if remaining <= 0 || remaining > 5*time.Minute {
		t.Fatalf("remaining = %s, want (0, 5m]", remaining)
	}
	if b.totalTrips != 1 {
		t.Fatalf("totalTrips = %d, want 1", b.totalTrips)
	}
}

func TestRateLimitBreaker_SlidingWindowPrune(t *testing.T) {
	t.Parallel()

	now := time.Now()
	b := newRateLimitBreaker(10*time.Minute, 5*time.Minute, 5)
	b.now = fakeClock(&now)

	// Three events in the window.
	b.RecordRateLimit()
	b.RecordRateLimit()
	b.RecordRateLimit()
	if b.WindowCount() != 3 {
		t.Fatalf("WindowCount = %d, want 3", b.WindowCount())
	}

	// Advance past the window; the old events should prune.
	now = now.Add(11 * time.Minute)
	b.RecordRateLimit()
	if b.WindowCount() != 1 {
		t.Fatalf("WindowCount after prune = %d, want 1", b.WindowCount())
	}
	if b.State() != breakerClosed {
		t.Fatalf("State = %s, want closed (threshold not reached after prune)", b.State())
	}
}

func TestRateLimitBreaker_CooldownElapsed(t *testing.T) {
	t.Parallel()

	now := time.Now()
	b := newRateLimitBreaker(10*time.Minute, 5*time.Minute, 2)
	b.now = fakeClock(&now)

	b.RecordRateLimit()
	b.RecordRateLimit()
	if b.State() != breakerOpen {
		t.Fatalf("State = %s, want open", b.State())
	}

	// Before cooldown elapses, ShouldBlock returns true.
	blocked, _ := b.ShouldBlock()
	if !blocked {
		t.Fatalf("ShouldBlock = false, want true during cooldown")
	}

	// Advance past cooldown.
	now = now.Add(6 * time.Minute)
	blocked, remaining := b.ShouldBlock()
	if blocked {
		t.Fatalf("ShouldBlock = true, want false after cooldown")
	}
	if remaining != 0 {
		t.Fatalf("remaining = %s, want 0", remaining)
	}
	if b.State() != breakerHalfOpen {
		t.Fatalf("State = %s, want half-open after cooldown", b.State())
	}
}

func TestRateLimitBreaker_ProbeSuccess(t *testing.T) {
	t.Parallel()

	now := time.Now()
	b := newRateLimitBreaker(10*time.Minute, 1*time.Minute, 2)
	b.now = fakeClock(&now)

	b.RecordRateLimit()
	b.RecordRateLimit()

	// Trigger Open→HalfOpen transition.
	now = now.Add(2 * time.Minute)
	b.ShouldBlock()
	if b.State() != breakerHalfOpen {
		t.Fatalf("State = %s, want half-open", b.State())
	}

	b.RecordSuccess()
	if b.State() != breakerClosed {
		t.Fatalf("State = %s, want closed after probe success", b.State())
	}
	if b.WindowCount() != 0 {
		t.Fatalf("WindowCount = %d, want 0 after close", b.WindowCount())
	}
}

func TestRateLimitBreaker_ProbeFailure(t *testing.T) {
	t.Parallel()

	now := time.Now()
	b := newRateLimitBreaker(10*time.Minute, 1*time.Minute, 2)
	b.now = fakeClock(&now)

	b.RecordRateLimit()
	b.RecordRateLimit()
	firstOpenedAt := b.openedAt

	// Transition to half-open.
	now = now.Add(2 * time.Minute)
	b.ShouldBlock()
	if b.State() != breakerHalfOpen {
		t.Fatalf("State = %s, want half-open", b.State())
	}

	// Probe fails → re-open with fresh cooldown.
	now = now.Add(1 * time.Second)
	state := b.RecordRateLimit()
	if state != breakerOpen {
		t.Fatalf("State = %s, want open after probe failure", state)
	}
	if !b.openedAt.After(firstOpenedAt) {
		t.Fatalf("openedAt = %s, want fresh (after %s)", b.openedAt, firstOpenedAt)
	}
	if b.totalTrips != 2 {
		t.Fatalf("totalTrips = %d, want 2", b.totalTrips)
	}
}

func TestRateLimitBreaker_SuccessDoesNotClearWindowInClosed(t *testing.T) {
	t.Parallel()

	now := time.Now()
	b := newRateLimitBreaker(10*time.Minute, 5*time.Minute, 5)
	b.now = fakeClock(&now)

	b.RecordRateLimit()
	b.RecordRateLimit()
	b.RecordSuccess() // state is Closed → no-op on window

	if b.WindowCount() != 2 {
		t.Fatalf("WindowCount = %d, want 2 (success in Closed should not prune)", b.WindowCount())
	}
}

func TestRateLimitBreaker_DisabledWithZeroThreshold(t *testing.T) {
	t.Parallel()

	now := time.Now()
	b := newRateLimitBreaker(10*time.Minute, 5*time.Minute, 0)
	b.now = fakeClock(&now)

	for i := 0; i < 100; i++ {
		b.RecordRateLimit()
	}
	if b.State() != breakerClosed {
		t.Fatalf("State = %s, want closed when disabled", b.State())
	}
	blocked, _ := b.ShouldBlock()
	if blocked {
		t.Fatalf("ShouldBlock = true, want false when disabled")
	}
	if b.WindowCount() != 0 {
		t.Fatalf("WindowCount = %d, want 0 when disabled", b.WindowCount())
	}
}

func TestRateLimitBreaker_DisabledWithZeroWindow(t *testing.T) {
	t.Parallel()

	now := time.Now()
	b := newRateLimitBreaker(0, 5*time.Minute, 3)
	b.now = fakeClock(&now)

	for i := 0; i < 100; i++ {
		b.RecordRateLimit()
	}
	if b.State() != breakerClosed {
		t.Fatalf("State = %s, want closed when disabled", b.State())
	}
	blocked, _ := b.ShouldBlock()
	if blocked {
		t.Fatalf("ShouldBlock = true, want false when disabled")
	}
}

func TestRateLimitBreaker_TotalTrips(t *testing.T) {
	t.Parallel()

	now := time.Now()
	b := newRateLimitBreaker(10*time.Minute, 1*time.Minute, 2)
	b.now = fakeClock(&now)

	// Trip 1: Closed → Open.
	b.RecordRateLimit()
	b.RecordRateLimit()
	// Recover.
	now = now.Add(2 * time.Minute)
	b.ShouldBlock()
	b.RecordSuccess()

	// Trip 2: Closed → Open.
	b.RecordRateLimit()
	b.RecordRateLimit()
	// Recover.
	now = now.Add(2 * time.Minute)
	b.ShouldBlock()
	b.RecordSuccess()

	// Trip 3: Closed → Open.
	b.RecordRateLimit()
	b.RecordRateLimit()

	if b.totalTrips != 3 {
		t.Fatalf("totalTrips = %d, want 3", b.totalTrips)
	}
}

func TestRateLimitBreaker_ShouldBlockInHalfOpen(t *testing.T) {
	t.Parallel()

	now := time.Now()
	b := newRateLimitBreaker(10*time.Minute, 1*time.Minute, 2)
	b.now = fakeClock(&now)

	b.RecordRateLimit()
	b.RecordRateLimit()
	now = now.Add(2 * time.Minute)
	b.ShouldBlock() // transition to half-open

	// Second ShouldBlock in half-open should not block (probe is in progress).
	blocked, _ := b.ShouldBlock()
	if blocked {
		t.Fatalf("ShouldBlock in half-open = true, want false")
	}
}

func TestRateLimitBreaker_WindowCountPrunesOldEvents(t *testing.T) {
	t.Parallel()

	now := time.Now()
	b := newRateLimitBreaker(5*time.Minute, 1*time.Minute, 10)
	b.now = fakeClock(&now)

	b.RecordRateLimit()
	b.RecordRateLimit()
	if b.WindowCount() != 2 {
		t.Fatalf("WindowCount = %d, want 2", b.WindowCount())
	}

	// Advance past window. WindowCount should prune.
	now = now.Add(6 * time.Minute)
	if b.WindowCount() != 0 {
		t.Fatalf("WindowCount after window expiry = %d, want 0", b.WindowCount())
	}
}

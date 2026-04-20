package automode

import "time"

// breakerState represents the state of the rate-limit circuit breaker.
type breakerState int

const (
	breakerClosed   breakerState = iota // Normal operation.
	breakerOpen                         // All work paused, cooldown active.
	breakerHalfOpen                     // Cooldown elapsed, single probe allowed.
)

func (s breakerState) String() string {
	switch s {
	case breakerClosed:
		return "closed"
	case breakerOpen:
		return "open"
	case breakerHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// rateLimitBreaker is an auto-mode-specific sliding-window circuit breaker that
// trips when rate-limit errors accumulate within a time window (regardless of
// intervening successes). It complements the per-error consecutive-rate-limit
// counter in handleRateLimitError by catching alternating rate-limit/success
// patterns that the consecutive counter misses.
//
// State machine:
//
//	Closed  → Open     when events in window reach threshold
//	Open    → HalfOpen when cooldown elapses (via ShouldBlock())
//	HalfOpen→ Closed   via RecordSuccess (window cleared)
//	HalfOpen→ Open     via RecordRateLimit (fresh cooldown)
//
// Not safe for concurrent use. The auto-mode loop is single-goroutine.
//
// Disabled when threshold or window are <= 0.
type rateLimitBreaker struct {
	window    time.Duration
	threshold int
	cooldown  time.Duration

	events     []time.Time // timestamps of rate-limit errors, pruned on each record
	state      breakerState
	openedAt   time.Time
	totalTrips int // lifetime counter (for summary output)

	// now is injectable for tests; production uses time.Now.
	now func() time.Time
}

// newRateLimitBreaker constructs a breaker. If threshold <= 0 or window <= 0,
// the breaker is disabled: ShouldBlock always returns false and RecordRateLimit
// is a no-op.
func newRateLimitBreaker(window, cooldown time.Duration, threshold int) *rateLimitBreaker {
	return &rateLimitBreaker{
		window:    window,
		threshold: threshold,
		cooldown:  cooldown,
		state:     breakerClosed,
		now:       time.Now,
	}
}

// disabled reports whether the breaker should treat all calls as no-ops.
func (b *rateLimitBreaker) disabled() bool {
	return b.threshold <= 0 || b.window <= 0
}

// RecordRateLimit appends a rate-limit event to the sliding window. It prunes
// events older than the window, then checks whether the event count reached the
// threshold; if so the breaker transitions Closed→Open (or HalfOpen→Open with a
// fresh cooldown). Returns the new state.
func (b *rateLimitBreaker) RecordRateLimit() breakerState {
	if b.disabled() {
		return b.state
	}
	now := b.now()
	b.pruneLocked(now)
	b.events = append(b.events, now)

	// In HalfOpen, a rate-limit on the probe re-opens the breaker immediately
	// with a fresh cooldown, regardless of window count.
	if b.state == breakerHalfOpen {
		b.state = breakerOpen
		b.openedAt = now
		b.totalTrips++
		return b.state
	}

	if len(b.events) >= b.threshold && b.state == breakerClosed {
		b.state = breakerOpen
		b.openedAt = now
		b.totalTrips++
	}
	return b.state
}

// RecordSuccess is called when an invocation completes without a rate-limit
// error. When the breaker is HalfOpen, the probe succeeded and the breaker
// transitions to Closed with a fresh (empty) window. In Closed state,
// RecordSuccess does NOT clear the window — alternating rate limits and
// successes still accumulate.
func (b *rateLimitBreaker) RecordSuccess() {
	if b.disabled() {
		return
	}
	if b.state == breakerHalfOpen {
		b.state = breakerClosed
		b.events = b.events[:0]
	}
}

// ShouldBlock reports whether the loop should block instead of invoking the
// agent. When Open, returns (true, remaining cooldown). When the cooldown has
// elapsed, transitions Open→HalfOpen and returns (false, 0) so a single probe
// can run. In Closed or HalfOpen, returns (false, 0).
func (b *rateLimitBreaker) ShouldBlock() (bool, time.Duration) {
	if b.disabled() {
		return false, 0
	}
	if b.state != breakerOpen {
		return false, 0
	}
	elapsed := b.now().Sub(b.openedAt)
	if elapsed >= b.cooldown {
		b.state = breakerHalfOpen
		return false, 0
	}
	return true, b.cooldown - elapsed
}

// State returns the current breaker state without side effects. Note that
// Open→HalfOpen transitions only happen via ShouldBlock; callers wanting the
// effective state should use ShouldBlock instead.
func (b *rateLimitBreaker) State() breakerState {
	if b.disabled() {
		return breakerClosed
	}
	return b.state
}

// WindowCount returns the number of rate-limit events currently in the sliding
// window (after pruning events older than window).
func (b *rateLimitBreaker) WindowCount() int {
	if b.disabled() {
		return 0
	}
	b.pruneLocked(b.now())
	return len(b.events)
}

// pruneLocked drops events older than the window. Kept unexported because the
// breaker is single-goroutine — the "locked" suffix mirrors the naming
// convention in other packages for clarity, not for synchronization.
func (b *rateLimitBreaker) pruneLocked(now time.Time) {
	cutoff := now.Add(-b.window)
	n := 0
	for _, ts := range b.events {
		if !ts.Before(cutoff) {
			b.events[n] = ts
			n++
		}
	}
	b.events = b.events[:n]
}

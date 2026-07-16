package runtime //nolint:revive // The approved target architecture names this platform mechanism runtime.

import (
	"context"
	"math"
	rand "math/rand/v2"
	"time"
)

// Timer is the minimum timer contract used by Host. It keeps scheduling fully
// deterministic under an injected Clock.
type Timer interface {
	C() <-chan time.Time
	Stop() bool
}

// Clock owns wall-clock reads, waits, and per-pass timeout contexts.
type Clock interface {
	Now() time.Time
	NewTimer(time.Duration) Timer
	WithTimeout(context.Context, time.Duration) (context.Context, context.CancelFunc)
}

// JitterSource returns an additional delay in [0, maximum]. Host clamps a
// misbehaving injected source so a component can never run early or escape its
// declared bound.
type JitterSource func(maximum time.Duration) time.Duration

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

func (realClock) NewTimer(delay time.Duration) Timer {
	return realTimer{Timer: time.NewTimer(delay)}
}

func (realClock) WithTimeout(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, timeout)
}

type realTimer struct {
	*time.Timer
}

func (timer realTimer) C() <-chan time.Time { return timer.Timer.C }

func randomJitter(maximum time.Duration) time.Duration {
	if maximum <= 0 {
		return 0
	}
	if maximum == time.Duration(math.MaxInt64) {
		return time.Duration(rand.Int64()) //nolint:gosec // Scheduling jitter is not a security token.
	}
	return time.Duration(rand.Int64N(int64(maximum) + 1)) //nolint:gosec // Scheduling jitter is not a security token.
}

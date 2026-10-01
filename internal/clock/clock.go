// Package clock is the injectable time source for code whose decisions depend
// on elapsed time (supervisor ownership validity, heartbeat cadence, liveness
// scans, kill deadlines). Production code uses Real; deterministic simulations
// use Fake, which only moves when the test advances it.
package clock

import "time"

// Clock is the subset of the time package the supervisor's time-dependent
// paths need.
type Clock interface {
	Now() time.Time
	NewTimer(d time.Duration) Timer
	NewTicker(d time.Duration) Ticker
	// After is NewTimer(d).C() for one-shot waits whose timer is never
	// stopped early.
	After(d time.Duration) <-chan time.Time
}

// Timer mirrors *time.Timer behind an interface.
type Timer interface {
	C() <-chan time.Time
	Stop() bool
	Reset(d time.Duration) bool
}

// Ticker mirrors *time.Ticker behind an interface.
type Ticker interface {
	C() <-chan time.Time
	Stop()
}

// Real is the wall/monotonic clock from the time package.
var Real Clock = realClock{}

type realClock struct{}

func (realClock) Now() time.Time                         { return time.Now() }
func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }
func (realClock) NewTimer(d time.Duration) Timer         { return realTimer{time.NewTimer(d)} }
func (realClock) NewTicker(d time.Duration) Ticker       { return realTicker{time.NewTicker(d)} }

type realTimer struct{ t *time.Timer }

func (r realTimer) C() <-chan time.Time        { return r.t.C }
func (r realTimer) Stop() bool                 { return r.t.Stop() }
func (r realTimer) Reset(d time.Duration) bool { return r.t.Reset(d) }

type realTicker struct{ t *time.Ticker }

func (r realTicker) C() <-chan time.Time { return r.t.C }
func (r realTicker) Stop()               { r.t.Stop() }

// Or returns c, or Real when c is nil, so a zero-valued owner struct keeps
// production behavior.
func Or(c Clock) Clock {
	if c == nil {
		return Real
	}
	return c
}

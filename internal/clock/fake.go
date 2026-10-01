package clock

import (
	"sync"
	"time"
)

// Fake is a deterministic Clock. Time moves only through Advance/Set; timers
// and tickers fire in deadline order (ties broken by creation order) and never
// consult the wall clock. Channel sends are non-blocking with capacity 1, as
// for the time package, so a slow receiver drops ticks instead of wedging the
// clock.
type Fake struct {
	mu     sync.Mutex
	cond   *sync.Cond
	now    time.Time
	seq    uint64
	timers map[*fakeTimer]struct{}
}

// NewFake returns a Fake clock reading start. Pass a fixed instant so runs are
// reproducible; the fake carries no monotonic reading.
func NewFake(start time.Time) *Fake {
	f := &Fake{now: start.Round(0), timers: map[*fakeTimer]struct{}{}}
	f.cond = sync.NewCond(&f.mu)
	return f
}

// Now returns the fake instant.
func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

// After returns a channel that receives once the fake clock reaches now+d.
func (f *Fake) After(d time.Duration) <-chan time.Time { return f.NewTimer(d).C() }

// NewTimer arms a one-shot timer at now+d.
func (f *Fake) NewTimer(d time.Duration) Timer { return f.arm(d, 0) }

// NewTicker arms a periodic ticker. It panics on a non-positive period, as
// time.NewTicker does.
func (f *Fake) NewTicker(d time.Duration) Ticker {
	if d <= 0 {
		panic("clock: non-positive interval for NewTicker")
	}
	return fakeTicker{f.arm(d, d)}
}

func (f *Fake) arm(d, period time.Duration) *fakeTimer {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	t := &fakeTimer{f: f, ch: make(chan time.Time, 1), period: period, seq: f.seq, deadline: f.now.Add(d)}
	f.timers[t] = struct{}{}
	f.cond.Broadcast()
	if d <= 0 && period == 0 {
		f.fireLocked(t)
	}
	return t
}

// Advance moves the clock forward by d, firing every timer and ticker whose
// deadline falls inside the window, earliest first.
func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.advanceToLocked(f.now.Add(d))
}

// Set moves the clock to t (never backwards; an earlier t is ignored).
func (f *Fake) Set(t time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if t.After(f.now) {
		f.advanceToLocked(t.Round(0))
	}
}

func (f *Fake) advanceToLocked(target time.Time) {
	for {
		next := f.nextDueLocked(target)
		if next == nil {
			break
		}
		f.now = next.deadline
		f.fireLocked(next)
	}
	f.now = target
}

func (f *Fake) nextDueLocked(target time.Time) *fakeTimer {
	var best *fakeTimer
	for t := range f.timers {
		if t.deadline.After(target) {
			continue
		}
		if best == nil || t.deadline.Before(best.deadline) || (t.deadline.Equal(best.deadline) && t.seq < best.seq) {
			best = t
		}
	}
	return best
}

func (f *Fake) fireLocked(t *fakeTimer) {
	select {
	case t.ch <- f.now:
	default:
	}
	if t.period > 0 {
		t.deadline = t.deadline.Add(t.period)
		return
	}
	delete(f.timers, t)
}

// Waiters reports how many timers/tickers are currently armed.
func (f *Fake) Waiters() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.timers)
}

// BlockUntilWaiters blocks until at least n timers/tickers are armed. It is the
// synchronization point that lets a test advance time only after a goroutine
// has parked on the clock, with no real sleep.
func (f *Fake) BlockUntilWaiters(n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for len(f.timers) < n {
		f.cond.Wait()
	}
}

type fakeTimer struct {
	f        *Fake
	ch       chan time.Time
	period   time.Duration
	seq      uint64
	deadline time.Time
}

func (t *fakeTimer) C() <-chan time.Time { return t.ch }

type fakeTicker struct{ t *fakeTimer }

func (k fakeTicker) C() <-chan time.Time { return k.t.ch }
func (k fakeTicker) Stop()               { k.t.Stop() }

func (t *fakeTimer) Stop() bool {
	t.f.mu.Lock()
	defer t.f.mu.Unlock()
	_, armed := t.f.timers[t]
	delete(t.f.timers, t)
	t.f.cond.Broadcast()
	return armed
}

func (t *fakeTimer) Reset(d time.Duration) bool {
	t.f.mu.Lock()
	defer t.f.mu.Unlock()
	_, armed := t.f.timers[t]
	t.f.seq++
	t.seq = t.f.seq
	t.deadline = t.f.now.Add(d)
	t.f.timers[t] = struct{}{}
	t.f.cond.Broadcast()
	if d <= 0 && t.period == 0 {
		t.f.fireLocked(t)
	}
	return armed
}

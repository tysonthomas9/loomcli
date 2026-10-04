package loomagent

import (
	"context"
	"errors"
	"slices"
	"time"
)

// loop is one of a Service's running background loops (the dispatcher, a
// harness feed) that Drain waits on. The loop takes a Drain request only
// between items, so the item it was handling is done, and answers it once
// it has handled every item already queued for it, with how many items it
// handled since its last answer: one in flight when the request came counts.
type loop struct {
	drain   chan chan int // a Drain request, answered with handled
	stopped chan struct{} // closed when the loop ends
	handled int           // items since the last answer; only the loop's goroutine touches it
}

// startLoop registers a loop for Drain; stopLoop ends it. Its start-up work
// counts as an item, so the first Drain asks it twice.
func (s *Service) startLoop() *loop {
	l := &loop{drain: make(chan chan int), stopped: make(chan struct{}), handled: 1}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loops = append(s.loops, l)
	return l
}

func (s *Service) stopLoop(l *loop) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stoppedWork += l.handled // what it did since its last answer outlives it
	s.loops = slices.DeleteFunc(s.loops, func(x *loop) bool { return x == l })
	close(l.stopped)
}

// took counts one item the loop handled outside a Drain request: an event,
// a retry tick, a backoff that ended.
func (l *loop) took() { l.handled++ }

// answer answers the Drain request req with the items handled since the
// last answer, plus extra.
func (l *loop) answer(req chan<- int, extra int) {
	req <- l.handled + extra
	l.handled = 0
}

// ErrDrainInLoop is Drain called from inside a loop's own work, which would
// wait forever: the loop answers only between items.
var ErrDrainInLoop = errors.New("loomagent: Drain called from inside a drained loop")

// inLoop marks the ctx a loop hands its work, so Drain can refuse it.
type inLoop struct{}

// within is ctx as l's work sees it.
func within(ctx context.Context) context.Context { return context.WithValue(ctx, inLoop{}, true) }

// Drain returns once every running background loop of s has handled
// everything queued for it, the item each was handling included, and none
// queued more for another meanwhile (T3's DrainableWorker.drain): it asks
// each loop in turn, in the order they started, until one full round finds
// that no loop handled anything since its previous answer, a loop that
// stopped meanwhile included (stopLoop keeps its count). Any work one loop
// queued for another came from an item it handled, so it shows in that
// round. A loop still starting (the dispatcher's start-up sweep) is waited
// for. Tests use it instead of sleeping. A loop's own work must not call
// it, as the loop answers only between items: with the ctx the loop handed
// that work, Drain fails at once with ErrDrainInLoop.
func (s *Service) Drain(ctx context.Context) error {
	if ctx.Value(inLoop{}) != nil {
		return ErrDrainInLoop
	}
	s.mu.Lock()
	seen := s.stoppedWork
	s.mu.Unlock()
	for {
		s.mu.Lock()
		loops := slices.Clone(s.loops)
		s.mu.Unlock()
		handled := 0
		for _, l := range loops {
			n, err := l.wait(ctx)
			if err != nil {
				return err
			}
			handled += n
		}
		s.mu.Lock() // work of loops that stopped since the last round counts too
		handled += s.stoppedWork - seen
		seen = s.stoppedWork
		s.mu.Unlock()
		if handled == 0 {
			return nil
		}
	}
}

// wait hands l a Drain request and returns its answer; a loop that ends
// first handled nothing more.
func (l *loop) wait(ctx context.Context) (int, error) {
	req := make(chan int, 1)
	select {
	case l.drain <- req:
	case <-l.stopped:
		return 0, nil
	case <-ctx.Done():
		return 0, ctx.Err()
	}
	select {
	case n := <-req:
		return n, nil
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

// settle answers l's Drain request req once handle has taken every item
// already queued on ch. It returns false when ch closed or handle did
// (returned false), so the loop ends as it would have on its own; a close
// counts as an item, so Drain asks again once the loop has moved on.
func settle[T any](l *loop, req chan<- int, ch <-chan T, handle func(T) bool) bool {
	defer func() { l.answer(req, 0) }()
	for {
		select {
		case v, ok := <-ch:
			l.took()
			if !ok {
				return false
			}
			if !handle(v) {
				return false
			}
		default:
			return true
		}
	}
}

// ticker is the clock a background loop's periodic work runs on: a channel
// of ticks and its stop. Tests inject one to tick by hand.
type ticker func(time.Duration) (<-chan time.Time, func())

// realTicker is time.NewTicker.
func realTicker(d time.Duration) (<-chan time.Time, func()) {
	t := time.NewTicker(d)
	return t.C, t.Stop
}

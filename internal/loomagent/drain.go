package loomagent

import (
	"context"
	"time"
)

// loop is one of a Service's running background loops (the dispatcher, a
// harness feed) that Drain waits on. The loop takes a Drain request only
// between items, so the item it was handling is done, and answers it once
// it has handled every item already queued for it.
type loop struct {
	drain   chan chan int // a Drain request, answered with how many items it handled
	stopped chan struct{} // closed when the loop ends
}

// startLoop registers a loop for Drain; stopLoop ends it.
func (s *Service) startLoop() *loop {
	l := &loop{drain: make(chan chan int), stopped: make(chan struct{})}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loops[l] = struct{}{}
	return l
}

func (s *Service) stopLoop(l *loop) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.loops, l)
	close(l.stopped)
}

// Drain returns once every running background loop of s has handled
// everything queued for it, the item each was handling included, and none
// queued more for another meanwhile (T3's DrainableWorker.drain). A loop
// still starting (the dispatcher's start-up sweep) is waited for. Tests use
// it instead of sleeping.
func (s *Service) Drain(ctx context.Context) error {
	for {
		s.mu.Lock()
		loops := make([]*loop, 0, len(s.loops))
		for l := range s.loops {
			loops = append(loops, l)
		}
		s.mu.Unlock()
		handled := 0
		for _, l := range loops {
			n, err := l.wait(ctx)
			if err != nil {
				return err
			}
			handled += n
		}
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

// settle answers the Drain request req once handle has taken every item
// already queued on ch. It returns false when ch closed or handle did
// (returned false), so the loop ends as it would have on its own; a close
// counts as an item, so Drain asks again once the loop has moved on.
func settle[T any](req chan<- int, ch <-chan T, handle func(T) bool) bool {
	n := 0
	defer func() { req <- n }()
	for {
		select {
		case v, ok := <-ch:
			if !ok {
				n++
				return false
			}
			n++
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

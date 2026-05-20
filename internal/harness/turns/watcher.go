package turns

import (
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/harness/screen"
	"github.com/tysonthomas9/loomcli/internal/harness/wrapper"
)

// Watcher composes a wrapper.Session, a screen.Screen, and an Adapter
// into a single <-chan Event stream.
//
// Typical use:
//
//	scr := screen.New(120, 40)
//	cfg := wrapper.Config{ ..., Stdout: scr } // or use sess.AttachOutput(scr)
//	sess, _ := wrapper.Start(ctx, cfg)
//	w := turns.Watch(sess, scr, generic.New())
//	defer w.Close()
//	for ev := range w.Events() {
//	    ...
//	}
//
// The events channel is closed after both source streams (the wrapper's
// SessionEvent channel and the screen's subscription) are exhausted —
// i.e. after Session terminates AND Close() is called.
type Watcher struct {
	events chan Event

	closeOnce sync.Once
	done      chan struct{}

	wg sync.WaitGroup
}

// Watch starts a Watcher. It does not consume the session's Wait/Stop
// methods; the caller still owns those. Watch is non-blocking; the
// event-pumping goroutines run in the background until both sources
// stop AND Close() is called.
//
// Pass nil for scr to skip screen-derived signals (e.g. when using an
// adapter that only consumes wrapper.Status).
//
//nolint:gocognit,funlen // The watcher fans out across screen/wrapper/turn event streams; complexity and length reflect the multi-source dispatch, not poor structure. Mirrors upstream harness-wrapper.
func Watch(sess *wrapper.Session, scr *screen.Screen, adapter Adapter) *Watcher {
	w := &Watcher{
		events: make(chan Event, 32),
		done:   make(chan struct{}),
	}

	// Pump 1: wrapper session events → adapter.OnWrapperStatus
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		for ev := range sess.Events() {
			for _, te := range adapter.OnWrapperStatus(ev.Status, ev.Reason) {
				if te.At.IsZero() {
					te.At = ev.At
				}
				// Enrich adapter-returned events with the structured
				// fields the adapter contract doesn't see. Adapters
				// can still set these explicitly for screen-derived
				// events (rare); we only overwrite zero values.
				if te.HTTPCode == 0 {
					te.HTTPCode = ev.HTTPCode
				}
				if te.RetryAfter == 0 {
					te.RetryAfter = ev.RetryAfter
				}
				w.send(te)
			}
			if ev.Terminated {
				return
			}
		}
	}()

	// Pump 2: screen subscription → adapter.OnScreen
	if scr != nil {
		notifyCh, unsubscribe := scr.Subscribe()
		w.wg.Add(1)
		go func() {
			defer w.wg.Done()
			defer unsubscribe()
			for {
				select {
				case <-w.done:
					return
				case _, ok := <-notifyCh:
					if !ok {
						return
					}
					snap := scr.Snapshot()
					for _, te := range adapter.OnScreen(snap) {
						if te.At.IsZero() {
							te.At = time.Now()
						}
						if te.Snap == nil {
							s := snap
							te.Snap = &s
						}
						w.send(te)
					}
				}
			}
		}()
	}

	// Closer: drain both pumps then close events.
	go func() {
		w.wg.Wait()
		close(w.events)
	}()

	return w
}

// Events returns the channel of turn events. It is closed after both
// the wrapper session and the watcher itself have terminated.
func (w *Watcher) Events() <-chan Event { return w.events }

// Close signals the screen-pumping goroutine to stop. It does not stop
// the wrapper session; the caller owns sess.Stop. Safe to call
// multiple times.
func (w *Watcher) Close() error {
	w.closeOnce.Do(func() { close(w.done) })
	return nil
}

func (w *Watcher) send(ev Event) {
	select {
	case w.events <- ev:
	case <-w.done:
	}
}

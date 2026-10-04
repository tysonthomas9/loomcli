package loomagent

import (
	"context"
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
)

// TestDrainSeesWorkQueuedByAnInFlightItem: loop A is asked first and
// answers; loop B's item, in flight when Drain began, then queues an item
// for A and ends before B answers. Drain must not return with A's item still
// queued: B's in-flight item counts in B's answer, so Drain asks A again.
// A takes items only inside a Drain request, so an early return leaves its
// item unhandled for sure, with no timing involved.
func TestDrainSeesWorkQueuedByAnInFlightItem(t *testing.T) {
	s := New(ServiceConfig{})
	a, b := s.startLoop(), s.startLoop() // Drain asks a first
	aItems, bItems := make(chan string, 1), make(chan string, 1)
	var aHandled []string               // read only after the loops end
	aAnswered := make(chan struct{}, 8) // one per Drain request a answers
	bEntered, gate := make(chan struct{}), make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	aDone, bDone := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(aDone)
		defer s.stopLoop(a)
		for {
			select {
			case <-ctx.Done():
				return
			case req := <-a.drain:
				settle(a, req, aItems, func(v string) bool { aHandled = append(aHandled, v); return true })
				aAnswered <- struct{}{}
			}
		}
	}()
	go func() {
		defer close(bDone)
		defer s.stopLoop(b)
		handle := func(string) bool {
			close(bEntered)
			<-gate
			aItems <- "from b" // as a feed event publishes agent.idle to the dispatcher
			return true
		}
		for {
			select {
			case <-ctx.Done():
				return
			case req := <-b.drain:
				settle(b, req, bItems, handle)
			case v := <-bItems:
				b.took()
				handle(v)
			}
		}
	}()
	defer func() { cancel(); <-aDone; <-bDone }()

	if err := s.Drain(ctx); err != nil { // the loops' start-up is settled: from here a round sees only new work
		t.Fatal(err)
	}
	for range len(aAnswered) {
		<-aAnswered
	}
	bItems <- "b's item"
	<-bEntered // b's item is in flight
	drained := make(chan error, 1)
	go func() { drained <- s.Drain(ctx) }()
	<-aAnswered // a answered first, before b's item queued anything for it
	close(gate)
	if err := <-drained; err != nil {
		t.Fatal(err)
	}
	cancel()
	<-aDone
	if len(aHandled) != 1 || aHandled[0] != "from b" {
		t.Fatalf("Drain returned with a's item unhandled: a handled %v", aHandled)
	}
}

// drainOnFeed calls Drain from inside RunFeed, as it opens the feed.
type drainOnFeed struct {
	loomharness.Harness
	s   *Service
	got chan error
}

func (d drainOnFeed) Feed(ctx context.Context) (loomharness.Feed, error) {
	select {
	case d.got <- d.s.Drain(ctx):
	default:
	}
	return d.Harness.Feed(ctx)
}

// TestDrainInsideLoopFailsFast: Drain from inside a loop's own work, with
// the ctx that work was handed, fails at once with ErrDrainInLoop instead
// of waiting forever on the loop that runs it; the loop goes on.
func TestDrainInsideLoopFailsFast(t *testing.T) {
	e := newCreateEnv(t)
	s := e.service(ServiceConfig{})
	got := make(chan error, 1)
	s.harnesses["opencode"] = drainOnFeed{e.h, s, got}
	runFeed(t, s, "opencode")
	if err := <-got; !errors.Is(err, ErrDrainInLoop) {
		t.Fatalf("Drain inside RunFeed = %v; want ErrDrainInLoop", err)
	}
	settled(t, s) // the feed is not stuck
}

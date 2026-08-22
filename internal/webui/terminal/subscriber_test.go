package terminal

import (
	"runtime"
	"testing"
	"time"
)

func TestSubscriberCloseAfterQueueTerminatesWithoutReceiver(t *testing.T) {
	baseline := runtime.NumGoroutine()
	s := newSubscriber("test", 80, 24, 1)
	if !s.enqueue(TerminalEvent{Kind: EventOutput, Data: []byte("queued")}) {
		t.Fatal("enqueue failed")
	}
	s.closeAfterQueue(CloseExited)
	time.AfterFunc(10*time.Millisecond, func() { s.closeImmediate(CloseExited) })

	select {
	case <-s.closedCh:
	case <-time.After(time.Second):
		t.Fatal("subscriber pump did not terminate")
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= baseline+1 {
			return
		}
		runtime.Gosched()
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("subscriber pump goroutine leaked: baseline=%d current=%d", baseline, runtime.NumGoroutine())
}

func TestSubscriberCloseAfterQueueDrainsEveryEvent(t *testing.T) {
	for attempt := 0; attempt < 100; attempt++ {
		s := newSubscriber("test", 80, 24, 1)
		const events = 5
		for i := 0; i < events; i++ {
			if !s.enqueue(TerminalEvent{Sequence: uint64(i + 1), Kind: EventOutput, Data: []byte("x")}) {
				t.Fatal("enqueue failed")
			}
		}
		s.closeAfterQueue(CloseExited)
		got := 0
		for event := range s.output {
			if event.Sequence != uint64(got+1) {
				t.Fatalf("attempt %d sequence=%d want %d", attempt, event.Sequence, got+1)
			}
			got++
			time.Sleep(time.Microsecond)
		}
		if got != events {
			t.Fatalf("attempt %d received %d events, want %d", attempt, got, events)
		}
	}
}

func TestSubscriberDetachAfterSessionCloseStopsDrain(t *testing.T) {
	s := newSubscriber("test", 80, 24, 1)
	if !s.enqueue(TerminalEvent{Kind: EventOutput, Data: []byte("queued")}) {
		t.Fatal("enqueue failed")
	}
	s.closeAfterQueue(CloseExited)
	s.closeImmediate(CloseReplaced)
	select {
	case <-s.closedCh:
	case <-time.After(time.Second):
		t.Fatal("subscriber did not stop after detach")
	}
}

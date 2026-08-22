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

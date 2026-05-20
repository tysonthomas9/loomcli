package screen

import (
	"strings"
	"sync"
	"testing"
	"time"
)

func TestWriteAndSnapshot(t *testing.T) {
	s := New(40, 10)
	s.Write([]byte("\x1b[2J\x1b[Hhello \x1b[1mworld\x1b[0m"))
	snap := s.Snapshot()
	if !strings.Contains(snap.Text, "hello world") {
		t.Fatalf("expected snapshot to contain 'hello world', got: %q", snap.Text)
	}
	if snap.Generation != 1 {
		t.Fatalf("expected Generation=1, got %d", snap.Generation)
	}
	if snap.Cols != 40 || snap.Rows != 10 {
		t.Fatalf("expected 40x10, got %dx%d", snap.Cols, snap.Rows)
	}
}

func TestSubscribeSignalsOnWrite(t *testing.T) {
	s := New(40, 10)
	ch, unsub := s.Subscribe()
	defer unsub()

	s.Write([]byte("hi"))

	select {
	case <-ch:
		// ok
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected subscribe channel to fire after Write")
	}
}

func TestSubscribeCoalesces(t *testing.T) {
	s := New(40, 10)
	ch, unsub := s.Subscribe()
	defer unsub()

	for i := 0; i < 100; i++ {
		s.Write([]byte("x"))
	}
	// Drain: there should be exactly one pending signal regardless of write count.
	<-ch
	select {
	case <-ch:
		t.Fatal("expected coalesced signals, but a second one was pending")
	case <-time.After(20 * time.Millisecond):
		// ok
	}
}

func TestUnsubscribeStopsDelivery(t *testing.T) {
	s := New(40, 10)
	ch, unsub := s.Subscribe()
	unsub()

	s.Write([]byte("x"))
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("expected channel to be closed after unsubscribe")
		}
	case <-time.After(20 * time.Millisecond):
		// channel closed-and-drained is acceptable; the key invariant
		// is that no further values are delivered.
	}
}

func TestConcurrentWritesAreSafe(t *testing.T) {
	s := New(80, 24)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				s.Write([]byte("abcdefghij"))
				_ = s.Snapshot()
			}
		}()
	}
	wg.Wait()
	if g := s.Generation(); g != 400 {
		t.Fatalf("expected Generation=400, got %d", g)
	}
}

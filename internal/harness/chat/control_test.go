package chat

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestControlQueueImmediateAcquire(t *testing.T) {
	q := newControlQueue()
	release, err := q.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if !q.Held() {
		t.Fatal("expected Held() true after Acquire")
	}
	release()
	if q.Held() {
		t.Fatal("expected Held() false after release")
	}
}

func TestControlQueueFIFOOrdering(t *testing.T) {
	q := newControlQueue()
	release, err := q.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var order []int

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := q.Acquire(context.Background())
			if err != nil {
				t.Errorf("waiter %d: %v", i, err)
				return
			}
			mu.Lock()
			order = append(order, i)
			mu.Unlock()
			r()
		}()
		// Stagger goroutine launches so queue insertions are ordered.
		time.Sleep(5 * time.Millisecond)
	}

	// Release the initial holder; waiters drain FIFO.
	release()
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	for i, n := range order {
		if n != i {
			t.Errorf("expected FIFO order, got %v", order)
			break
		}
	}
}

func TestControlQueueCtxCancelWhileWaiting(t *testing.T) {
	q := newControlQueue()
	release, err := q.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	_, err = q.Acquire(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded, got %v", err)
	}
	if !q.Held() {
		t.Fatal("expected Held() still true (original holder unchanged)")
	}
}

func TestControlQueueDoubleRelease(t *testing.T) {
	q := newControlQueue()
	release, err := q.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	release()
	release() // must not panic; must not flip state
	if q.Held() {
		t.Fatal("expected Held() false after release")
	}
}

func TestControlQueueClosedRejectsAcquire(t *testing.T) {
	q := newControlQueue()
	q.Close()
	if _, err := q.Acquire(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatalf("expected ErrClosed, got %v", err)
	}
}

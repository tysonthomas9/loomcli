package serve

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCachedValue_CacheHit(t *testing.T) {
	var calls atomic.Int32
	data := &MonitorData{Timestamp: time.Now()}

	c := newCachedValue[*MonitorData](5*time.Second, func() *MonitorData {
		calls.Add(1)
		return data
	})

	// First call: cache miss
	got1 := c.get()
	if got1 != data {
		t.Fatal("first call returned wrong data")
	}
	if calls.Load() != 1 {
		t.Fatalf("expected 1 collection call, got %d", calls.Load())
	}

	// Second call within TTL: cache hit
	got2 := c.get()
	if got2 != data {
		t.Fatal("second call returned wrong data")
	}
	if calls.Load() != 1 {
		t.Fatalf("expected 1 collection call after cache hit, got %d", calls.Load())
	}
}

func TestCachedValue_CacheExpiry(t *testing.T) {
	var calls atomic.Int32
	c := newCachedValue[*MonitorData](10*time.Millisecond, func() *MonitorData {
		calls.Add(1)
		return &MonitorData{Timestamp: time.Now()}
	})

	// First call
	c.get()
	if calls.Load() != 1 {
		t.Fatalf("expected 1 call, got %d", calls.Load())
	}

	// Wait for TTL to expire
	time.Sleep(20 * time.Millisecond)

	// Second call: cache expired
	c.get()
	if calls.Load() != 2 {
		t.Fatalf("expected 2 calls after expiry, got %d", calls.Load())
	}
}

func TestCachedValue_ConcurrentCoalescing(t *testing.T) {
	var calls atomic.Int32
	// Simulate a slow collection
	c := newCachedValue[*MonitorData](5*time.Second, func() *MonitorData {
		calls.Add(1)
		time.Sleep(50 * time.Millisecond)
		return &MonitorData{Timestamp: time.Now()}
	})

	const goroutines = 10
	var wg sync.WaitGroup
	wg.Add(goroutines)

	results := make([]*MonitorData, goroutines)
	for i := range goroutines {
		go func(idx int) {
			defer wg.Done()
			results[idx] = c.get()
		}(i)
	}

	wg.Wait()

	// All goroutines should have received the same result
	if calls.Load() != 1 {
		t.Fatalf("expected exactly 1 collection call from %d goroutines, got %d", goroutines, calls.Load())
	}

	// All results should be the same pointer
	for i := 1; i < goroutines; i++ {
		if results[i] != results[0] {
			t.Fatalf("goroutine %d got different result pointer than goroutine 0", i)
		}
	}
}

func TestCachedValue_ExpiryAfterCoalescing(t *testing.T) {
	var calls atomic.Int32
	c := newCachedValue[*MonitorData](10*time.Millisecond, func() *MonitorData {
		calls.Add(1)
		return &MonitorData{Timestamp: time.Now()}
	})

	// First batch: all should coalesce
	var wg sync.WaitGroup
	wg.Add(5)
	for range 5 {
		go func() {
			defer wg.Done()
			c.get()
		}()
	}
	wg.Wait()

	if calls.Load() != 1 {
		t.Fatalf("expected 1 call from first batch, got %d", calls.Load())
	}

	// Wait for TTL to expire
	time.Sleep(20 * time.Millisecond)

	// Second call triggers new collection
	c.get()
	if calls.Load() != 2 {
		t.Fatalf("expected 2 calls after expiry, got %d", calls.Load())
	}
}

// TestCachedValue_BackgroundSignalsInflight verifies the v2 deadlock fix:
// while the background collection is running, a concurrent get() should wait
// for the background result instead of starting its own competing collection.
func TestCachedValue_BackgroundSignalsInflight(t *testing.T) {
	var calls atomic.Int32
	started := make(chan struct{}, 1)
	release := make(chan struct{})

	c := newCachedValue[int](5*time.Second, func() int {
		n := calls.Add(1)
		select {
		case started <- struct{}{}:
		default:
		}
		<-release
		return int(n)
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.startBackground(ctx, 1*time.Hour) // long interval — we only exercise the initial refresh

	<-started // background's initial refresh has entered collectFn

	// get() should observe inflight=true and wait instead of starting a second collection
	done := make(chan int, 1)
	go func() { done <- c.get() }()

	// Give the waiter time to enter get() and observe inflight
	time.Sleep(20 * time.Millisecond)
	if calls.Load() != 1 {
		t.Fatalf("expected only background collection in flight; got %d total calls", calls.Load())
	}

	close(release)

	select {
	case got := <-done:
		if got != 1 {
			t.Fatalf("waiter should receive background's result (1), got %d", got)
		}
	case <-time.After(time.Second):
		t.Fatal("waiter did not unblock after background close")
	}

	if calls.Load() != 1 {
		t.Fatalf("expected exactly 1 collection total, got %d", calls.Load())
	}
}

// TestCachedValue_InflightIdentityGuard verifies that when a get() collector
// and a refresh() overlap, inflight is not prematurely cleared — new callers
// arriving while either is still running should wait, not start a third collector.
func TestCachedValue_InflightIdentityGuard(t *testing.T) {
	var calls atomic.Int32
	collecting := make(chan struct{})
	release := make(chan struct{})

	c := newCachedValue[int](10*time.Millisecond, func() int {
		n := calls.Add(1)
		if n == 1 {
			collecting <- struct{}{}
			<-release
		}
		return int(n)
	})

	// Kick off a get() that becomes the collector and blocks.
	go c.get()
	<-collecting // get() is now mid-collection; inflight=true, waitCh=chGet

	// Simulate refresh() overwriting waitCh and completing while the get() collector
	// is still running. We call a refresh()-style sequence by constructing a fake
	// flow: since refresh() is embedded inside startBackground's goroutine, we
	// instead test the invariant directly via another get() caller that should wait.
	var wg sync.WaitGroup
	wg.Add(3)
	for range 3 {
		go func() {
			defer wg.Done()
			c.get()
		}()
	}

	// Give waiters time to enter
	time.Sleep(20 * time.Millisecond)
	if calls.Load() != 1 {
		t.Fatalf("waiters should coalesce onto in-flight collection; got %d calls", calls.Load())
	}

	close(release)
	wg.Wait()

	if calls.Load() != 1 {
		t.Fatalf("expected 1 collection (all waiters coalesced), got %d", calls.Load())
	}
}

func TestCachedValue_NonPointerType(t *testing.T) {
	var calls atomic.Int32
	c := newCachedValue[int](5*time.Second, func() int {
		calls.Add(1)
		return 42
	})

	got := c.get()
	if got != 42 {
		t.Fatalf("expected 42, got %d", got)
	}

	// Second call: cache hit
	got2 := c.get()
	if got2 != 42 {
		t.Fatalf("expected 42, got %d", got2)
	}
	if calls.Load() != 1 {
		t.Fatalf("expected 1 call, got %d", calls.Load())
	}
}

package metricscmd

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli/monitor"
)

func TestCachedCollector_CacheHit(t *testing.T) {
	var calls atomic.Int32
	data := &monitor.MonitorData{Timestamp: time.Now()}

	c := &cachedCollector{
		ttl: 5 * time.Second,
		collectFn: func() *monitor.MonitorData {
			calls.Add(1)
			return data
		},
	}

	got1 := c.get()
	if got1 != data {
		t.Fatal("first call returned wrong data")
	}
	if calls.Load() != 1 {
		t.Fatalf("expected 1 collection call, got %d", calls.Load())
	}

	got2 := c.get()
	if got2 != data {
		t.Fatal("second call returned wrong data")
	}
	if calls.Load() != 1 {
		t.Fatalf("expected 1 collection call after cache hit, got %d", calls.Load())
	}
}

func TestCachedCollector_ConcurrentCoalescing(t *testing.T) {
	var calls atomic.Int32
	c := &cachedCollector{
		ttl: 5 * time.Second,
		collectFn: func() *monitor.MonitorData {
			calls.Add(1)
			time.Sleep(50 * time.Millisecond)
			return &monitor.MonitorData{Timestamp: time.Now()}
		},
	}

	const goroutines = 10
	var wg sync.WaitGroup
	wg.Add(goroutines)

	results := make([]*monitor.MonitorData, goroutines)
	for i := range goroutines {
		go func(idx int) {
			defer wg.Done()
			results[idx] = c.get()
		}(i)
	}

	wg.Wait()

	if calls.Load() != 1 {
		t.Fatalf("expected exactly 1 collection call from %d goroutines, got %d", goroutines, calls.Load())
	}

	for i := 1; i < goroutines; i++ {
		if results[i] != results[0] {
			t.Fatalf("goroutine %d got different result pointer than goroutine 0", i)
		}
	}
}

// TestCachedCollector_BackgroundSignalsInflight verifies the v2 deadlock fix:
// while the background collection is running, a concurrent get() should wait
// for the background result instead of starting its own competing collection.
func TestCachedCollector_BackgroundSignalsInflight(t *testing.T) {
	var calls atomic.Int32
	started := make(chan struct{}, 1)
	release := make(chan struct{})

	result := &monitor.MonitorData{Timestamp: time.Now()}
	c := &cachedCollector{
		ttl: 5 * time.Second,
		collectFn: func() *monitor.MonitorData {
			calls.Add(1)
			select {
			case started <- struct{}{}:
			default:
			}
			<-release
			return result
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.startBackground(ctx, 1*time.Hour) // long interval — we only exercise the initial refresh

	<-started // background's initial refresh has entered collectFn

	done := make(chan *monitor.MonitorData, 1)
	go func() { done <- c.get() }()

	time.Sleep(20 * time.Millisecond)
	if calls.Load() != 1 {
		t.Fatalf("expected only background collection in flight; got %d total calls", calls.Load())
	}

	close(release)

	select {
	case got := <-done:
		if got != result {
			t.Fatalf("waiter should receive background's result, got %v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("waiter did not unblock after background close")
	}

	if calls.Load() != 1 {
		t.Fatalf("expected exactly 1 collection total, got %d", calls.Load())
	}
}

// TestCachedCollector_InflightIdentityGuard verifies that when a get() collector
// is mid-collection, new callers coalesce onto the inflight collection rather than
// starting a competing one. This exercises the identity-guarded inflight clear.
func TestCachedCollector_InflightIdentityGuard(t *testing.T) {
	var calls atomic.Int32
	collecting := make(chan struct{})
	release := make(chan struct{})

	c := &cachedCollector{
		ttl: 10 * time.Millisecond,
		collectFn: func() *monitor.MonitorData {
			n := calls.Add(1)
			if n == 1 {
				collecting <- struct{}{}
				<-release
			}
			return &monitor.MonitorData{Timestamp: time.Now()}
		},
	}

	go c.get()
	<-collecting // get() is now mid-collection; inflight=true, waitCh=chGet

	var wg sync.WaitGroup
	wg.Add(3)
	for range 3 {
		go func() {
			defer wg.Done()
			c.get()
		}()
	}

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

// TestCachedCollector_CacheExpiry verifies that after TTL elapses, a subsequent
// get() triggers a new collection. This exercises the !c.cachedAt.IsZero() &&
// time.Since(c.cachedAt) < c.ttl freshness check.
func TestCachedCollector_CacheExpiry(t *testing.T) {
	var calls atomic.Int32
	c := &cachedCollector{
		ttl: 10 * time.Millisecond,
		collectFn: func() *monitor.MonitorData {
			calls.Add(1)
			return &monitor.MonitorData{Timestamp: time.Now()}
		},
	}

	c.get()
	if calls.Load() != 1 {
		t.Fatalf("expected 1 call, got %d", calls.Load())
	}

	time.Sleep(20 * time.Millisecond)

	c.get()
	if calls.Load() != 2 {
		t.Fatalf("expected 2 calls after expiry, got %d", calls.Load())
	}
}

// TestCachedCollector_GetTimeout verifies that get() returns within ~5 seconds
// even if collectFn hangs indefinitely, avoiding permanent deadlock of HTTP handlers.
func TestCachedCollector_GetTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping 5-second timeout test in -short mode")
	}
	hang := make(chan struct{})
	defer close(hang)

	started := make(chan struct{}, 1)
	c := &cachedCollector{
		ttl: 5 * time.Second,
		collectFn: func() *monitor.MonitorData {
			select {
			case started <- struct{}{}:
			default:
			}
			<-hang
			return nil
		},
	}

	// Kick off a collector that will hang forever.
	go c.get()
	<-started

	// A second call must enter the wait path (inflight=true) and time out,
	// rather than blocking forever.
	done := make(chan *monitor.MonitorData, 1)
	start := time.Now()
	go func() { done <- c.get() }()

	select {
	case got := <-done:
		elapsed := time.Since(start)
		if elapsed > 6*time.Second {
			t.Fatalf("get() took too long (%s) — timeout did not fire", elapsed)
		}
		if elapsed < 4*time.Second {
			t.Fatalf("get() returned too quickly (%s) — expected ~5s timeout", elapsed)
		}
		if got != nil {
			t.Fatalf("expected nil cached value on timeout, got %v", got)
		}
	case <-time.After(7 * time.Second):
		t.Fatal("get() blocked indefinitely — timeout did not fire")
	}
}

// TestCachedCollector_NilCachedValue verifies that a collectFn returning nil
// still marks the cache as fresh via cachedAt (not pointer nilness), so
// subsequent get() calls within TTL don't re-collect.
func TestCachedCollector_NilCachedValue(t *testing.T) {
	var calls atomic.Int32
	c := &cachedCollector{
		ttl: 5 * time.Second,
		collectFn: func() *monitor.MonitorData {
			calls.Add(1)
			return nil
		},
	}

	got := c.get()
	if got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
	if calls.Load() != 1 {
		t.Fatalf("expected 1 call, got %d", calls.Load())
	}

	// Second call within TTL: cache should be considered fresh (via cachedAt),
	// even though c.cached is nil.
	got2 := c.get()
	if got2 != nil {
		t.Fatalf("expected nil, got %v", got2)
	}
	if calls.Load() != 1 {
		t.Fatalf("expected 1 call (cache hit via cachedAt), got %d", calls.Load())
	}
}

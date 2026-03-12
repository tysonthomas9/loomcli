package cli

import (
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

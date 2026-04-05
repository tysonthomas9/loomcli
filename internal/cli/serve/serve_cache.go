package serve

import (
	"context"
	"sync"
	"time"
)

// cachedValue wraps a collection function with TTL caching and request
// coalescing (singleflight). When multiple HTTP handlers call get()
// concurrently, only one collection runs — the rest wait and share
// the same result.
type cachedValue[T any] struct {
	ttl       time.Duration
	collectFn func() T

	mu       sync.Mutex
	cached   T
	cachedAt time.Time
	inflight bool
	waitCh   chan struct{} // closed when inflight collection finishes
}

func newCachedValue[T any](ttl time.Duration, fn func() T) *cachedValue[T] {
	return &cachedValue[T]{
		ttl:       ttl,
		collectFn: fn,
	}
}

// startBackground starts a background goroutine that proactively refreshes
// the cache every interval, ensuring get() always finds warm data.
// The goroutine exits when ctx is canceled.
func (c *cachedValue[T]) startBackground(ctx context.Context, interval time.Duration) {
	// Run an immediate first collection to warm the cache on startup
	result := c.collectFn()
	c.mu.Lock()
	c.cached = result
	c.cachedAt = time.Now()
	c.mu.Unlock()

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				result := c.collectFn()
				c.mu.Lock()
				c.cached = result
				c.cachedAt = time.Now()
				// If anyone is waiting on inflight, signal them
				if c.inflight {
					c.inflight = false
					close(c.waitCh)
				}
				c.mu.Unlock()
			}
		}
	}()
}

// get returns cached data if fresh, otherwise triggers a single collection
// that all concurrent callers share.
func (c *cachedValue[T]) get() T {
	c.mu.Lock()

	// Fast path: cache is fresh
	if !c.cachedAt.IsZero() && time.Since(c.cachedAt) < c.ttl {
		data := c.cached
		c.mu.Unlock()
		return data
	}

	// If another goroutine is already collecting, wait for it
	if c.inflight {
		ch := c.waitCh
		c.mu.Unlock()
		<-ch // block until collection finishes
		c.mu.Lock()
		data := c.cached
		c.mu.Unlock()
		return data
	}

	// We are the collector
	c.inflight = true
	ch := make(chan struct{})
	c.waitCh = ch
	c.mu.Unlock()

	// Ensure waiters are always unblocked, even if collectFn panics.
	defer func() {
		c.mu.Lock()
		c.inflight = false
		close(ch)
		c.mu.Unlock()
	}()

	// Perform collection outside the lock
	data := c.collectFn()

	c.mu.Lock()
	c.cached = data
	c.cachedAt = time.Now()
	c.mu.Unlock()

	return data
}

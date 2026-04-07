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
	// Run the first collection and all subsequent ones in the background goroutine.
	// The first HTTP request hitting get() will trigger a singleflight collection
	// if the background hasn't finished yet — this avoids blocking server startup.
	go func() {
		// Helper: run collectFn and update cache atomically. Sets inflight/waitCh
		// so concurrent get() callers wait instead of starting competing collections.
		// Uses a local ch per invocation to avoid double-close if a get() collector
		// is already inflight when refresh() starts.
		refresh := func() {
			c.mu.Lock()
			c.inflight = true
			ch := make(chan struct{})
			c.waitCh = ch
			c.mu.Unlock()

			result := c.collectFn()
			collectedAt := time.Now()

			c.mu.Lock()
			// Don't roll back fresher data written by a concurrent get() collector.
			if collectedAt.After(c.cachedAt) {
				c.cached = result
				c.cachedAt = collectedAt
			}
			// Only clear inflight if we still own the current claim. A concurrent
			// get() collector may have overwritten c.waitCh with its own channel;
			// in that case we must not prematurely clear inflight or new callers
			// could start a third competing collection.
			if c.waitCh == ch {
				c.inflight = false
			}
			close(ch)
			c.mu.Unlock()
		}

		// Immediate first collection to warm the cache
		refresh()

		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				refresh()
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

	// If another goroutine is already collecting, wait for it (with timeout)
	if c.inflight {
		ch := c.waitCh
		c.mu.Unlock()
		timer := time.NewTimer(5 * time.Second)
		select {
		case <-ch: // collection finished
		case <-timer.C: // don't block HTTP handler indefinitely
		}
		timer.Stop()
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
	// Only clear inflight if we still own the current claim — a background
	// refresh() may have overwritten c.waitCh while we were collecting.
	defer func() {
		c.mu.Lock()
		if c.waitCh == ch {
			c.inflight = false
		}
		close(ch)
		c.mu.Unlock()
	}()

	// Perform collection outside the lock
	data := c.collectFn()
	collectedAt := time.Now()

	c.mu.Lock()
	// Only write if our result is newer than what's cached — a concurrent
	// background ticker may have written a fresher value while we were running.
	if collectedAt.After(c.cachedAt) {
		c.cached = data
		c.cachedAt = collectedAt
	} else {
		data = c.cached
	}
	c.mu.Unlock()

	return data
}

package cli

import (
	"sync"
	"time"
)

// cachedCollector wraps a monitor data collection function with TTL caching
// and request coalescing (singleflight). When multiple HTTP handlers call
// get() concurrently, only one collection runs — the rest wait and share
// the same result.
type cachedCollector struct {
	ttl       time.Duration
	collectFn func() *MonitorData

	mu       sync.Mutex
	cached   *MonitorData
	cachedAt time.Time
	inflight bool
	waitCh   chan struct{} // closed when inflight collection finishes
}

func newCachedCollector(ttl time.Duration, fn func() *MonitorData) *cachedCollector {
	return &cachedCollector{
		ttl:       ttl,
		collectFn: fn,
	}
}

// get returns cached data if fresh, otherwise triggers a single collection
// that all concurrent callers share.
func (c *cachedCollector) get() *MonitorData {
	c.mu.Lock()

	// Fast path: cache is fresh
	if c.cached != nil && time.Since(c.cachedAt) < c.ttl {
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
	c.waitCh = make(chan struct{})
	c.mu.Unlock()

	// Perform collection outside the lock
	data := c.collectFn()

	c.mu.Lock()
	c.cached = data
	c.cachedAt = time.Now()
	c.inflight = false
	close(c.waitCh) // wake all waiters
	c.mu.Unlock()

	return data
}

package metricscmd

import (
	"context"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli/monitor"
)

// CollectDataFn is a function that returns cached monitor data.
type CollectDataFn = func() *monitor.MonitorData

// NewCollector creates a cached monitor data collector with the given TTL.
// It uses singleflight-style coalescing: concurrent callers share one collection.
func NewCollector(ttl time.Duration) CollectDataFn {
	return NewCollectorFunc(ttl, func() *monitor.MonitorData { return monitor.CollectMonitorData(10000, "") })
}

// NewCollectorFunc creates a cached monitor data collector around collectFn.
func NewCollectorFunc(ttl time.Duration, collectFn CollectDataFn) CollectDataFn {
	if collectFn == nil {
		collectFn = func() *monitor.MonitorData { return monitor.CollectMonitorData(10000, "") }
	}
	cv := &cachedCollector{
		ttl:       ttl,
		collectFn: collectFn,
	}
	return cv.get
}

// NewCollectorWithBackground creates a cached monitor data collector with a
// background goroutine that proactively refreshes the cache every interval.
// HTTP handlers always read pre-warmed data, eliminating cache-miss latency.
// The background goroutine exits when ctx is canceled.
func NewCollectorWithBackground(ctx context.Context, ttl, interval time.Duration) CollectDataFn {
	return NewCollectorWithBackgroundFunc(ctx, ttl, interval, func() *monitor.MonitorData { return monitor.CollectMonitorData(10000, "") })
}

// NewCollectorWithBackgroundFunc creates a cached monitor data collector around
// collectFn and refreshes it every interval in the background.
func NewCollectorWithBackgroundFunc(ctx context.Context, ttl, interval time.Duration, collectFn CollectDataFn) CollectDataFn {
	if collectFn == nil {
		collectFn = func() *monitor.MonitorData { return monitor.CollectMonitorData(10000, "") }
	}
	cv := &cachedCollector{
		ttl:       ttl,
		collectFn: collectFn,
	}
	cv.startBackground(ctx, interval)
	return cv.get
}

// cachedCollector wraps a collection function with TTL caching and request
// coalescing. When multiple HTTP handlers call get() concurrently, only one
// collection runs — the rest wait and share the same result.
type cachedCollector struct {
	ttl       time.Duration
	collectFn func() *monitor.MonitorData

	mu       sync.Mutex
	cached   *monitor.MonitorData
	cachedAt time.Time
	inflight bool
	waitCh   chan struct{} // closed when inflight collection finishes
}

// startBackground starts a background goroutine that proactively refreshes
// the cache every interval, ensuring get() always finds warm data.
// The goroutine exits when ctx is canceled.
func (c *cachedCollector) startBackground(ctx context.Context, interval time.Duration) {
	// Run the first collection and all subsequent ones in the background goroutine.
	// The first HTTP request hitting get() will trigger a singleflight collection
	// if the background hasn't finished yet — this avoids blocking server startup.
	go func() {
		// Immediate first collection to warm the cache
		result := c.collectFn()
		collectedAt := time.Now()
		c.mu.Lock()
		// Don't roll back fresher data written by an in-flight get().
		// Don't touch inflight/waitCh — those are owned by the get()
		// collector and double-closing waitCh would panic.
		if collectedAt.After(c.cachedAt) {
			c.cached = result
			c.cachedAt = collectedAt
		}
		c.mu.Unlock()

		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				result := c.collectFn()
				collectedAt := time.Now()
				c.mu.Lock()
				// Don't roll back fresher data written by an in-flight get().
				// Don't touch inflight/waitCh — those are owned by the get()
				// collector and double-closing waitCh would panic.
				if collectedAt.After(c.cachedAt) {
					c.cached = result
					c.cachedAt = collectedAt
				}
				c.mu.Unlock()
			}
		}
	}()
}

// get returns cached data if fresh, otherwise triggers a single collection
// that all concurrent callers share.
func (c *cachedCollector) get() *monitor.MonitorData {
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

package metricscmd

import (
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli/monitor"
)

// CollectDataFn is a function that returns cached monitor data.
type CollectDataFn = func() *monitor.MonitorData

// NewCollector creates a cached monitor data collector with the given TTL.
// It uses singleflight-style coalescing: concurrent callers share one collection.
func NewCollector(ttl time.Duration) CollectDataFn {
	cv := &cachedCollector{
		ttl:       ttl,
		collectFn: func() *monitor.MonitorData { return monitor.CollectMonitorData(50, "") },
	}
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

	c.mu.Lock()
	c.cached = data
	c.cachedAt = time.Now()
	c.mu.Unlock()

	return data
}

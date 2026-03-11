package cli

import (
	"log"
	"sync"
)

// ConcurrencyTracker enforces per-role concurrency limits in the daemon.
// Roles without a configured MaxConcurrency (nil or 0) have no limit.
// Thread-safe via sync.Mutex + sync.Cond for blocking acquisition.
type ConcurrencyTracker struct {
	mu     sync.Mutex
	cond   *sync.Cond
	counts map[string]int // role -> current active count
	limits map[string]int // role -> max concurrent (0 = unlimited)
	closed bool           // set on shutdown to unblock all waiters
}

// NewConcurrencyTracker builds a tracker from the role config map.
// Roles with nil or zero MaxConcurrency get limit 0 (unlimited).
func NewConcurrencyTracker(roles map[string]RoleConfig) *ConcurrencyTracker {
	ct := &ConcurrencyTracker{
		counts: make(map[string]int),
		limits: make(map[string]int),
	}
	ct.cond = sync.NewCond(&ct.mu)

	for name, rc := range roles {
		if rc.MaxConcurrency != nil && *rc.MaxConcurrency > 0 {
			ct.limits[name] = *rc.MaxConcurrency
		}
	}
	return ct
}

// Acquire blocks until a concurrency slot is available for the role.
// Returns true if acquired, false if the tracker was closed (shutdown).
// Roles with no limit (0) always acquire immediately. Safe on nil receiver.
func (ct *ConcurrencyTracker) Acquire(role string) bool {
	if ct == nil {
		return true
	}
	ct.mu.Lock()
	defer ct.mu.Unlock()

	limit := ct.limits[role]
	if limit == 0 {
		if ct.closed {
			return false
		}
		ct.counts[role]++
		return true
	}

	for ct.counts[role] >= limit && !ct.closed {
		ct.cond.Wait()
	}

	if ct.closed {
		return false
	}

	ct.counts[role]++
	return true
}

// TryAcquire attempts a non-blocking acquire. Returns false if at limit. Safe on nil receiver.
func (ct *ConcurrencyTracker) TryAcquire(role string) bool {
	if ct == nil {
		return true
	}
	ct.mu.Lock()
	defer ct.mu.Unlock()

	if ct.closed {
		return false
	}

	limit := ct.limits[role]
	if limit == 0 {
		ct.counts[role]++
		return true
	}

	if ct.counts[role] >= limit {
		return false
	}

	ct.counts[role]++
	return true
}

// Release decrements the active count for a role and wakes waiters. Safe on nil receiver.
func (ct *ConcurrencyTracker) Release(role string) {
	if ct == nil {
		return
	}
	ct.mu.Lock()
	defer ct.mu.Unlock()

	if ct.counts[role] <= 0 {
		log.Printf("[concurrency] Warning: Release called for role %q with count %d (bug?)", role, ct.counts[role])
		ct.counts[role] = 0
		return
	}

	ct.counts[role]--
	ct.cond.Broadcast()
}

// ActiveCount returns the current active count for a role.
func (ct *ConcurrencyTracker) ActiveCount(role string) int {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	return ct.counts[role]
}

// Counts returns a copy of the counts map (for status/monitoring).
func (ct *ConcurrencyTracker) Counts() map[string]int {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	result := make(map[string]int, len(ct.counts))
	for k, v := range ct.counts {
		result[k] = v
	}
	return result
}

// Close sets the tracker to closed state and wakes all blocked Acquire callers.
// Safe to call multiple times (idempotent) and on nil receiver.
func (ct *ConcurrencyTracker) Close() {
	if ct == nil {
		return
	}
	ct.mu.Lock()
	defer ct.mu.Unlock()

	if ct.closed {
		return
	}
	ct.closed = true
	ct.cond.Broadcast()
}

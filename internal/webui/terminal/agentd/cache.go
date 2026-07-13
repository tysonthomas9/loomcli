package agentd

import (
	"sync"
	"time"
)

// cacheEntry stores everything AttachSession needs to set up a streaming
// connection to a persistent-agent's loom-agentd. The PEM-encoded mTLS cert +
// key are kept as strings (not as a parsed *tls.Certificate) so the
// AttachSession path can build a fresh tls.Config on each call without
// worrying about whether a parsed certificate is safe to share across
// goroutines.
type cacheEntry struct {
	vmHost     string
	agentdPort int32
	certPEM    string
	keyPEM     string
	expiresAt  time.Time
}

// expired reports whether now is at or past the entry's expiry. The cache
// always supplies time.Now() as now; tests inject a clock for determinism.
func (e cacheEntry) expired(now time.Time) bool {
	return !now.Before(e.expiresAt)
}

// cacheKey is the routing-cache lookup key. We treat workspace + agent as
// independent dimensions so two agents in the same workspace never collide.
type cacheKey struct {
	workspace string
	agent     string
}

// routingCache is a tiny in-memory mapping from (workspace, agent) to the
// last successful Resolve / EnsureAlive result, including a parsed cert + key
// pair that can be used to build a tls.Config. Entries expire after certTTL —
// always strictly less than the CA's actual cert validity (2 minutes today)
// so AttachSession re-mints with margin instead of racing the agentd's
// expiration check.
//
// All exported methods are safe for concurrent use. Get drops expired entries
// lazily on read — there's no background reaper because the cache is small,
// reads are the only hot path, and TTLs are short enough that a stale entry
// can't accumulate cost.
type routingCache struct {
	mu      sync.Mutex
	entries map[cacheKey]cacheEntry
	ttl     time.Duration
	now     func() time.Time
}

// newRoutingCache returns a cache that stamps Put entries with expiry =
// now + ttl. ttl must be positive; the AgentdClient constructor enforces
// this with a 90 s default.
func newRoutingCache(ttl time.Duration) *routingCache {
	return &routingCache{
		entries: make(map[cacheKey]cacheEntry),
		ttl:     ttl,
		now:     time.Now,
	}
}

// Get returns the cached entry for (ws, agent) and reports whether it was a
// live (non-expired) hit. An expired entry is removed in place so the next
// Put can write fresh values without a separate eviction pass.
func (c *routingCache) Get(ws, agent string) (cacheEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	k := cacheKey{workspace: ws, agent: agent}
	entry, ok := c.entries[k]
	if !ok {
		return cacheEntry{}, false
	}
	if entry.expired(c.now()) {
		delete(c.entries, k)
		return cacheEntry{}, false
	}
	return entry, true
}

// Put stores the routing + cert tuple under (ws, agent) with expiry
// computed against the cache's clock. Existing entries are overwritten.
func (c *routingCache) Put(ws, agent, vmHost string, agentdPort int32, certPEM, keyPEM string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[cacheKey{workspace: ws, agent: agent}] = cacheEntry{
		vmHost:     vmHost,
		agentdPort: agentdPort,
		certPEM:    certPEM,
		keyPEM:     keyPEM,
		expiresAt:  c.now().Add(c.ttl),
	}
}

// Invalidate drops the entry for (ws, agent) if present. Used when an
// AttachSession-level error suggests the cached cert / address is stale
// (e.g. agentd refused the cert) so the next call re-resolves.
func (c *routingCache) Invalidate(ws, agent string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, cacheKey{workspace: ws, agent: agent})
}

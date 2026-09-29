package prreview

import (
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/prreadiness"
)

// maxReadinessCacheEntries bounds the in-memory readiness cache. When it is
// exceeded, entries not observed within readinessCacheEvictAge are dropped.
const (
	maxReadinessCacheEntries = 5000
	readinessCacheEvictAge   = time.Hour
)

// readinessCache holds the last-known readiness snapshot per workspace PR.
// Snapshots are cache, not intent: they are never persisted or evented.
type readinessCache struct {
	mu      sync.Mutex
	entries map[string]*readinessEntry
	// backoff holds per-repo "do not read before" times after a rate limit.
	backoff map[string]readinessBackoff
}

type readinessEntry struct {
	snap          *prreadiness.Snapshot
	invalidated   string
	invalidatedAt time.Time
	lastErr       *prreadiness.ReadError
	touched       time.Time
}

type readinessBackoff struct {
	until time.Time
}

func readinessCacheKey(ws, prKey string) string { return ws + "|" + prKey }

func (c *readinessCache) entryLocked(key string, now time.Time) *readinessEntry {
	if c.entries == nil {
		c.entries = map[string]*readinessEntry{}
	}
	e, ok := c.entries[key]
	if !ok {
		if len(c.entries) >= maxReadinessCacheEntries {
			c.evictLocked(now)
		}
		e = &readinessEntry{}
		c.entries[key] = e
	}
	e.touched = now
	return e
}

func (c *readinessCache) evictLocked(now time.Time) {
	for k, e := range c.entries {
		if now.Sub(e.touched) > readinessCacheEvictAge {
			delete(c.entries, k)
		}
	}
}

// apply stores snap unless it would move the entry backwards: an older or
// equal observation, a read that started before the invalidation it should
// answer, or an unmerge of a merged PR (merged is sticky). It reports
// whether snap was stored.
func (c *readinessCache) apply(ws string, snap prreadiness.Snapshot, readStarted time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.entryLocked(readinessCacheKey(ws, snap.PRKey), snap.ObservedAt)
	if e.snap != nil {
		if !snap.ObservedAt.After(e.snap.ObservedAt) {
			return false
		}
		if e.snap.Verdict == prreadiness.VerdictMerged && snap.Verdict != prreadiness.VerdictMerged {
			return false
		}
	}
	if !e.invalidatedAt.IsZero() && readStarted.Before(e.invalidatedAt) {
		return false
	}
	stored := snap
	e.snap = &stored
	e.invalidated, e.invalidatedAt = "", time.Time{}
	if e.lastErr != nil && !e.lastErr.At.After(snap.ObservedAt) {
		e.lastErr = nil
	}
	return true
}

// recordError remembers a failed read beside the last-known snapshot.
func (c *readinessCache) recordError(ws, prKey string, code prreadiness.ErrorCode, retryAfter time.Duration, at time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.entryLocked(readinessCacheKey(ws, prKey), at)
	e.lastErr = &prreadiness.ReadError{Code: code, RetryAfterSeconds: ceilSeconds(retryAfter), At: at.UTC()}
}

// observeRefs invalidates a cached snapshot when another read (list or
// detail) saw a different head SHA or base branch for the PR. Unknown
// (empty) values never invalidate.
func (c *readinessCache) observeRefs(ws, prKey, headSHA, baseRef string, at time.Time) {
	if prKey == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[readinessCacheKey(ws, prKey)]
	if !ok || e.snap == nil || e.snap.Verdict == prreadiness.VerdictMerged {
		return
	}
	reason := ""
	switch {
	case headSHA != "" && headSHA != e.snap.HeadSHA:
		reason = prreadiness.InvalidatedHeadMoved
	case baseRef != "" && baseRef != e.snap.BaseRef:
		reason = prreadiness.InvalidatedBaseChanged
	}
	if reason == "" || e.invalidated != "" {
		return
	}
	e.invalidated, e.invalidatedAt = reason, at
}

// needsRead reports whether prKey lacks a snapshot young enough
// (readinessMinRefetch) to reuse without re-reading GitHub.
func (c *readinessCache) needsRead(ws, prKey string, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[readinessCacheKey(ws, prKey)]
	if !ok || e.snap == nil || e.invalidated != "" {
		return true
	}
	if e.lastErr != nil && e.lastErr.At.After(e.snap.ObservedAt) {
		return true
	}
	return now.Sub(e.snap.ObservedAt) >= readinessMinRefetch
}

// view renders prKey's current view at now.
func (c *readinessCache) view(ws, prKey string, now time.Time) prreadiness.View {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[readinessCacheKey(ws, prKey)]
	if !ok {
		return prreadiness.NewView(prKey, nil, now, "", nil)
	}
	var snap *prreadiness.Snapshot
	if e.snap != nil {
		cp := *e.snap
		snap = &cp
	}
	var lastErr *prreadiness.ReadError
	if e.lastErr != nil {
		cp := *e.lastErr
		lastErr = &cp
	}
	return prreadiness.NewView(prKey, snap, now, e.invalidated, lastErr)
}

func (c *readinessCache) backoffUntil(ws, repo string) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.backoff[readinessCacheKey(ws, repo)].until
}

func (c *readinessCache) setBackoff(ws, repo string, until time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.backoff == nil {
		c.backoff = map[string]readinessBackoff{}
	}
	c.backoff[readinessCacheKey(ws, repo)] = readinessBackoff{until: until}
}

// clear drops every snapshot, e.g. after the credential changed and the next
// read may see different repositories.
func (c *readinessCache) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = nil
	c.backoff = nil
}

func ceilSeconds(d time.Duration) int {
	if d <= 0 {
		return 0
	}
	return int((d + time.Second - 1) / time.Second)
}

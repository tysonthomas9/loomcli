package leadapi

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

const (
	defaultDataReadRate     = rate.Limit(20)
	defaultDataReadBurst    = 40
	defaultDataMutateRate   = rate.Limit(5)
	defaultDataMutateBurst  = 10
	defaultDataLimiterTTL   = 15 * time.Minute
	defaultDataLimiterSweep = 5 * time.Minute
)

type placementEntry struct {
	readLimiter   *rate.Limiter
	mutateLimiter *rate.Limiter
	lastUsed      time.Time
}

type placementLimiter struct {
	mu      sync.Mutex
	entries map[string]*placementEntry

	lastSweep   time.Time
	read        rate.Limit
	mutate      rate.Limit
	readBurst   int
	mutateBurst int
	ttl         time.Duration
	sweepEvery  time.Duration
	now         func() time.Time
}

func newPlacementLimiter() *placementLimiter {
	return &placementLimiter{
		entries:     make(map[string]*placementEntry),
		read:        defaultDataReadRate,
		mutate:      defaultDataMutateRate,
		readBurst:   defaultDataReadBurst,
		mutateBurst: defaultDataMutateBurst,
		ttl:         defaultDataLimiterTTL,
		sweepEvery:  defaultDataLimiterSweep,
		now:         time.Now,
	}
}

func (l *placementLimiter) allow(placementID string, mutating bool) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	l.sweep(now)
	entry := l.entries[placementID]
	if entry == nil {
		entry = &placementEntry{
			readLimiter:   rate.NewLimiter(l.read, l.readBurst),
			mutateLimiter: rate.NewLimiter(l.mutate, l.mutateBurst),
		}
		l.entries[placementID] = entry
	}
	entry.lastUsed = now
	if mutating {
		return entry.mutateLimiter.AllowN(now, 1)
	}
	return entry.readLimiter.AllowN(now, 1)
}

func (l *placementLimiter) sweep(now time.Time) {
	if !l.lastSweep.IsZero() && now.Sub(l.lastSweep) < l.sweepEvery {
		return
	}
	for placementID, entry := range l.entries {
		if now.Sub(entry.lastUsed) > l.ttl {
			delete(l.entries, placementID)
		}
	}
	l.lastSweep = now
}

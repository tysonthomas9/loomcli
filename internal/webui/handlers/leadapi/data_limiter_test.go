package leadapi

import (
	"testing"
	"time"

	"golang.org/x/time/rate"
)

func TestPlacementLimiterSeparatesPlacementsAndOperationClasses(t *testing.T) {
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	limiter := newPlacementLimiter()
	limiter.now = func() time.Time { return now }
	limiter.read = rate.Limit(1)
	limiter.readBurst = 1
	limiter.mutate = rate.Limit(1)
	limiter.mutateBurst = 1

	if !limiter.allow("p1", false) || limiter.allow("p1", false) {
		t.Fatal("p1 read bucket did not allow one request then throttle")
	}
	if !limiter.allow("p1", true) {
		t.Fatal("p1 mutate bucket was consumed by its read bucket")
	}
	if !limiter.allow("p2", false) {
		t.Fatal("p2 read bucket was consumed by p1")
	}

	now = now.Add(time.Second)
	if !limiter.allow("p1", false) || !limiter.allow("p1", true) {
		t.Fatal("buckets did not refill against the injected clock")
	}
}

func TestPlacementLimiterEvictsIdleEntriesOpportunistically(t *testing.T) {
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	limiter := newPlacementLimiter()
	limiter.now = func() time.Time { return now }
	limiter.ttl = time.Minute
	limiter.sweepEvery = 30 * time.Second

	if !limiter.allow("idle", false) {
		t.Fatal("initial idle placement request was rejected")
	}
	now = now.Add(2 * time.Minute)
	if !limiter.allow("active", false) {
		t.Fatal("active placement request was rejected")
	}
	if _, ok := limiter.entries["idle"]; ok {
		t.Fatal("idle placement entry survived an eligible sweep")
	}
	if _, ok := limiter.entries["active"]; !ok {
		t.Fatal("active placement entry missing after sweep")
	}
}

func TestPlacementLimiterDefaults(t *testing.T) {
	limiter := newPlacementLimiter()
	if limiter.read != 20 || limiter.readBurst != 40 || limiter.mutate != 5 || limiter.mutateBurst != 10 {
		t.Fatalf("rates = read %v/%d mutate %v/%d", limiter.read, limiter.readBurst, limiter.mutate, limiter.mutateBurst)
	}
	if limiter.ttl != 15*time.Minute || limiter.sweepEvery != 5*time.Minute {
		t.Fatalf("eviction = ttl %s sweep %s", limiter.ttl, limiter.sweepEvery)
	}
}

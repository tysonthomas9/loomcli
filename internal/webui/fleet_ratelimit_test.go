package webui

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestFleetRateLimiter_UnderLimit_Allowed(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	rl := NewFleetRateLimiter(client, 5, time.Minute)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		allowed, err := rl.Allow(ctx, "192.168.1.1")
		if err != nil {
			t.Fatalf("request %d: unexpected error: %v", i+1, err)
		}
		if !allowed {
			t.Errorf("request %d: expected allowed, got denied", i+1)
		}
	}
}

func TestFleetRateLimiter_AtLimit_Denied(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	rl := NewFleetRateLimiter(client, 3, time.Minute)
	ctx := context.Background()

	// Use up the limit
	for i := 0; i < 3; i++ {
		allowed, err := rl.Allow(ctx, "10.0.0.1")
		if err != nil {
			t.Fatalf("request %d: unexpected error: %v", i+1, err)
		}
		if !allowed {
			t.Errorf("request %d: expected allowed, got denied", i+1)
		}
	}

	// Next request should be denied
	allowed, err := rl.Allow(ctx, "10.0.0.1")
	if err != nil {
		t.Fatalf("over-limit request: unexpected error: %v", err)
	}
	if allowed {
		t.Error("over-limit request: expected denied, got allowed")
	}
}

func TestFleetRateLimiter_DifferentKeys_Independent(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	rl := NewFleetRateLimiter(client, 2, time.Minute)
	ctx := context.Background()

	// Exhaust limit for key A
	for i := 0; i < 2; i++ {
		rl.Allow(ctx, "key-a")
	}

	// Key B should still be allowed
	allowed, err := rl.Allow(ctx, "key-b")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !allowed {
		t.Error("different key should be allowed independently")
	}
}

func TestFleetRateLimiter_WindowExpiry_AllowedAgain(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	rl := NewFleetRateLimiter(client, 2, time.Second)
	ctx := context.Background()

	// Exhaust limit
	for i := 0; i < 2; i++ {
		rl.Allow(ctx, "expiry-test")
	}

	// Should be denied
	allowed, _ := rl.Allow(ctx, "expiry-test")
	if allowed {
		t.Error("expected denied at limit")
	}

	// Fast-forward time in miniredis
	mr.FastForward(2 * time.Second)

	// Should be allowed again after window expires
	allowed, err := rl.Allow(ctx, "expiry-test")
	if err != nil {
		t.Fatalf("unexpected error after window: %v", err)
	}
	if !allowed {
		t.Error("expected allowed after window expiry")
	}
}

func TestFleetRateLimiter_RedisUnavailable_FailOpen(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	rl := NewFleetRateLimiter(client, 5, time.Minute)
	ctx := context.Background()

	// Close miniredis to simulate Redis unavailability
	mr.Close()

	allowed, err := rl.Allow(ctx, "fail-open-test")
	if err == nil {
		t.Error("expected error when Redis is unavailable")
	}
	if !allowed {
		t.Error("expected fail-open (allowed) when Redis is unavailable")
	}
}

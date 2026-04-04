package webui

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// FleetRateLimiter provides Redis-based sliding window rate limiting for fleet endpoints.
// On Redis errors, it fails open (allows the request) to preserve fleet availability.
type FleetRateLimiter struct {
	client *redis.Client
	limit  int
	window time.Duration
}

// NewFleetRateLimiter creates a rate limiter that allows limit requests per window per key.
func NewFleetRateLimiter(client *redis.Client, limit int, window time.Duration) *FleetRateLimiter {
	return &FleetRateLimiter{
		client: client,
		limit:  limit,
		window: window,
	}
}

// Allow checks if the given key (typically a client IP) is within the rate limit.
// Returns true if the request should be allowed. On Redis errors, fails open.
func (rl *FleetRateLimiter) Allow(ctx context.Context, key string) (bool, error) {
	redisKey := fmt.Sprintf("fleet:ratelimit:%s", key)
	now := time.Now()
	windowStart := now.Add(-rl.window)

	pipe := rl.client.Pipeline()
	pipe.ZRemRangeByScore(ctx, redisKey, "-inf", fmt.Sprintf("%d", windowStart.UnixNano()))
	countCmd := pipe.ZCard(ctx, redisKey)
	pipe.ZAdd(ctx, redisKey, redis.Z{
		Score:  float64(now.UnixNano()),
		Member: fmt.Sprintf("%d", now.UnixNano()),
	})
	pipe.Expire(ctx, redisKey, rl.window)

	_, err := pipe.Exec(ctx)
	if err != nil {
		logger.Warn("rate limiter redis fail-open", "err", err)
		return true, err
	}

	return countCmd.Val() < int64(rl.limit), nil
}

// Close closes the underlying Redis client.
func (rl *FleetRateLimiter) Close() error {
	if rl.client != nil {
		return rl.client.Close()
	}
	return nil
}

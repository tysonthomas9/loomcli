package kv

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/tysonthomas9/loomcli/internal/circuitbreaker"
)

// ClaimResult represents the outcome of a task claim attempt.
type ClaimResult struct {
	Success       bool
	ExistingOwner string // non-empty if already claimed by another worker
}

// HeartbeatResult represents the outcome of a heartbeat.
type HeartbeatResult struct {
	Success bool
	TTL     int    // new TTL in seconds on success
	Error   string // "no_active_session" or "ownership_lost" on failure
}

// CompleteResult represents the outcome of task completion.
type CompleteResult struct {
	Success bool
	Error   string // "task_not_found" or "not_owner" on failure
}

// Client wraps a Redis client and provides atomic task management operations.
type Client struct {
	rdb     *redis.Client
	breaker *circuitbreaker.Breaker
}

// NewClient creates a new KV client connected to the given Redis instance.
func NewClient(addr, password string, db int) *Client {
	rdb := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
		DB:       db,
	})
	return &Client{rdb: rdb}
}

// NewClientFromRedis creates a KV client from an existing redis.Client.
// This is useful for testing with miniredis.
func NewClientFromRedis(rdb *redis.Client) *Client {
	return &Client{rdb: rdb}
}

// SetCircuitBreaker attaches a circuit breaker to the client.
// When set, all operations are wrapped with circuit breaker protection.
func (c *Client) SetCircuitBreaker(b *circuitbreaker.Breaker) {
	c.breaker = b
}

// Close closes the underlying Redis connection.
func (c *Client) Close() error {
	return c.rdb.Close()
}

// RedisShouldTrip classifies Redis errors for the circuit breaker.
// Connection-level errors trip the breaker; application errors do not.
func RedisShouldTrip(err error) bool {
	if err == nil {
		return false
	}
	// Don't trip on context cancellation
	if err == context.Canceled || err == context.DeadlineExceeded {
		return false
	}
	// Trip on connection errors
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	// Trip on Redis connection refused / EOF
	errStr := err.Error()
	if strings.Contains(errStr, "connection refused") ||
		strings.Contains(errStr, "EOF") ||
		strings.Contains(errStr, "connection reset") ||
		strings.Contains(errStr, "broken pipe") {
		return true
	}
	return false
}

// ClaimTask atomically claims a task for a worker.
// If the task is already claimed by a different worker, it returns success=false
// with the existing owner's ID. Re-claiming by the same worker is idempotent.
func (c *Client) ClaimTask(ctx context.Context, workerID, taskID, taskTitle, agentType string) (ClaimResult, error) {
	if c.breaker != nil {
		return circuitbreaker.ExecuteWithResult(c.breaker, func() (ClaimResult, error) {
			return c.claimTask(ctx, workerID, taskID, taskTitle, agentType)
		})
	}
	return c.claimTask(ctx, workerID, taskID, taskTitle, agentType)
}

func (c *Client) claimTask(ctx context.Context, workerID, taskID, taskTitle, agentType string) (ClaimResult, error) {
	if err := validateID(workerID, "workerID"); err != nil {
		return ClaimResult{}, err
	}
	if err := validateID(taskID, "taskID"); err != nil {
		return ClaimResult{}, err
	}

	nowMs := time.Now().UnixMilli()

	keys := []string{
		taskOwnerKey(taskID),
		workerStateKey(workerID),
		activeWorkersKey(),
	}
	args := []interface{}{
		workerID,
		taskID,
		taskTitle,
		agentType,
		DefaultTTLSeconds,
		nowMs,
	}

	result, err := claimScript.Run(ctx, c.rdb, keys, args...).Slice()
	if err != nil {
		return ClaimResult{}, fmt.Errorf("claim script failed: %w", err)
	}
	if len(result) != 2 {
		return ClaimResult{}, fmt.Errorf("unexpected claim script result length: got %d, want 2", len(result))
	}

	status, err := toInt64(result[0])
	if err != nil {
		return ClaimResult{}, fmt.Errorf("unexpected claim status type: %w", err)
	}

	msg, err := toString(result[1])
	if err != nil {
		return ClaimResult{}, fmt.Errorf("unexpected claim message type: %w", err)
	}

	if status == 1 {
		return ClaimResult{Success: true}, nil
	}
	return ClaimResult{Success: false, ExistingOwner: msg}, nil
}

// Heartbeat atomically extends TTLs for a worker's keys.
// Must be called periodically (recommended: every 60 seconds) to prevent
// key expiration while a worker is active.
func (c *Client) Heartbeat(ctx context.Context, workerID string) (HeartbeatResult, error) {
	if c.breaker != nil {
		return circuitbreaker.ExecuteWithResult(c.breaker, func() (HeartbeatResult, error) {
			return c.heartbeat(ctx, workerID)
		})
	}
	return c.heartbeat(ctx, workerID)
}

func (c *Client) heartbeat(ctx context.Context, workerID string) (HeartbeatResult, error) {
	if err := validateID(workerID, "workerID"); err != nil {
		return HeartbeatResult{}, err
	}

	nowMs := time.Now().UnixMilli()

	keys := []string{
		workerStateKey(workerID),
		activeWorkersKey(),
	}
	args := []interface{}{
		workerID,
		DefaultTTLSeconds,
		nowMs,
	}

	result, err := heartbeatScript.Run(ctx, c.rdb, keys, args...).Slice()
	if err != nil {
		return HeartbeatResult{}, fmt.Errorf("heartbeat script failed: %w", err)
	}
	if len(result) != 2 {
		return HeartbeatResult{}, fmt.Errorf("unexpected heartbeat script result length: got %d, want 2", len(result))
	}

	status, err := toInt64(result[0])
	if err != nil {
		return HeartbeatResult{}, fmt.Errorf("unexpected heartbeat status type: %w", err)
	}

	msg, err := toString(result[1])
	if err != nil {
		return HeartbeatResult{}, fmt.Errorf("unexpected heartbeat message type: %w", err)
	}

	if status == 1 {
		ttl, _ := strconv.Atoi(msg)
		return HeartbeatResult{Success: true, TTL: ttl}, nil
	}
	return HeartbeatResult{Success: false, Error: msg}, nil
}

// CompleteTask atomically verifies ownership and cleans up after task completion.
// The worker remains registered but transitions to idle state.
func (c *Client) CompleteTask(ctx context.Context, workerID, taskID string) (CompleteResult, error) {
	if c.breaker != nil {
		return circuitbreaker.ExecuteWithResult(c.breaker, func() (CompleteResult, error) {
			return c.completeTask(ctx, workerID, taskID)
		})
	}
	return c.completeTask(ctx, workerID, taskID)
}

func (c *Client) completeTask(ctx context.Context, workerID, taskID string) (CompleteResult, error) {
	if err := validateID(workerID, "workerID"); err != nil {
		return CompleteResult{}, err
	}
	if err := validateID(taskID, "taskID"); err != nil {
		return CompleteResult{}, err
	}

	nowMs := time.Now().UnixMilli()

	keys := []string{
		taskOwnerKey(taskID),
		workerStateKey(workerID),
		activeWorkersKey(),
	}
	args := []interface{}{
		workerID,
		taskID,
		nowMs,
		DefaultTTLSeconds,
	}

	result, err := completeScript.Run(ctx, c.rdb, keys, args...).Slice()
	if err != nil {
		return CompleteResult{}, fmt.Errorf("complete script failed: %w", err)
	}
	if len(result) != 2 {
		return CompleteResult{}, fmt.Errorf("unexpected complete script result length: got %d, want 2", len(result))
	}

	status, err := toInt64(result[0])
	if err != nil {
		return CompleteResult{}, fmt.Errorf("unexpected complete status type: %w", err)
	}

	msg, err := toString(result[1])
	if err != nil {
		return CompleteResult{}, fmt.Errorf("unexpected complete message type: %w", err)
	}

	if status == 1 {
		return CompleteResult{Success: true}, nil
	}
	return CompleteResult{Success: false, Error: msg}, nil
}

// toInt64 converts a Lua script return value to int64.
// Redis Lua returns numbers as int64, but the go-redis driver may return them
// as int64 or string depending on context.
func toInt64(v interface{}) (int64, error) {
	switch val := v.(type) {
	case int64:
		return val, nil
	case string:
		return strconv.ParseInt(val, 10, 64)
	default:
		return 0, fmt.Errorf("cannot convert %T to int64", v)
	}
}

// toString converts a Lua script return value to string.
func toString(v interface{}) (string, error) {
	switch val := v.(type) {
	case string:
		return val, nil
	case int64:
		return strconv.FormatInt(val, 10), nil
	case nil:
		return "", nil
	default:
		return "", fmt.Errorf("cannot convert %T to string", v)
	}
}

// StaleWorkerEntry represents a worker with an expired heartbeat found in the ZSET.
type StaleWorkerEntry struct {
	WorkerID string
	Score    float64 // last heartbeat timestamp in milliseconds
}

// GetStaleWorkers returns workers whose heartbeats are older than the given threshold.
// It queries the active workers ZSET for entries with scores below (now - threshold).
func (c *Client) GetStaleWorkers(ctx context.Context, threshold time.Duration) ([]StaleWorkerEntry, error) {
	if c.breaker != nil {
		return circuitbreaker.ExecuteWithResult(c.breaker, func() ([]StaleWorkerEntry, error) {
			return c.getStaleWorkers(ctx, threshold)
		})
	}
	return c.getStaleWorkers(ctx, threshold)
}

func (c *Client) getStaleWorkers(ctx context.Context, threshold time.Duration) ([]StaleWorkerEntry, error) {
	cutoff := float64(time.Now().Add(-threshold).UnixMilli())
	results, err := c.rdb.ZRangeByScoreWithScores(ctx, activeWorkersKey(), &redis.ZRangeBy{
		Min: "-inf",
		Max: strconv.FormatFloat(cutoff, 'f', 0, 64),
	}).Result()
	if err != nil {
		return nil, fmt.Errorf("ZRANGEBYSCORE failed: %w", err)
	}

	entries := make([]StaleWorkerEntry, len(results))
	for i, z := range results {
		entries[i] = StaleWorkerEntry{
			WorkerID: z.Member.(string),
			Score:    z.Score,
		}
	}
	return entries, nil
}

// GetWorkerState returns the hash fields for a worker's state key.
func (c *Client) GetWorkerState(ctx context.Context, workerID string) (map[string]string, error) {
	if c.breaker != nil {
		return circuitbreaker.ExecuteWithResult(c.breaker, func() (map[string]string, error) {
			return c.rdb.HGetAll(ctx, workerStateKey(workerID)).Result()
		})
	}
	return c.rdb.HGetAll(ctx, workerStateKey(workerID)).Result()
}

// GetTaskOwner returns the worker ID that owns the given task, or empty string if unowned.
func (c *Client) GetTaskOwner(ctx context.Context, taskID string) (string, error) {
	get := func() (string, error) {
		val, err := c.rdb.Get(ctx, taskOwnerKey(taskID)).Result()
		if err == redis.Nil {
			return "", nil
		}
		return val, err
	}
	if c.breaker != nil {
		return circuitbreaker.ExecuteWithResult(c.breaker, get)
	}
	return get()
}

// DeleteTaskOwner removes the ownership key for a task.
func (c *Client) DeleteTaskOwner(ctx context.Context, taskID string) error {
	fn := func() error {
		return c.rdb.Del(ctx, taskOwnerKey(taskID)).Err()
	}
	if c.breaker != nil {
		return c.breaker.Execute(fn)
	}
	return fn()
}

// DeleteWorkerState removes the state hash for a worker.
func (c *Client) DeleteWorkerState(ctx context.Context, workerID string) error {
	fn := func() error {
		return c.rdb.Del(ctx, workerStateKey(workerID)).Err()
	}
	if c.breaker != nil {
		return c.breaker.Execute(fn)
	}
	return fn()
}

// RemoveActiveWorker removes a worker from the active workers ZSET.
func (c *Client) RemoveActiveWorker(ctx context.Context, workerID string) error {
	fn := func() error {
		return c.rdb.ZRem(ctx, activeWorkersKey(), workerID).Err()
	}
	if c.breaker != nil {
		return c.breaker.Execute(fn)
	}
	return fn()
}

// SetLeaderKey atomically sets a leader key if it does not exist (SETNX with TTL).
// Returns true if the key was set (leadership acquired), false if already held.
func (c *Client) SetLeaderKey(ctx context.Context, key, value string, ttl time.Duration) (bool, error) {
	if c.breaker != nil {
		return circuitbreaker.ExecuteWithResult(c.breaker, func() (bool, error) {
			return c.rdb.SetNX(ctx, key, value, ttl).Result()
		})
	}
	return c.rdb.SetNX(ctx, key, value, ttl).Result()
}

// RenewLeaderKey extends the TTL on a leader key.
func (c *Client) RenewLeaderKey(ctx context.Context, key string, ttl time.Duration) error {
	fn := func() error {
		return c.rdb.Expire(ctx, key, ttl).Err()
	}
	if c.breaker != nil {
		return c.breaker.Execute(fn)
	}
	return fn()
}

// DeleteLeaderKey removes a leader key (best-effort on shutdown).
func (c *Client) DeleteLeaderKey(ctx context.Context, key string) error {
	fn := func() error {
		return c.rdb.Del(ctx, key).Err()
	}
	if c.breaker != nil {
		return c.breaker.Execute(fn)
	}
	return fn()
}

// validateID checks that an ID is non-empty and does not contain characters
// that could cause Redis key collisions or parsing issues.
func validateID(id, name string) error {
	if id == "" {
		return fmt.Errorf("%s must not be empty", name)
	}
	if strings.ContainsAny(id, ":\n\r\t ") {
		return fmt.Errorf("%s contains invalid characters: %q", name, id)
	}
	return nil
}

package kv

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
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
	rdb *redis.Client
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

// Close closes the underlying Redis connection.
func (c *Client) Close() error {
	return c.rdb.Close()
}

// ClaimTask atomically claims a task for a worker.
// If the task is already claimed by a different worker, it returns success=false
// with the existing owner's ID. Re-claiming by the same worker is idempotent.
func (c *Client) ClaimTask(ctx context.Context, workerID, taskID, taskTitle, agentType string) (ClaimResult, error) {
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

// Package fleet provides Redis-based coordination for multi-server Fleet API.
//
// It manages task claim tracking and idempotent claim caching for fleet workers,
// using Redis SETNX for atomic claims and JSON serialization for response caching.
package fleet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// ErrWorkerNotFound is returned when a worker is not registered.
var ErrWorkerNotFound = errors.New("worker not found")

const (
	// keyPrefix namespaces all fleet Redis keys.
	keyPrefix = "fleet:"

	// defaultClaimTTL is the initial TTL for claimed task keys (5 minutes).
	defaultClaimTTL = 5 * time.Minute

	// defaultWorkerClaimTTL is the TTL for cached worker claim responses (5 minutes).
	defaultWorkerClaimTTL = 5 * time.Minute

	// defaultWorkerRegistrationTTL is the TTL for worker registration keys (2 hours).
	defaultWorkerRegistrationTTL = 2 * time.Hour
)

// Redis key builders.
func claimedTaskKey(taskID string) string {
	return keyPrefix + "tasks:claimed:" + taskID
}

func workerClaimKey(workerID string) string {
	return keyPrefix + "worker:claim:" + workerID
}

func workerRegistrationKey(workerID string) string {
	return keyPrefix + "workers:" + workerID
}

// RedisConfig holds connection parameters for the Redis instance.
type RedisConfig struct {
	Address  string `yaml:"address"`
	Password string `yaml:"password"`
	DB       int    `yaml:"db"`
	PoolSize int    `yaml:"pool_size"`
}

// ClaimResponse represents a cached fleet claim result for idempotent retries.
type ClaimResponse struct {
	TaskID  string          `json:"task_id"`
	Success bool            `json:"success"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// Store manages fleet-level Redis state for task claims and worker coordination.
type Store struct {
	client *redis.Client
	logger *slog.Logger

	claimScript *redis.Script
}

// NewStore creates a new fleet Store connected to the given Redis instance.
func NewStore(cfg RedisConfig, logger *slog.Logger) (*Store, error) {
	if cfg.Address == "" {
		return nil, fmt.Errorf("fleet redis address is required")
	}
	if logger == nil {
		logger = slog.Default()
	}

	// Resolve password: YAML config > FLEET_REDIS_PASSWORD > LOOM_REDIS_PASSWORD
	password := cfg.Password
	if password == "" {
		if p := os.Getenv("FLEET_REDIS_PASSWORD"); p != "" {
			password = p
		} else if p := os.Getenv("LOOM_REDIS_PASSWORD"); p != "" {
			password = p
		}
	}

	poolSize := cfg.PoolSize
	if poolSize == 0 {
		poolSize = 10
	}

	client := redis.NewClient(&redis.Options{
		Addr:     cfg.Address,
		Password: password,
		DB:       cfg.DB,
		PoolSize: poolSize,
	})

	s := &Store{
		client: client,
		logger: logger,
	}
	s.claimScript = redis.NewScript(claimLua)

	return s, nil
}

// NewStoreFromClient creates a Store from an existing redis.Client.
// This is useful for testing with miniredis.
func NewStoreFromClient(client *redis.Client, logger *slog.Logger) *Store {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Store{
		client: client,
		logger: logger,
	}
	s.claimScript = redis.NewScript(claimLua)
	return s
}

// NewSigningKeyManagerFromStore creates a SigningKeyManager using the same Redis
// connection as the Store. This avoids exposing the raw Redis client while allowing
// the signing key manager to share the Store's connection pool.
func NewSigningKeyManagerFromStore(s *Store) *SigningKeyManager {
	return NewSigningKeyManager(s.client, s.logger)
}

// NewRedisClient creates a bare redis.Client for use outside of the Store.
// This is useful when only the SigningKeyManager is needed without a full Store.
func NewRedisClient(addr, password string, db int) *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
		DB:       db,
	})
}

// Close closes the underlying Redis connection.
func (s *Store) Close() error {
	return s.client.Close()
}

// claimLua atomically claims a task for a worker using SETNX semantics.
//
// KEYS[1] = fleet:tasks:claimed:{task_id}
// ARGV[1] = worker_id
// ARGV[2] = ttl_seconds
//
// Returns:
//
//	{1, ""} if claimed successfully
//	{1, ""} if already claimed by same worker (idempotent)
//	{0, existing_owner} if claimed by different worker
const claimLua = `
local existing = redis.call('GET', KEYS[1])
if existing == false then
    redis.call('SET', KEYS[1], ARGV[1], 'EX', tonumber(ARGV[2]))
    return {1, ''}
end
if existing == ARGV[1] then
    redis.call('EXPIRE', KEYS[1], tonumber(ARGV[2]))
    return {1, ''}
end
return {0, existing}
`

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

// TryClaim atomically attempts to claim a task for a worker.
// Returns (true, nil) if the claim succeeded or was idempotently re-claimed.
// Returns (false, nil) if the task is already claimed by a different worker.
func (s *Store) TryClaim(ctx context.Context, taskID, workerID string) (bool, error) {
	if err := validateID(taskID, "taskID"); err != nil {
		return false, err
	}
	if err := validateID(workerID, "workerID"); err != nil {
		return false, err
	}

	ttlSec := int(defaultClaimTTL.Seconds())

	result, err := s.claimScript.Run(ctx, s.client,
		[]string{claimedTaskKey(taskID)},
		workerID, ttlSec,
	).Slice()
	if err != nil {
		return false, fmt.Errorf("claim script failed: %w", err)
	}
	if len(result) != 2 {
		return false, fmt.Errorf("unexpected claim script result length: got %d, want 2", len(result))
	}

	status, ok := result[0].(int64)
	if !ok {
		return false, fmt.Errorf("unexpected claim status type: %T", result[0])
	}

	if status == 1 {
		s.logger.Debug("task claimed", "task_id", taskID, "worker_id", workerID)
		return true, nil
	}

	existingOwner, _ := result[1].(string)
	s.logger.Debug("task already claimed", "task_id", taskID, "worker_id", workerID, "existing_owner", existingOwner)
	return false, nil
}

// ExtendClaimTTL extends the TTL on a claimed task key.
// This should be called after beads confirms the claim to keep it alive longer.
func (s *Store) ExtendClaimTTL(ctx context.Context, taskID string, timeoutMin int) error {
	if err := validateID(taskID, "taskID"); err != nil {
		return err
	}

	key := claimedTaskKey(taskID)
	ttl := time.Duration(timeoutMin) * time.Minute

	ok, err := s.client.Expire(ctx, key, ttl).Result()
	if err != nil {
		return fmt.Errorf("extend claim TTL failed: %w", err)
	}
	if !ok {
		return fmt.Errorf("task claim key not found: %s", taskID)
	}

	s.logger.Debug("claim TTL extended", "task_id", taskID, "ttl_min", timeoutMin)
	return nil
}

// ReleaseClaim removes the claim key for a task, making it available for other workers.
func (s *Store) ReleaseClaim(ctx context.Context, taskID string) error {
	if err := validateID(taskID, "taskID"); err != nil {
		return err
	}

	key := claimedTaskKey(taskID)

	err := s.client.Del(ctx, key).Err()
	if err != nil {
		return fmt.Errorf("release claim failed: %w", err)
	}

	s.logger.Debug("claim released", "task_id", taskID)
	return nil
}

// RecordWorkerClaim caches a ClaimResponse for a worker to enable idempotent retries.
// The cached response expires after 5 minutes.
func (s *Store) RecordWorkerClaim(ctx context.Context, workerID string, claim *ClaimResponse) error {
	if err := validateID(workerID, "workerID"); err != nil {
		return err
	}

	data, err := json.Marshal(claim)
	if err != nil {
		return fmt.Errorf("marshal claim response: %w", err)
	}

	key := workerClaimKey(workerID)
	err = s.client.Set(ctx, key, data, defaultWorkerClaimTTL).Err()
	if err != nil {
		return fmt.Errorf("record worker claim failed: %w", err)
	}

	s.logger.Debug("worker claim recorded", "worker_id", workerID, "task_id", claim.TaskID)
	return nil
}

// GetWorkerClaim retrieves a cached ClaimResponse for a worker.
// Returns (nil, nil) if no cached claim exists.
func (s *Store) GetWorkerClaim(ctx context.Context, workerID string) (*ClaimResponse, error) {
	if err := validateID(workerID, "workerID"); err != nil {
		return nil, err
	}

	key := workerClaimKey(workerID)

	data, err := s.client.Get(ctx, key).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get worker claim failed: %w", err)
	}

	var claim ClaimResponse
	if err := json.Unmarshal(data, &claim); err != nil {
		return nil, fmt.Errorf("unmarshal claim response: %w", err)
	}

	return &claim, nil
}

// ClearWorkerClaim removes the cached claim response for a worker.
// This is called after a worker completes its task to clean up state.
func (s *Store) ClearWorkerClaim(ctx context.Context, workerID string) error {
	if err := validateID(workerID, "workerID"); err != nil {
		return err
	}

	key := workerClaimKey(workerID)
	if err := s.client.Del(ctx, key).Err(); err != nil {
		return fmt.Errorf("clear worker claim failed: %w", err)
	}

	s.logger.Debug("worker claim cleared", "worker_id", workerID)
	return nil
}

// Worker represents a registered fleet worker.
type Worker struct {
	WorkerID     string   `json:"worker_id"`
	Repos        []string `json:"repos,omitempty"`
	RegisteredAt int64    `json:"registered_at"`
}

// RegisterWorker registers (or re-registers) a worker in Redis.
// Re-registration is idempotent: it updates the timestamp and repos.
func (s *Store) RegisterWorker(ctx context.Context, worker *Worker) error {
	if err := validateID(worker.WorkerID, "workerID"); err != nil {
		return err
	}

	data, err := json.Marshal(worker)
	if err != nil {
		return fmt.Errorf("marshal worker: %w", err)
	}

	key := workerRegistrationKey(worker.WorkerID)
	if err := s.client.Set(ctx, key, data, defaultWorkerRegistrationTTL).Err(); err != nil {
		return fmt.Errorf("register worker failed: %w", err)
	}

	s.logger.Debug("worker registered", "worker_id", worker.WorkerID)
	return nil
}

// GetWorker retrieves a registered worker by ID.
// Returns (nil, nil) if the worker is not registered.
func (s *Store) GetWorker(ctx context.Context, workerID string) (*Worker, error) {
	if err := validateID(workerID, "workerID"); err != nil {
		return nil, err
	}

	key := workerRegistrationKey(workerID)
	data, err := s.client.Get(ctx, key).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get worker failed: %w", err)
	}

	var worker Worker
	if err := json.Unmarshal(data, &worker); err != nil {
		return nil, fmt.Errorf("unmarshal worker: %w", err)
	}

	return &worker, nil
}

// UpdateHeartbeat refreshes a worker's registration TTL.
// Returns the timestamp of the heartbeat update and ErrWorkerNotFound if the worker
// is not registered.
func (s *Store) UpdateHeartbeat(ctx context.Context, workerID string) (time.Time, error) {
	if err := validateID(workerID, "workerID"); err != nil {
		return time.Time{}, err
	}

	key := workerRegistrationKey(workerID)

	// Refresh the TTL atomically - only if the key exists
	ok, err := s.client.Expire(ctx, key, defaultWorkerRegistrationTTL).Result()
	if err != nil {
		return time.Time{}, fmt.Errorf("update heartbeat failed: %w", err)
	}
	if !ok {
		return time.Time{}, ErrWorkerNotFound
	}

	now := time.Now().UTC()
	s.logger.Debug("heartbeat updated", "worker_id", workerID)
	return now, nil
}

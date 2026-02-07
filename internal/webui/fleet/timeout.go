package fleet

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	// claimTimesKey is a Redis sorted set tracking when tasks were claimed.
	// Score = Unix timestamp, Member = taskID.
	claimTimesKey = keyPrefix + "tasks:claim_times"
)

// TimedOutTask contains info about a task that exceeded the timeout.
type TimedOutTask struct {
	TaskID    string
	WorkerID  string
	ClaimedAt time.Time
}

// RecordClaimTime records the claim timestamp for a task in the sorted set.
func (s *Store) RecordClaimTime(ctx context.Context, taskID string) error {
	score := float64(time.Now().Unix())
	return s.client.ZAdd(ctx, claimTimesKey, redis.Z{
		Score:  score,
		Member: taskID,
	}).Err()
}

// ClearClaimTime removes the claim timestamp for a task from the sorted set.
func (s *Store) ClearClaimTime(ctx context.Context, taskID string) error {
	return s.client.ZRem(ctx, claimTimesKey, taskID).Err()
}

// FindTimedOutTasks returns tasks whose claim time exceeds the given timeout.
func (s *Store) FindTimedOutTasks(ctx context.Context, timeout time.Duration) ([]TimedOutTask, error) {
	cutoff := time.Now().Add(-timeout)

	// Get all tasks claimed before the cutoff
	results, err := s.client.ZRangeByScoreWithScores(ctx, claimTimesKey, &redis.ZRangeBy{
		Min: "-inf",
		Max: strconv.FormatInt(cutoff.Unix(), 10),
	}).Result()
	if err != nil {
		return nil, fmt.Errorf("query timed out tasks: %w", err)
	}

	if len(results) == 0 {
		return nil, nil
	}

	var tasks []TimedOutTask
	for _, z := range results {
		taskID, ok := z.Member.(string)
		if !ok {
			continue
		}

		// Look up the worker that owns this claim
		workerID, err := s.client.Get(ctx, claimedTaskKey(taskID)).Result()
		if err == redis.Nil {
			// Claim key expired but sorted set entry remains — clean it up
			if err := s.client.ZRem(ctx, claimTimesKey, taskID).Err(); err != nil {
				s.logger.Warn("failed to clean up orphaned claim time", "task_id", taskID, "error", err)
			}
			continue
		}
		if err != nil {
			s.logger.Warn("failed to look up claim owner", "task_id", taskID, "error", err)
			continue
		}

		tasks = append(tasks, TimedOutTask{
			TaskID:    taskID,
			WorkerID:  workerID,
			ClaimedAt: time.Unix(int64(z.Score), 0),
		})
	}

	return tasks, nil
}

// TimeoutConfig holds configuration for task timeout enforcement.
type TimeoutConfig struct {
	TaskTimeout   time.Duration // Maximum task execution time (default: 30 minutes)
	CheckInterval time.Duration // How often to check for timeouts (default: 1 minute)
}

// DefaultTimeoutConfig returns sensible defaults.
func DefaultTimeoutConfig() TimeoutConfig {
	return TimeoutConfig{
		TaskTimeout:   30 * time.Minute,
		CheckInterval: 1 * time.Minute,
	}
}

// TimeoutEnforcer periodically checks for and handles timed-out tasks.
type TimeoutEnforcer struct {
	store        *Store
	config       TimeoutConfig
	timeoutCount int64 // atomic counter for loom_fleet_timeouts_total
	done         chan struct{}
	wg           sync.WaitGroup
	logger       *slog.Logger

	// onTimeout is called when a task times out (e.g., to add a comment to the issue).
	onTimeout func(workerID, taskID string, duration time.Duration) error
}

// NewTimeoutEnforcer creates a new timeout enforcer.
// Panics if store is nil or config values are non-positive.
func NewTimeoutEnforcer(store *Store, config TimeoutConfig, logger *slog.Logger) *TimeoutEnforcer {
	if store == nil {
		panic("timeout enforcer requires non-nil store")
	}
	if config.TaskTimeout <= 0 {
		panic("task timeout must be positive")
	}
	if config.CheckInterval <= 0 {
		panic("check interval must be positive")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &TimeoutEnforcer{
		store:  store,
		config: config,
		done:   make(chan struct{}),
		logger: logger,
	}
}

// SetOnTimeout sets the callback invoked when a task times out.
func (t *TimeoutEnforcer) SetOnTimeout(fn func(workerID, taskID string, duration time.Duration) error) {
	t.onTimeout = fn
}

// Start begins the background timeout check loop.
func (t *TimeoutEnforcer) Start() {
	t.wg.Add(1)
	go t.run()
}

// Stop gracefully stops the timeout enforcer.
func (t *TimeoutEnforcer) Stop() {
	close(t.done)
	t.wg.Wait()
}

// GetTimeoutCount returns the current timeout counter value.
func (t *TimeoutEnforcer) GetTimeoutCount() int64 {
	return atomic.LoadInt64(&t.timeoutCount)
}

func (t *TimeoutEnforcer) run() {
	defer t.wg.Done()

	ticker := time.NewTicker(t.config.CheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-t.done:
			return
		case <-ticker.C:
			if err := t.checkTimeouts(); err != nil {
				t.logger.Warn("timeout check failed", "error", err)
			}
		}
	}
}

func (t *TimeoutEnforcer) checkTimeouts() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tasks, err := t.store.FindTimedOutTasks(ctx, t.config.TaskTimeout)
	if err != nil {
		return err
	}

	for _, task := range tasks {
		t.handleTimeout(ctx, task)
	}

	return nil
}

func (t *TimeoutEnforcer) handleTimeout(ctx context.Context, task TimedOutTask) {
	duration := time.Since(task.ClaimedAt)

	// Notify via callback (best-effort)
	if t.onTimeout != nil {
		if err := t.onTimeout(task.WorkerID, task.TaskID, duration); err != nil {
			t.logger.Warn("timeout callback failed",
				"task_id", task.TaskID,
				"worker_id", task.WorkerID,
				"error", err,
			)
		}
	}

	// Release the claim
	if err := t.store.ReleaseClaim(ctx, task.TaskID); err != nil {
		t.logger.Warn("failed to release timed-out claim",
			"task_id", task.TaskID,
			"error", err,
		)
		return
	}

	// ClearClaimTime is called inside ReleaseClaim, so no need here.

	atomic.AddInt64(&t.timeoutCount, 1)

	t.logger.Info("task timed out",
		"task_id", task.TaskID,
		"worker_id", task.WorkerID,
		"duration", duration.String(),
	)
}

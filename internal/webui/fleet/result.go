package fleet

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	// defaultTaskResultTTL is the TTL for task result keys (24 hours).
	defaultTaskResultTTL = 24 * time.Hour
)

func taskResultKey(taskID string) string {
	return keyPrefix + "task:result:" + taskID
}

// TaskResult represents the outcome of a completed task.
type TaskResult struct {
	WorkerID    string    `json:"worker_id"`
	TaskID      string    `json:"task_id"`
	Success     bool      `json:"success"`
	CommitSHA   string    `json:"commit_sha,omitempty"`
	Error       string    `json:"error,omitempty"`
	CompletedAt time.Time `json:"completed_at"`
}

// RecordTaskResult stores a task completion result in Redis.
// Results are stored with a 24-hour TTL for debugging and audit purposes.
func (s *Store) RecordTaskResult(ctx context.Context, result *TaskResult) error {
	if err := validateID(result.TaskID, "taskID"); err != nil {
		return err
	}
	if err := validateID(result.WorkerID, "workerID"); err != nil {
		return err
	}

	data, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("marshal task result: %w", err)
	}

	key := taskResultKey(result.TaskID)
	if err := s.client.Set(ctx, key, data, defaultTaskResultTTL).Err(); err != nil {
		return fmt.Errorf("record task result failed: %w", err)
	}

	s.logger.Debug("task result recorded", "task_id", result.TaskID, "worker_id", result.WorkerID, "success", result.Success)
	return nil
}

// GetTaskResult retrieves a task result by task ID.
// Returns (nil, nil) if no result exists.
func (s *Store) GetTaskResult(ctx context.Context, taskID string) (*TaskResult, error) {
	if err := validateID(taskID, "taskID"); err != nil {
		return nil, err
	}

	key := taskResultKey(taskID)
	data, err := s.client.Get(ctx, key).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get task result failed: %w", err)
	}

	var result TaskResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("unmarshal task result: %w", err)
	}

	return &result, nil
}

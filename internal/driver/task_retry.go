package driver

import (
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

// Retry-then-block policy support for claimed TaskRun execution: decide
// whether a failed attempt is retried, requeue it with scheduler metadata,
// or block its underlying task (terminal failure) once attempts are exhausted. The linked
// DriverStep follows the requeued TaskRun back to queued.
type taskRunRetryDecisionResult struct {
	Retry       bool
	Attempt     int
	MaxAttempts int
}

func taskRunRetryDecision(claimed *domain.TaskRun, opts executeClaimedTaskRunOptions, completion taskExecCompletion) taskRunRetryDecisionResult {
	maxAttempts := opts.MaxAttempts
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	attempt := taskRunAttempt(claimed) + 1
	decision := taskRunRetryDecisionResult{Attempt: attempt, MaxAttempts: maxAttempts}
	if completion.Status == domain.TaskRunFailed && attempt < maxAttempts {
		decision.Retry = true
	}
	return decision
}

func taskRunAttempt(run *domain.TaskRun) int {
	if run == nil || run.RuntimeMetadata == nil {
		return 0
	}
	raw := strings.TrimSpace(run.RuntimeMetadata["scheduler_attempt"])
	if raw == "" {
		return 0
	}
	attempt, err := strconv.Atoi(raw)
	if err != nil || attempt < 0 {
		return 0
	}
	return attempt
}

const (
	taskRunRetryBackoffBase = time.Second
	taskRunRetryBackoffMax  = 30 * time.Second
)

// taskRunNow resolves the injectable clock seam; a nil now uses time.Now.
func taskRunNow(now func() time.Time) time.Time {
	if now != nil {
		return now().UTC()
	}
	return time.Now().UTC()
}

// taskRunRetryBackoff computes the exponential retry backoff for a failed
// attempt: min(30s, 1s<<attempt).
func taskRunRetryBackoff(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	// 1s<<5 = 32s already exceeds the cap; avoid shift overflow for large attempts.
	if attempt >= 5 {
		return taskRunRetryBackoffMax
	}
	backoff := taskRunRetryBackoffBase << attempt
	if backoff > taskRunRetryBackoffMax {
		return taskRunRetryBackoffMax
	}
	return backoff
}

func taskRunRetryMetadata(_ *domain.TaskRun, retry taskRunRetryDecisionResult, completion taskExecCompletion, metadata map[string]string) map[string]string {
	return schedulerMetadata(metadata, "retrying", retry.Attempt, retry.MaxAttempts, completion)
}

func taskRunBlockedMetadata(claimed *domain.TaskRun, opts executeClaimedTaskRunOptions, completion taskExecCompletion, metadata map[string]string) map[string]string {
	maxAttempts := opts.MaxAttempts
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	return schedulerMetadata(metadata, "blocked", taskRunAttempt(claimed)+1, maxAttempts, completion)
}

// schedulerMetadata stamps the retry-then-block scheduler state onto a copy of
// the run's runtime metadata.
func schedulerMetadata(metadata map[string]string, state string, attempt, maxAttempts int, completion taskExecCompletion) map[string]string {
	out := cloneStringMap(metadata)
	if out == nil {
		out = map[string]string{}
	}
	out["scheduler_state"] = state
	out["scheduler_attempt"] = strconv.Itoa(attempt)
	out["scheduler_max_attempts"] = strconv.Itoa(maxAttempts)
	if completion.ErrorClass != "" {
		out["scheduler_last_error_class"] = completion.ErrorClass
	}
	if completion.ErrorMessage != "" {
		out["scheduler_last_error_message"] = completion.ErrorMessage
	}
	return out
}

func driverStepStatusForTaskRun(status domain.TaskRunStatus) domain.DriverStepStatus {
	switch status {
	case domain.TaskRunQueued:
		return domain.DriverStepQueued
	case domain.TaskRunRunning:
		return domain.DriverStepRunning
	case domain.TaskRunCompleted:
		return domain.DriverStepCompleted
	case domain.TaskRunCancelled:
		return domain.DriverStepSkipped
	default:
		return domain.DriverStepFailed
	}
}

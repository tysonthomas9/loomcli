package driver

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// Retry-then-park policy support for claimed TaskRun execution: decide
// whether a failed attempt is retried, requeue it with scheduler metadata,
// or park it (terminal failure) once attempts are exhausted. The linked
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

func requeueClaimedTaskRun(ctx context.Context, s store.Store, claimed *domain.TaskRun, opts executeClaimedTaskRunOptions, execResult TaskExecResult, completion taskExecCompletion, metadata map[string]string, retry taskRunRetryDecisionResult) (*domain.TaskRun, error) {
	metadata = taskRunRetryMetadata(claimed, retry, completion, metadata)
	requeued, err := s.TaskRuns().Requeue(ctx, claimed.WorkspaceKey, claimed.TaskRunID, store.TaskRunRequeue{
		NodeID:          claimed.NodeID,
		LeaseID:         claimed.LeaseID,
		LeaseToken:      opts.LeaseToken,
		FencingToken:    claimed.FencingToken,
		RuntimeMetadata: metadata,
		LogsRef:         execResult.LogsRef,
		ArtifactsRef:    execResult.ArtifactsRef,
		ErrorClass:      completion.ErrorClass,
		ErrorMessage:    completion.ErrorMessage,
		NextEligibleAt:  taskRunNow(opts.Now).Add(taskRunRetryBackoff(retry.Attempt)),
	})
	if err != nil {
		return nil, fmt.Errorf("requeue task run: %w", err)
	}
	appendTaskRunEvent(ctx, s, requeued, domain.TaskRunEventRequeued, completion, taskRunEventContext{
		EpicID:     taskRunEpicID(ctx, s, requeued),
		LeaseToken: opts.LeaseToken,
	})
	return requeued, nil
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

func taskRunParkedMetadata(claimed *domain.TaskRun, opts executeClaimedTaskRunOptions, completion taskExecCompletion, metadata map[string]string) map[string]string {
	maxAttempts := opts.MaxAttempts
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	return schedulerMetadata(metadata, "parked", taskRunAttempt(claimed)+1, maxAttempts, completion)
}

// schedulerMetadata stamps the retry-then-park scheduler state onto a copy of
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

func requeueLinkedDriverStep(ctx context.Context, s store.Store, claimed, requeued *domain.TaskRun) error {
	if claimed == nil || requeued == nil || claimed.DriverStepID == "" {
		return nil
	}
	parent, err := s.DriverRuns().Get(ctx, requeued.WorkspaceKey, requeued.DriverRunID)
	if err != nil {
		return fmt.Errorf("get parent driver run for requeued task step update: %w", err)
	}
	if parent.Status != domain.DriverRunRunning {
		return nil
	}
	status := domain.DriverStepQueued
	outputRef := firstNonEmpty(requeued.ArtifactsRef, requeued.LogsRef)
	_, err = s.DriverSteps().Update(ctx, requeued.WorkspaceKey, claimed.DriverStepID, store.DriverStepUpdate{
		Status:       &status,
		TaskRunID:    &requeued.TaskRunID,
		OutputRef:    &outputRef,
		NodeID:       parent.NodeID,
		LeaseID:      parent.LeaseID,
		FencingToken: parent.FencingToken,
	})
	if err != nil {
		return fmt.Errorf("update linked driver step from requeued task run: %w", err)
	}
	return nil
}

func finishLinkedDriverStep(ctx context.Context, s store.Store, claimed *domain.TaskRun, opts executeClaimedTaskRunOptions, refs claimedTaskRunRefs, execResult TaskExecResult, status domain.TaskRunStatus) error {
	if !opts.UpdateDriverStep || refs.DriverStepID == "" {
		return nil
	}
	stepStatus := driverStepStatusForTaskRun(status)
	outputRef := firstNonEmpty(execResult.ArtifactsRef, execResult.LogsRef)
	_, err := s.DriverSteps().Update(ctx, refs.WorkspaceKey, refs.DriverStepID, store.DriverStepUpdate{
		Status:       &stepStatus,
		TaskRunID:    &claimed.TaskRunID,
		OutputRef:    &outputRef,
		NodeID:       opts.ParentNodeID,
		LeaseID:      opts.ParentLeaseID,
		FencingToken: opts.ParentFence,
	})
	if err != nil {
		return fmt.Errorf("finish driver step: %w", err)
	}
	return nil
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

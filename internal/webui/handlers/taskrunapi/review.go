package taskrunapi

import (
	"context"
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/loomgit/driverfreeze"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// attemptAwaitsReview reads the capture journal; tests replace it.
var attemptAwaitsReview = func(ctx context.Context, journalPath, workspace, attempt string) (bool, error) {
	if journalPath == "" {
		return driverfreeze.AttemptAwaitsReview(ctx, workspace, attempt)
	}
	return driverfreeze.AttemptAwaitsReviewAt(ctx, journalPath, workspace, attempt)
}

// closeTaskOnComplete decides whether a runner's successful completion may
// close its task. When the run's attempt froze code that awaits review (D29),
// the task stays open in review instead, whatever the runner asked for.
func (m *Module) closeTaskOnComplete(ctx context.Context, ws string, id leaseIdentity, complete store.TaskRunComplete) (bool, error) {
	if !complete.CloseTask || complete.Status != domain.TaskRunCompleted {
		return complete.CloseTask, nil
	}
	run, err := m.store.TaskRuns().Get(ctx, ws, id.TaskRunID)
	if err != nil {
		return false, fmt.Errorf("get task run: %w", err)
	}
	attempt := complete.RuntimeMetadata["attempt_id"]
	if attempt == "" {
		attempt = captureAttempt(id.TaskRunID, run.RuntimeMetadata)
	}
	awaits, err := attemptAwaitsReview(ctx, m.captureJournalPath, ws, attempt)
	if err != nil {
		return false, fmt.Errorf("check task %s revision for review: %w", run.TaskID, err)
	}
	if !awaits {
		return true, nil
	}
	issues, err := m.issueBackends(ws, taskRunActor(run))
	if err != nil {
		return false, err
	}
	if err := backend.MarkCodeReview(ctx, issues, run.TaskID, ""); err != nil {
		return false, err
	}
	return false, nil
}

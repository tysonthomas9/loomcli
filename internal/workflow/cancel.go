package workflow

import (
	"context"
	"fmt"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type TerminalWorkflowRunError struct {
	RunID  string
	Status domain.WorkflowRunStatus
}

func (e *TerminalWorkflowRunError) Error() string {
	return fmt.Sprintf("cannot cancel workflow run %s: already in terminal state %q", e.RunID, e.Status)
}

func (e *TerminalWorkflowRunError) Unwrap() error {
	return domain.ErrConflict
}

func CancelRun(ctx context.Context, st store.Store, ws, runID string, eventData map[string]any) (*domain.WorkflowRun, error) {
	if st == nil || st.WorkflowRuns() == nil || st.RunEvents() == nil {
		return nil, fmt.Errorf("workflow stores not configured: %w", domain.ErrInvalid)
	}
	run, err := st.WorkflowRuns().Get(ctx, ws, runID)
	if err != nil {
		return nil, fmt.Errorf("get workflow run %s: %w", runID, err)
	}
	if !domain.WorkflowRunStatusLive(run.Status) {
		return nil, &TerminalWorkflowRunError{RunID: run.RunID, Status: run.Status}
	}
	now := time.Now().UTC()
	finishedAt := &now
	status := domain.WorkflowRunCancelled
	updated, err := st.WorkflowRuns().Update(ctx, ws, runID, store.WorkflowRunUpdate{
		Status:     &status,
		FinishedAt: &finishedAt,
	})
	if err != nil {
		return nil, fmt.Errorf("cancel workflow run %s: %w", runID, err)
	}
	append := store.RunEventAppend{
		WorkspaceKey:  ws,
		WorkflowRunID: updated.RunID,
		Type:          "workflow_cancelled",
		Message:       "workflow run cancelled",
	}
	if len(eventData) > 0 {
		append.Data = mustJSON(cloneCancelEventData(eventData))
	}
	_, _ = st.RunEvents().Append(ctx, append)
	return updated, nil
}

func cloneCancelEventData(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

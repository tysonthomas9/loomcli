package workflow

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestCancelRunRejectsTerminalAndLeavesRunUnchanged(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WF", Name: "Workflow"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	run, err := CreateOrResumeRun(ctx, st, "WF", RunParentWorkItemsName, []byte(`{"parentId":"EPIC-1"}`), "test")
	if err != nil {
		t.Fatalf("CreateOrResumeRun() error = %v", err)
	}
	finishedAt := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	finishedAtPtr := &finishedAt
	completed := domain.WorkflowRunCompleted
	if _, err := st.WorkflowRuns().Update(ctx, "WF", run.RunID, store.WorkflowRunUpdate{
		Status:     &completed,
		FinishedAt: &finishedAtPtr,
	}); err != nil {
		t.Fatalf("complete run: %v", err)
	}

	_, err = CancelRun(ctx, st, "WF", run.RunID, nil)
	var terminalErr *TerminalWorkflowRunError
	if !errors.As(err, &terminalErr) || !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("CancelRun() error = %v, want TerminalWorkflowRunError wrapping domain.ErrConflict", err)
	}
	if err.Error() != `cannot cancel workflow run `+run.RunID+`: already in terminal state "completed"` {
		t.Fatalf("CancelRun() error = %q, want terminal message", err.Error())
	}
	unchanged, err := st.WorkflowRuns().Get(ctx, "WF", run.RunID)
	if err != nil {
		t.Fatalf("get unchanged run: %v", err)
	}
	if unchanged.Status != domain.WorkflowRunCompleted ||
		unchanged.FinishedAt == nil || !unchanged.FinishedAt.Equal(finishedAt) {
		t.Fatalf("unchanged run = %+v, want completed status and original finished_at", unchanged)
	}
}

func TestCancelRunCancelsLiveRun(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WF", Name: "Workflow"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	run, err := CreateOrResumeRun(ctx, st, "WF", RunParentWorkItemsName, []byte(`{"parentId":"EPIC-1"}`), "test")
	if err != nil {
		t.Fatalf("CreateOrResumeRun() error = %v", err)
	}
	cancelled, err := CancelRun(ctx, st, "WF", run.RunID, map[string]any{"actor": "tester"})
	if err != nil {
		t.Fatalf("CancelRun() error = %v", err)
	}
	if cancelled.Status != domain.WorkflowRunCancelled || cancelled.FinishedAt == nil {
		t.Fatalf("cancelled run = %+v, want cancelled with finished_at", cancelled)
	}
	events, err := st.RunEvents().List(ctx, "WF", store.RunEventFilter{WorkflowRunID: run.RunID})
	if err != nil {
		t.Fatalf("list run events: %v", err)
	}
	foundCancel := false
	for _, event := range events {
		if event.Type == "workflow_cancelled" {
			foundCancel = true
			break
		}
	}
	if !foundCancel {
		t.Fatalf("events = %+v, want workflow_cancelled event", events)
	}
}

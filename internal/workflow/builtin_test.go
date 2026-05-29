package workflow

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestSlackCloneEpicWorkflowEnsuresTaskRuns(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	ib := clitest.NewMockIssueBackend()
	ready := backend.IssueData{ID: "SLACK-2", Title: "Build channel sidebar", Status: "open"}
	ib.ReadyResult = []backend.IssueData{ready}
	ib.ListResult = []backend.IssueData{ready}

	input := ParentWorkItemsInput{ParentID: "SLACK-1", Role: "task", MaxConcurrency: 2}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	run, err := CreateOrResumeRun(ctx, st, "WS", RunParentWorkItemsName, raw, "atlas")
	if err != nil {
		t.Fatalf("CreateOrResumeRun() error = %v", err)
	}
	if _, err := RunOnce(ctx, st, ib, run); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}

	taskRuns, err := st.TaskRuns().List(ctx, "WS", store.TaskRunFilter{WorkflowRunID: run.RunID, WorkItemID: ready.ID})
	if err != nil {
		t.Fatalf("list task runs: %v", err)
	}
	if len(taskRuns) != 1 {
		t.Fatalf("task runs = %d, want 1", len(taskRuns))
	}
	if taskRuns[0].Status != domain.TaskRunQueued || taskRuns[0].RoleName != "task" {
		t.Fatalf("task run = %+v, want queued task role", taskRuns[0])
	}

	events, err := st.RunEvents().List(ctx, "WS", store.RunEventFilter{WorkflowRunID: run.RunID})
	if err != nil {
		t.Fatalf("list run events: %v", err)
	}
	if len(events) < 2 {
		t.Fatalf("events = %d, want workflow start and task ensure events", len(events))
	}
	updated, err := st.WorkflowRuns().Get(ctx, "WS", run.RunID)
	if err != nil {
		t.Fatalf("get workflow run: %v", err)
	}
	if updated.Status != domain.WorkflowRunWaiting {
		t.Fatalf("workflow status = %q, want waiting", updated.Status)
	}
}

func TestCodeDefinedSlackCloneWorkflowDelegatesToBuiltinRunner(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	manifest := json.RawMessage(`{"name":"slack-clone-epic-runner","builtin":"run-parent-work-items"}`)
	if _, err := st.WorkflowDefinitions().Upsert(ctx, store.WorkflowDefinitionUpsert{
		WorkspaceKey: "WS",
		Name:         "slack-clone-epic-runner",
		Version:      "v1",
		Manifest:     manifest,
		Status:       domain.DefinitionStatusActive,
	}); err != nil {
		t.Fatalf("upsert workflow definition: %v", err)
	}

	ib := clitest.NewMockIssueBackend()
	task := backend.IssueData{ID: "SLACK-3", Title: "Add message composer", Status: "open"}
	ib.ReadyResult = []backend.IssueData{task}
	ib.ListResult = []backend.IssueData{task}
	raw := json.RawMessage(`{"parentId":"SLACK-1","role":"task","maxConcurrency":1}`)
	run, err := CreateOrResumeRun(ctx, st, "WS", "slack-clone-epic-runner", raw, "atlas")
	if err != nil {
		t.Fatalf("CreateOrResumeRun() error = %v", err)
	}
	if _, err := RunOnce(ctx, st, ib, run); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}

	taskRuns, err := st.TaskRuns().List(ctx, "WS", store.TaskRunFilter{WorkflowRunID: run.RunID, WorkItemID: task.ID})
	if err != nil {
		t.Fatalf("list task runs: %v", err)
	}
	if len(taskRuns) != 1 {
		t.Fatalf("task runs = %d, want 1 from code-defined workflow delegation", len(taskRuns))
	}
	events, err := st.RunEvents().List(ctx, "WS", store.RunEventFilter{WorkflowRunID: run.RunID})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	foundReconcile := false
	for _, event := range events {
		if event.Type == "workflow_ts_reconciled" {
			foundReconcile = true
			break
		}
	}
	if !foundReconcile {
		t.Fatalf("events = %+v, want workflow_ts_reconciled delegation event", events)
	}
}

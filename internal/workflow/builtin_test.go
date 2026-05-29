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

func TestParentWorkItemsWorkflowCompletesWhenNoOpenChildren(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	ib := clitest.NewMockIssueBackend()
	ib.ReadyResult = nil
	ib.BlockedResult = nil
	ib.ListResult = nil

	run, err := CreateOrResumeRun(ctx, st, "WS", RunParentWorkItemsName, json.RawMessage(`{"parentId":"DONE","role":"task"}`), "atlas")
	if err != nil {
		t.Fatalf("CreateOrResumeRun() error = %v", err)
	}
	result, err := RunBuiltinOnce(ctx, st, ib, run)
	if err != nil {
		t.Fatalf("RunBuiltinOnce() error = %v", err)
	}
	if !result.Done || result.Run.Status != domain.WorkflowRunCompleted {
		t.Fatalf("result = %+v, want completed done run", result)
	}
	events, err := st.RunEvents().List(ctx, "WS", store.RunEventFilter{WorkflowRunID: run.RunID})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if !hasWorkflowEvent(events, "workflow_completed") {
		t.Fatalf("events = %+v, want workflow_completed", events)
	}
}

func TestParentWorkItemHelpers(t *testing.T) {
	input, err := decodeParentInput(json.RawMessage(`{"parentId":"EPIC","maxConcurrency":0}`))
	if err != nil {
		t.Fatalf("decodeParentInput() error = %v", err)
	}
	if input.Role != "task" || input.MaxConcurrency != 4 {
		t.Fatalf("input = %+v, want default role/task concurrency", input)
	}
	if _, err := decodeParentInput(json.RawMessage(`{"parentId":`)); err == nil {
		t.Fatal("decodeParentInput() succeeded for invalid JSON")
	}

	live := map[string]struct{}{"TASK-1": {}}
	for _, child := range []backend.IssueData{
		{ID: "TASK-1", Status: "open"},
		{ID: "TASK-2", Status: "closed"},
		{ID: "TASK-3", Status: "deferred"},
	} {
		if shouldEnsureChild(child, live) {
			t.Fatalf("shouldEnsureChild(%+v) = true, want false", child)
		}
	}
	if !shouldEnsureChild(backend.IssueData{ID: "TASK-4", Status: "open"}, live) {
		t.Fatal("shouldEnsureChild(open unseen child) = false, want true")
	}

	ensure := taskRunEnsure(&domain.WorkflowRun{WorkspaceKey: "WS", RunID: "RUN", WorkflowName: "wf"}, input, backend.IssueData{ID: "TASK-4", Title: "Build"})
	if ensure.IdempotencyKey != "child:TASK-4:role:task" || ensure.Metadata["parent_id"] != "EPIC" {
		t.Fatalf("taskRunEnsure() = %+v", ensure)
	}
}

func hasWorkflowEvent(events []*domain.RunEvent, typ string) bool {
	for _, event := range events {
		if event != nil && event.Type == typ {
			return true
		}
	}
	return false
}

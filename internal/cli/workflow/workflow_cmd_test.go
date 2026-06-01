package workflow

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
	workflowpkg "github.com/tysonthomas9/loomcli/internal/workflow"
)

func TestParseInput(t *testing.T) {
	for _, input := range []string{"", "  ", `{"parentId":"EPIC-1"}`} {
		got, err := parseInput(input)
		if err != nil {
			t.Fatalf("parseInput(%q) error = %v", input, err)
		}
		if len(got) == 0 {
			t.Fatalf("parseInput(%q) returned empty input", input)
		}
	}
	if _, err := parseInput(`{"broken":`); err == nil {
		t.Fatal("parseInput() succeeded for invalid JSON")
	}
}

func TestParseWorkflowPayloadAlias(t *testing.T) {
	got, err := parseWorkflowPayload("{}", `{"parentId":"EPIC-1"}`)
	if err != nil {
		t.Fatalf("parseWorkflowPayload() error = %v", err)
	}
	if string(got) != `{"parentId":"EPIC-1"}` {
		t.Fatalf("payload = %s, want payload alias value", got)
	}
	if _, err := parseWorkflowPayload("{}", `{"broken":`); err == nil || !strings.Contains(err.Error(), "--payload must be valid JSON") {
		t.Fatalf("invalid payload error = %v, want --payload validation", err)
	}
	if _, err := parseWorkflowPayload(`{"input":true}`, `{"payload":true}`); err == nil || !strings.Contains(err.Error(), "cannot both be set") {
		t.Fatalf("conflicting input/payload error = %v, want conflict", err)
	}
	if workflowRunCmd.Flags().Lookup("payload") == nil {
		t.Fatal("loom workflow run missing --payload flag")
	}
}

func TestRunWorkflowRunRejectsInvalidInputBeforeOpeningStore(t *testing.T) {
	old := workflowRunInput
	workflowRunInput = `{"broken":`
	t.Cleanup(func() { workflowRunInput = old })

	err := runWorkflowRun(nil, []string{"epic-runner"})
	if err == nil || !strings.Contains(err.Error(), "--input must be valid JSON") {
		t.Fatalf("runWorkflowRun() error = %v, want invalid JSON", err)
	}
}

func TestWorkflowCommandsUseActiveWorkspaceStore(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WF", Name: "Workflow CLI"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	withWorkflowStore(t, st, "WF")
	withWorkflowGlobals(t, func() {
		ib := clitest.NewMockIssueBackend()
		task := backend.IssueData{ID: "TASK-1", Title: "Build inbox", Status: "open"}
		ib.ReadyResult = []backend.IssueData{task}
		ib.ListResult = []backend.IssueData{task}
		cli.SetDefaultIssueBackend(ib)
		t.Cleanup(cli.ResetDefaultIssueBackend)

		workflowRunInput = `{"parentId":"EPIC-1","role":"task","maxConcurrency":1}`
		workflowRunOnce = true
		workflowRunWait = false

		if err := runWorkflowList(nil, nil); err != nil {
			t.Fatalf("runWorkflowList() error = %v", err)
		}
		if err := runWorkflowRun(nil, []string{workflowpkg.RunParentWorkItemsName}); err != nil {
			t.Fatalf("runWorkflowRun() error = %v", err)
		}
		runs, err := st.WorkflowRuns().List(ctx, "WF", store.WorkflowRunFilter{})
		if err != nil {
			t.Fatalf("list workflow runs: %v", err)
		}
		if len(runs) != 1 || runs[0].Status != domain.WorkflowRunWaiting {
			t.Fatalf("runs = %+v, want one waiting run", runs)
		}
		if err := runWorkflowShow(nil, []string{runs[0].RunID}); err != nil {
			t.Fatalf("runWorkflowShow() error = %v", err)
		}
		if err := runWorkflowLogs(nil, []string{runs[0].RunID}); err != nil {
			t.Fatalf("runWorkflowLogs() error = %v", err)
		}
		if err := runWorkflowCancel(nil, []string{runs[0].RunID}); err != nil {
			t.Fatalf("runWorkflowCancel() error = %v", err)
		}
		cancelled, err := st.WorkflowRuns().Get(ctx, "WF", runs[0].RunID)
		if err != nil {
			t.Fatalf("get cancelled run: %v", err)
		}
		if cancelled.Status != domain.WorkflowRunCancelled {
			t.Fatalf("cancelled status = %s, want cancelled", cancelled.Status)
		}
	})
}

func TestWorkflowCancelJSON(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WF", Name: "Workflow CLI"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := workflowpkg.EnsureBuiltins(ctx, st, "WF"); err != nil {
		t.Fatalf("EnsureBuiltins() error = %v", err)
	}
	run, err := workflowpkg.CreateOrResumeRun(ctx, st, "WF", workflowpkg.RunParentWorkItemsName, []byte(`{"parentId":"EPIC-1"}`), "test")
	if err != nil {
		t.Fatalf("CreateOrResumeRun() error = %v", err)
	}

	withWorkflowStore(t, st, "WF")
	withWorkflowGlobals(t, func() {
		workflowCancelJSON = true
		oldWriteJSON := workflowWriteJSON
		var written any
		workflowWriteJSON = func(v any) error {
			written = v
			return nil
		}
		t.Cleanup(func() { workflowWriteJSON = oldWriteJSON })

		if err := runWorkflowCancel(nil, []string{run.RunID}); err != nil {
			t.Fatalf("runWorkflowCancel() error = %v", err)
		}
		cancelled, ok := written.(*domain.WorkflowRun)
		if !ok {
			t.Fatalf("workflowWriteJSON() value = %T, want *domain.WorkflowRun", written)
		}
		if cancelled.RunID != run.RunID || cancelled.Status != domain.WorkflowRunCancelled {
			t.Fatalf("cancelled run = %+v, want run %s cancelled", cancelled, run.RunID)
		}
	})
}

func TestWorkflowTasksJSON(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WF", Name: "Workflow CLI"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := workflowpkg.EnsureBuiltins(ctx, st, "WF"); err != nil {
		t.Fatalf("EnsureBuiltins() error = %v", err)
	}
	run, err := workflowpkg.CreateOrResumeRun(ctx, st, "WF", workflowpkg.RunParentWorkItemsName, []byte(`{"parentId":"EPIC-1"}`), "test")
	if err != nil {
		t.Fatalf("CreateOrResumeRun() error = %v", err)
	}
	if _, err := st.TaskRuns().Ensure(ctx, store.TaskRunEnsure{
		WorkspaceKey:  "WF",
		WorkflowRunID: run.RunID,
		WorkItemID:    "TASK-1",
		RoleName:      "coder",
		Status:        domain.TaskRunRunning,
	}); err != nil {
		t.Fatalf("ensure task run: %v", err)
	}

	withWorkflowStore(t, st, "WF")
	withWorkflowGlobals(t, func() {
		workflowTasksJSON = true
		oldWriteJSON := workflowWriteJSON
		var written any
		workflowWriteJSON = func(v any) error {
			written = v
			return nil
		}
		t.Cleanup(func() { workflowWriteJSON = oldWriteJSON })

		if err := runWorkflowTasks(nil, []string{run.RunID}); err != nil {
			t.Fatalf("runWorkflowTasks() error = %v", err)
		}
		taskRuns, ok := written.([]*domain.TaskRun)
		if !ok {
			t.Fatalf("workflowWriteJSON() value = %T, want []*domain.TaskRun", written)
		}
		if len(taskRuns) != 1 || taskRuns[0].WorkflowRunID != run.RunID || taskRuns[0].WorkItemID != "TASK-1" {
			t.Fatalf("task runs = %+v, want TASK-1 for run %s", taskRuns, run.RunID)
		}
	})
}

func TestWorkflowArtifactsJSON(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WF", Name: "Workflow CLI"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := workflowpkg.EnsureBuiltins(ctx, st, "WF"); err != nil {
		t.Fatalf("EnsureBuiltins() error = %v", err)
	}
	run, err := workflowpkg.CreateOrResumeRun(ctx, st, "WF", workflowpkg.RunParentWorkItemsName, []byte(`{"parentId":"EPIC-1"}`), "test")
	if err != nil {
		t.Fatalf("CreateOrResumeRun() error = %v", err)
	}
	if _, err := st.TaskRuns().Ensure(ctx, store.TaskRunEnsure{
		WorkspaceKey:  "WF",
		WorkflowRunID: run.RunID,
		WorkItemID:    "TASK-1",
		RoleName:      "coder",
		Status:        domain.TaskRunRunning,
		SessionID:     "session-taskrun",
	}); err != nil {
		t.Fatalf("ensure task run: %v", err)
	}
	if _, err := st.Artifacts().Create(ctx, store.ArtifactCreate{
		WorkspaceKey: "WF",
		ArtifactID:   "artifact-task",
		TaskID:       "TASK-1",
		Type:         "report",
		URI:          "artifact://task/report.json",
	}); err != nil {
		t.Fatalf("create task artifact: %v", err)
	}
	if _, err := st.Artifacts().Create(ctx, store.ArtifactCreate{
		WorkspaceKey: "WF",
		ArtifactID:   "artifact-controller",
		Type:         "report",
		URI:          "artifact://controller/report.json",
		Metadata: map[string]string{
			"workflow_run_id": run.RunID,
		},
	}); err != nil {
		t.Fatalf("create controller artifact: %v", err)
	}
	if _, err := st.Artifacts().Create(ctx, store.ArtifactCreate{
		WorkspaceKey: "WF",
		ArtifactID:   "artifact-session",
		SessionID:    "session-taskrun",
		Type:         "report",
		URI:          "artifact://session/report.json",
	}); err != nil {
		t.Fatalf("create session artifact: %v", err)
	}
	if _, err := st.Artifacts().Create(ctx, store.ArtifactCreate{
		WorkspaceKey: "WF",
		ArtifactID:   "artifact-other",
		Type:         "report",
		URI:          "artifact://other/report.json",
		Metadata: map[string]string{
			"workflow_run_id": "other-run",
		},
	}); err != nil {
		t.Fatalf("create unrelated artifact: %v", err)
	}

	withWorkflowStore(t, st, "WF")
	withWorkflowGlobals(t, func() {
		workflowArtifactsJSON = true
		workflowArtifactsType = "report"
		oldWriteJSON := workflowWriteJSON
		var written any
		workflowWriteJSON = func(v any) error {
			written = v
			return nil
		}
		t.Cleanup(func() { workflowWriteJSON = oldWriteJSON })

		if err := runWorkflowArtifacts(nil, []string{run.RunID}); err != nil {
			t.Fatalf("runWorkflowArtifacts() error = %v", err)
		}
		artifacts, ok := written.([]*domain.Artifact)
		if !ok {
			t.Fatalf("workflowWriteJSON() value = %T, want []*domain.Artifact", written)
		}
		if len(artifacts) != 3 ||
			!hasArtifact(artifacts, "artifact-task") ||
			!hasArtifact(artifacts, "artifact-controller") ||
			!hasArtifact(artifacts, "artifact-session") {
			t.Fatalf("artifacts = %+v, want task, controller, and session artifacts for run %s", artifacts, run.RunID)
		}
	})
}

func TestWorkflowSessionsJSON(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WF", Name: "Workflow CLI"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := workflowpkg.EnsureBuiltins(ctx, st, "WF"); err != nil {
		t.Fatalf("EnsureBuiltins() error = %v", err)
	}
	run, err := workflowpkg.CreateOrResumeRun(ctx, st, "WF", workflowpkg.RunParentWorkItemsName, []byte(`{"parentId":"EPIC-1"}`), "test")
	if err != nil {
		t.Fatalf("CreateOrResumeRun() error = %v", err)
	}
	if _, err := st.TaskRuns().Ensure(ctx, store.TaskRunEnsure{
		WorkspaceKey:  "WF",
		WorkflowRunID: run.RunID,
		WorkItemID:    "TASK-1",
		RoleName:      "coder",
		Status:        domain.TaskRunRunning,
		SessionID:     "session-taskrun",
	}); err != nil {
		t.Fatalf("ensure task run: %v", err)
	}
	if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "WF",
		SessionID:    "session-metadata",
		AgentID:      "nova",
		Kind:         domain.AgentSessionKindTask,
		Status:       domain.AgentSessionRunning,
		Metadata: map[string]string{
			"workflow_run_id": run.RunID,
		},
	}); err != nil {
		t.Fatalf("create workflow metadata session: %v", err)
	}
	if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "WF",
		SessionID:    "session-task",
		AgentID:      "nova",
		Kind:         domain.AgentSessionKindTask,
		TaskID:       "TASK-1",
		Status:       domain.AgentSessionRunning,
	}); err != nil {
		t.Fatalf("create task-linked session: %v", err)
	}
	if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "WF",
		SessionID:    "session-taskrun",
		AgentID:      "nova",
		Kind:         domain.AgentSessionKindTask,
		Status:       domain.AgentSessionRunning,
	}); err != nil {
		t.Fatalf("create task-run session: %v", err)
	}
	if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "WF",
		SessionID:    "session-other",
		AgentID:      "nova",
		Kind:         domain.AgentSessionKindTask,
		Status:       domain.AgentSessionRunning,
		Metadata: map[string]string{
			"workflow_run_id": "other-run",
		},
	}); err != nil {
		t.Fatalf("create unrelated session: %v", err)
	}
	if _, err := st.AgentSessionOperations().Upsert(ctx, store.AgentSessionOperationUpsert{
		WorkspaceKey:  "WF",
		OperationID:   "operation-run",
		SessionID:     "session-metadata",
		AgentID:       "nova",
		WorkflowRunID: run.RunID,
		Kind:          "prompt",
		Status:        domain.AgentSessionOperationCompleted,
	}); err != nil {
		t.Fatalf("upsert run-linked operation: %v", err)
	}
	if _, err := st.AgentSessionOperations().Upsert(ctx, store.AgentSessionOperationUpsert{
		WorkspaceKey: "WF",
		OperationID:  "operation-task",
		SessionID:    "session-task",
		AgentID:      "nova",
		TaskID:       "TASK-1",
		Kind:         "shell",
		Status:       domain.AgentSessionOperationCompleted,
	}); err != nil {
		t.Fatalf("upsert task-linked operation: %v", err)
	}
	if _, err := st.AgentSessionOperations().Upsert(ctx, store.AgentSessionOperationUpsert{
		WorkspaceKey: "WF",
		OperationID:  "operation-other",
		SessionID:    "session-other",
		AgentID:      "nova",
		Kind:         "prompt",
		Status:       domain.AgentSessionOperationCompleted,
	}); err != nil {
		t.Fatalf("upsert unrelated operation: %v", err)
	}
	if _, err := st.AgentSessionToolCalls().Upsert(ctx, store.AgentSessionToolCallUpsert{
		WorkspaceKey:  "WF",
		CallID:        "call-run",
		OperationID:   "operation-run",
		SessionID:     "session-metadata",
		AgentID:       "nova",
		WorkflowRunID: run.RunID,
		Name:          "lookup",
		Status:        "completed",
	}); err != nil {
		t.Fatalf("upsert run-linked tool call: %v", err)
	}
	if _, err := st.AgentSessionToolCalls().Upsert(ctx, store.AgentSessionToolCallUpsert{
		WorkspaceKey: "WF",
		CallID:       "call-task",
		OperationID:  "operation-task",
		SessionID:    "session-task",
		AgentID:      "nova",
		TaskID:       "TASK-1",
		Name:         "shell",
		Status:       "completed",
	}); err != nil {
		t.Fatalf("upsert task-linked tool call: %v", err)
	}
	if _, err := st.AgentSessionToolCalls().Upsert(ctx, store.AgentSessionToolCallUpsert{
		WorkspaceKey: "WF",
		CallID:       "call-other",
		OperationID:  "operation-other",
		SessionID:    "session-other",
		AgentID:      "nova",
		Name:         "lookup",
		Status:       "completed",
	}); err != nil {
		t.Fatalf("upsert unrelated tool call: %v", err)
	}

	withWorkflowStore(t, st, "WF")
	withWorkflowGlobals(t, func() {
		workflowSessionsJSON = true
		oldWriteJSON := workflowWriteJSON
		var written any
		workflowWriteJSON = func(v any) error {
			written = v
			return nil
		}
		t.Cleanup(func() { workflowWriteJSON = oldWriteJSON })

		if err := runWorkflowSessions(nil, []string{run.RunID}); err != nil {
			t.Fatalf("runWorkflowSessions() error = %v", err)
		}
		sessions, ok := written.([]*domain.AgentSession)
		if !ok {
			t.Fatalf("workflowWriteJSON() value = %T, want []*domain.AgentSession", written)
		}
		if len(sessions) != 3 ||
			!hasSession(sessions, "session-metadata") ||
			!hasSession(sessions, "session-task") ||
			!hasSession(sessions, "session-taskrun") {
			t.Fatalf("sessions = %+v, want metadata, task-linked, and task-run session links for run %s", sessions, run.RunID)
		}
	})

	withWorkflowGlobals(t, func() {
		workflowOperationsJSON = true
		oldWriteJSON := workflowWriteJSON
		var written any
		workflowWriteJSON = func(v any) error {
			written = v
			return nil
		}
		t.Cleanup(func() { workflowWriteJSON = oldWriteJSON })

		if err := runWorkflowOperations(nil, []string{run.RunID}); err != nil {
			t.Fatalf("runWorkflowOperations() error = %v", err)
		}
		operations, ok := written.([]*domain.AgentSessionOperation)
		if !ok {
			t.Fatalf("workflowWriteJSON() value = %T, want []*domain.AgentSessionOperation", written)
		}
		if len(operations) != 2 ||
			!hasOperation(operations, "operation-run") ||
			!hasOperation(operations, "operation-task") {
			t.Fatalf("operations = %+v, want run-linked and task-linked operation roots for run %s", operations, run.RunID)
		}
	})

	withWorkflowGlobals(t, func() {
		workflowToolCallsJSON = true
		oldWriteJSON := workflowWriteJSON
		var written any
		workflowWriteJSON = func(v any) error {
			written = v
			return nil
		}
		t.Cleanup(func() { workflowWriteJSON = oldWriteJSON })

		if err := runWorkflowToolCalls(nil, []string{run.RunID}); err != nil {
			t.Fatalf("runWorkflowToolCalls() error = %v", err)
		}
		calls, ok := written.([]*domain.AgentSessionToolCall)
		if !ok {
			t.Fatalf("workflowWriteJSON() value = %T, want []*domain.AgentSessionToolCall", written)
		}
		if len(calls) != 2 ||
			!hasToolCall(calls, "call-run") ||
			!hasToolCall(calls, "call-task") {
			t.Fatalf("tool calls = %+v, want run-linked and task-linked tool-call roots for run %s", calls, run.RunID)
		}
	})
}

func TestWorkflowOperationCancelUpdatesOperationAndToolCalls(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WF", Name: "Workflow CLI"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := workflowpkg.EnsureBuiltins(ctx, st, "WF"); err != nil {
		t.Fatalf("EnsureBuiltins() error = %v", err)
	}
	run, err := workflowpkg.CreateOrResumeRun(ctx, st, "WF", workflowpkg.RunParentWorkItemsName, []byte(`{"parentId":"EPIC-1"}`), "test")
	if err != nil {
		t.Fatalf("CreateOrResumeRun() error = %v", err)
	}
	startedAt := time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC)
	if _, err := st.AgentSessionOperations().Upsert(ctx, store.AgentSessionOperationUpsert{
		WorkspaceKey:  "WF",
		OperationID:   "op-cancel",
		SessionID:     "session-cancel",
		AgentID:       "nova",
		WorkflowRunID: run.RunID,
		TaskID:        "TASK-1",
		Kind:          "prompt",
		Status:        domain.AgentSessionOperationRunning,
		StartedAt:     startedAt,
	}); err != nil {
		t.Fatalf("upsert operation: %v", err)
	}
	if _, err := st.AgentSessionToolCalls().Upsert(ctx, store.AgentSessionToolCallUpsert{
		WorkspaceKey:  "WF",
		CallID:        "call-cancel",
		OperationID:   "op-cancel",
		SessionID:     "session-cancel",
		AgentID:       "nova",
		WorkflowRunID: run.RunID,
		TaskID:        "TASK-1",
		Name:          "lookup",
		Status:        "running",
	}); err != nil {
		t.Fatalf("upsert tool call: %v", err)
	}

	withWorkflowStore(t, st, "WF")
	withWorkflowGlobals(t, func() {
		workflowOperationCancelJSON = true
		workflowOperationCancelReason = "stale request"
		oldWriteJSON := workflowWriteJSON
		var written any
		workflowWriteJSON = func(v any) error {
			written = v
			return nil
		}
		t.Cleanup(func() { workflowWriteJSON = oldWriteJSON })

		if err := runWorkflowOperationCancel(nil, []string{"op-cancel"}); err != nil {
			t.Fatalf("runWorkflowOperationCancel() error = %v", err)
		}
		operation, ok := written.(*domain.AgentSessionOperation)
		if !ok {
			t.Fatalf("workflowWriteJSON() value = %T, want *domain.AgentSessionOperation", written)
		}
		if operation.Status != domain.AgentSessionOperationCancelled ||
			operation.ErrorClass != "cancelled" ||
			operation.ErrorMessage != "stale request" ||
			operation.Metadata["cancel_reason"] != "stale request" ||
			operation.CompletedAt == nil ||
			operation.DurationMS <= 0 {
			t.Fatalf("cancelled operation = %+v, want cancelled state with reason and duration", operation)
		}
	})

	call, err := st.AgentSessionToolCalls().Get(ctx, "WF", "call-cancel")
	if err != nil {
		t.Fatalf("get cancelled tool call: %v", err)
	}
	if call.Status != "cancelled" || call.ErrorMessage != "stale request" || call.CompletedAt == nil {
		t.Fatalf("tool call = %+v, want cancellation propagated", call)
	}
	events, err := st.RunEvents().List(ctx, "WF", store.RunEventFilter{WorkflowRunID: run.RunID})
	if err != nil {
		t.Fatalf("list run events: %v", err)
	}
	foundCancelEvent := false
	for _, event := range events {
		if event.Type == "agent_session_operation_cancelled" &&
			strings.Contains(string(event.Data), `"operation_id":"op-cancel"`) {
			foundCancelEvent = true
			break
		}
	}
	if !foundCancelEvent {
		t.Fatalf("events = %+v, want operation cancellation audit event", events)
	}
}

func TestWorkflowRouteAndTriggerBindJSON(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WF", Name: "Workflow CLI"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := workflowpkg.EnsureBuiltins(ctx, st, "WF"); err != nil {
		t.Fatalf("EnsureBuiltins() error = %v", err)
	}

	withWorkflowStore(t, st, "WF")
	withWorkflowGlobals(t, func() {
		oldWriteJSON := workflowWriteJSON
		var written any
		workflowWriteJSON = func(v any) error {
			written = v
			return nil
		}
		t.Cleanup(func() { workflowWriteJSON = oldWriteJSON })

		workflowRouteJSON = true
		workflowRouteAuth = "workspace"
		if err := runWorkflowRouteBind(nil, []string{workflowpkg.RunParentWorkItemsName, "hooks/epic"}); err != nil {
			t.Fatalf("runWorkflowRouteBind() error = %v", err)
		}
		route, ok := written.(*domain.RouteBinding)
		if !ok {
			t.Fatalf("workflowWriteJSON() route value = %T, want *domain.RouteBinding", written)
		}
		if route.DefinitionName != workflowpkg.RunParentWorkItemsName ||
			route.Path != "/hooks/epic" ||
			route.Method != "POST" ||
			route.AuthPolicy != "workspace" {
			t.Fatalf("route binding = %+v, want POST /hooks/epic workspace binding", route)
		}
		if err := runWorkflowRouteList(nil, []string{workflowpkg.RunParentWorkItemsName}); err != nil {
			t.Fatalf("runWorkflowRouteList() error = %v", err)
		}
		routes, ok := written.([]*domain.RouteBinding)
		if !ok {
			t.Fatalf("workflowWriteJSON() route list value = %T, want []*domain.RouteBinding", written)
		}
		if len(routes) != 1 || routes[0].BindingID != route.BindingID {
			t.Fatalf("route list = %+v, want active route binding", routes)
		}
		if err := runWorkflowRouteRemove(nil, []string{workflowpkg.RunParentWorkItemsName, "hooks/epic"}); err != nil {
			t.Fatalf("runWorkflowRouteRemove() error = %v", err)
		}
		removedRoute, ok := written.(*domain.RouteBinding)
		if !ok {
			t.Fatalf("workflowWriteJSON() removed route value = %T, want *domain.RouteBinding", written)
		}
		if removedRoute.Status != domain.DefinitionStatusDisabled {
			t.Fatalf("removed route = %+v, want disabled status", removedRoute)
		}
		if err := runWorkflowRouteList(nil, []string{workflowpkg.RunParentWorkItemsName}); err != nil {
			t.Fatalf("runWorkflowRouteList(after remove) error = %v", err)
		}
		routes, ok = written.([]*domain.RouteBinding)
		if !ok || len(routes) != 0 {
			t.Fatalf("route list after remove = %+v (%T), want no active routes", written, written)
		}

		workflowTriggerJSON = true
		workflowTriggerFilter = `{"label":"ready","type":"epic","merged":true,"attempt":2}`
		if err := runWorkflowTriggerBind(nil, []string{workflowpkg.RunParentWorkItemsName, "issue.label_added"}); err != nil {
			t.Fatalf("runWorkflowTriggerBind() error = %v", err)
		}
		trigger, ok := written.(*domain.TriggerBinding)
		if !ok {
			t.Fatalf("workflowWriteJSON() trigger value = %T, want *domain.TriggerBinding", written)
		}
		if trigger.WorkflowName != workflowpkg.RunParentWorkItemsName ||
			trigger.EventType != "issue.label_added" ||
			!strings.Contains(string(trigger.Filter), `"label":"ready"`) ||
			!strings.Contains(string(trigger.Filter), `"merged":"true"`) ||
			!strings.Contains(string(trigger.Filter), `"attempt":"2"`) {
			t.Fatalf("trigger binding = %+v, want normalized issue label trigger", trigger)
		}
		if err := runWorkflowTriggerList(nil, []string{workflowpkg.RunParentWorkItemsName}); err != nil {
			t.Fatalf("runWorkflowTriggerList() error = %v", err)
		}
		triggers, ok := written.([]*domain.TriggerBinding)
		if !ok {
			t.Fatalf("workflowWriteJSON() trigger list value = %T, want []*domain.TriggerBinding", written)
		}
		if len(triggers) != 1 || triggers[0].BindingID != trigger.BindingID {
			t.Fatalf("trigger list = %+v, want active trigger binding", triggers)
		}
		if err := runWorkflowTriggerRemove(nil, []string{workflowpkg.RunParentWorkItemsName, "issue.label_added"}); err != nil {
			t.Fatalf("runWorkflowTriggerRemove() error = %v", err)
		}
		removedTrigger, ok := written.(*domain.TriggerBinding)
		if !ok {
			t.Fatalf("workflowWriteJSON() removed trigger value = %T, want *domain.TriggerBinding", written)
		}
		if removedTrigger.Status != domain.DefinitionStatusDisabled {
			t.Fatalf("removed trigger = %+v, want disabled status", removedTrigger)
		}
		if err := runWorkflowTriggerList(nil, []string{workflowpkg.RunParentWorkItemsName}); err != nil {
			t.Fatalf("runWorkflowTriggerList(after remove) error = %v", err)
		}
		triggers, ok = written.([]*domain.TriggerBinding)
		if !ok || len(triggers) != 0 {
			t.Fatalf("trigger list after remove = %+v (%T), want no active triggers", written, written)
		}
	})
}

func TestWaitWorkflowReturnsTerminalRun(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WF", Name: "Workflow CLI"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := workflowpkg.EnsureBuiltins(ctx, st, "WF"); err != nil {
		t.Fatalf("EnsureBuiltins() error = %v", err)
	}
	run, err := workflowpkg.CreateOrResumeRun(ctx, st, "WF", workflowpkg.RunParentWorkItemsName, []byte(`{"parentId":"EPIC-1"}`), "test")
	if err != nil {
		t.Fatalf("CreateOrResumeRun() error = %v", err)
	}
	completed := domain.WorkflowRunCompleted
	if _, err := st.WorkflowRuns().Update(ctx, "WF", run.RunID, store.WorkflowRunUpdate{Status: &completed}); err != nil {
		t.Fatalf("complete workflow run: %v", err)
	}
	got, err := waitWorkflow(ctx, st, "WF", run.RunID)
	if err != nil {
		t.Fatalf("waitWorkflow() error = %v", err)
	}
	if got.RunID != run.RunID || got.Status != domain.WorkflowRunCompleted {
		t.Fatalf("waitWorkflow() = %+v, want completed run %s", got, run.RunID)
	}
}

func TestWorkflowActorName(t *testing.T) {
	t.Setenv("LOOM_ACTOR", " runner ")
	if got := actorName(); got != "runner" {
		t.Fatalf("actorName() = %q, want runner", got)
	}
	t.Setenv("LOOM_ACTOR", "")
	t.Setenv("USER", "")
	if got := actorName(); got != "loom" {
		t.Fatalf("actorName() = %q, want loom", got)
	}
}

func hasArtifact(artifacts []*domain.Artifact, id string) bool {
	for _, artifact := range artifacts {
		if artifact != nil && artifact.ArtifactID == id {
			return true
		}
	}
	return false
}

func hasSession(sessions []*domain.AgentSession, id string) bool {
	for _, session := range sessions {
		if session != nil && session.SessionID == id {
			return true
		}
	}
	return false
}

func hasOperation(operations []*domain.AgentSessionOperation, id string) bool {
	for _, operation := range operations {
		if operation != nil && operation.OperationID == id {
			return true
		}
	}
	return false
}

func hasToolCall(calls []*domain.AgentSessionToolCall, id string) bool {
	for _, call := range calls {
		if call != nil && call.CallID == id {
			return true
		}
	}
	return false
}

func withWorkflowStore(t *testing.T, st store.Store, workspace string) {
	t.Helper()
	old := workflowWithActiveWorkspace
	workflowWithActiveWorkspace = func(fn func(context.Context, *bootstrap.StoreHandle, string) error) error {
		return fn(context.Background(), &bootstrap.StoreHandle{Store: st}, workspace)
	}
	t.Cleanup(func() { workflowWithActiveWorkspace = old })
}

func withWorkflowGlobals(t *testing.T, fn func()) {
	t.Helper()
	oldWorkflowJSON := workflowJSON
	oldWorkflowRunInput, oldWorkflowRunPayload, oldWorkflowRunWait, oldWorkflowRunOnce := workflowRunInput, workflowRunPayload, workflowRunWait, workflowRunOnce
	oldWorkflowArtifactsJSON, oldWorkflowArtifactsType := workflowArtifactsJSON, workflowArtifactsType
	oldWorkflowLogsJSON, oldWorkflowTasksJSON := workflowLogsJSON, workflowTasksJSON
	oldWorkflowSessionsJSON := workflowSessionsJSON
	oldWorkflowOperationsJSON := workflowOperationsJSON
	oldWorkflowOperationCancelJSON, oldWorkflowOperationCancelReason := workflowOperationCancelJSON, workflowOperationCancelReason
	oldWorkflowToolCallsJSON := workflowToolCallsJSON
	oldWorkflowShowJSON, oldWorkflowListJSON, oldWorkflowCancelJSON := workflowShowJSON, workflowListJSON, workflowCancelJSON
	oldWorkflowRouteAuth, oldWorkflowRouteJSON := workflowRouteAuth, workflowRouteJSON
	oldWorkflowTriggerFilter, oldWorkflowTriggerJSON := workflowTriggerFilter, workflowTriggerJSON
	t.Cleanup(func() {
		workflowJSON = oldWorkflowJSON
		workflowRunInput, workflowRunPayload, workflowRunWait, workflowRunOnce = oldWorkflowRunInput, oldWorkflowRunPayload, oldWorkflowRunWait, oldWorkflowRunOnce
		workflowArtifactsJSON, workflowArtifactsType = oldWorkflowArtifactsJSON, oldWorkflowArtifactsType
		workflowLogsJSON, workflowTasksJSON = oldWorkflowLogsJSON, oldWorkflowTasksJSON
		workflowSessionsJSON = oldWorkflowSessionsJSON
		workflowOperationsJSON = oldWorkflowOperationsJSON
		workflowOperationCancelJSON, workflowOperationCancelReason = oldWorkflowOperationCancelJSON, oldWorkflowOperationCancelReason
		workflowToolCallsJSON = oldWorkflowToolCallsJSON
		workflowShowJSON, workflowListJSON, workflowCancelJSON = oldWorkflowShowJSON, oldWorkflowListJSON, oldWorkflowCancelJSON
		workflowRouteAuth, workflowRouteJSON = oldWorkflowRouteAuth, oldWorkflowRouteJSON
		workflowTriggerFilter, workflowTriggerJSON = oldWorkflowTriggerFilter, oldWorkflowTriggerJSON
	})
	fn()
}

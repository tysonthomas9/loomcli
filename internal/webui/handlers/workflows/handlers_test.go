package workflows

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	defspkg "github.com/tysonthomas9/loomcli/internal/defs"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestWorkflowRunAPICreatesInspectableRun(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "Workflow Store"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	root := t.TempDir()
	if _, err := defspkg.ScaffoldWorkflow(root, "epic-runner"); err != nil {
		t.Fatalf("ScaffoldWorkflow() error = %v", err)
	}
	plan, err := defspkg.Load(root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if err := defspkg.Apply(ctx, st, "WS", "test", plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	ready := backend.IssueData{ID: "TASK-2", Title: "Build composer", Status: "open"}
	ib := testIssueBackend{ready: []backend.IssueData{ready}, list: []backend.IssueData{ready}}
	mux := workflowMux(st, ib)

	listRec := httptest.NewRecorder()
	mux.ServeHTTP(listRec, httptest.NewRequest(http.MethodGet, "/api/workspaces/WS/workflows", nil))
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200; body=%s", listRec.Code, listRec.Body.String())
	}
	var listed struct {
		Data []domain.WorkflowDefinition `json:"data"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if !hasWorkflowDefinition(listed.Data, "epic-runner") {
		t.Fatalf("listed workflows = %+v, want epic-runner", listed.Data)
	}

	runRec := postJSON(t, mux, "/api/workspaces/WS/workflows/epic-runner/runs", map[string]any{
		"input": map[string]any{
			"parentId":       "EPIC-1",
			"role":           "task",
			"maxConcurrency": 1,
		},
	})
	if runRec.Code != http.StatusCreated {
		t.Fatalf("run status = %d, want 201; body=%s", runRec.Code, runRec.Body.String())
	}
	var created runResponse
	if err := json.Unmarshal(runRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode run: %v", err)
	}
	if created.Run == nil || created.Run.Status != domain.WorkflowRunWaiting {
		t.Fatalf("created run = %+v, want waiting run", created.Run)
	}
	if created.Builtin == nil || len(created.Builtin.TaskRuns) != 1 {
		t.Fatalf("created builtin = %+v, want one ensured task run", created.Builtin)
	}

	admissionStreamRec := postJSONStream(t, mux, "/api/workspaces/WS/workflows/epic-runner/runs", map[string]any{
		"input": map[string]any{
			"parentId":       "EPIC-STREAM",
			"role":           "task",
			"maxConcurrency": 1,
		},
	})
	if admissionStreamRec.Code != http.StatusCreated {
		t.Fatalf("admission stream status = %d, want 201; body=%s", admissionStreamRec.Code, admissionStreamRec.Body.String())
	}
	if ct := admissionStreamRec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("admission stream content type = %q, want text/event-stream", ct)
	}
	if body := admissionStreamRec.Body.String(); !strings.Contains(body, "event: workflow_admission") ||
		!strings.Contains(body, "event: workflow_event") ||
		!strings.Contains(body, `"type":"workflow_api_admitted"`) ||
		!strings.Contains(body, `"type":"task_run_ensured"`) {
		t.Fatalf("admission stream body = %s, want admission and replayed workflow events", body)
	}

	listRunsRec := httptest.NewRecorder()
	mux.ServeHTTP(listRunsRec, httptest.NewRequest(http.MethodGet, "/api/workspaces/WS/workflow-runs?work_item_id=TASK-2&status=waiting", nil))
	if listRunsRec.Code != http.StatusOK {
		t.Fatalf("list runs status = %d, want 200; body=%s", listRunsRec.Code, listRunsRec.Body.String())
	}
	var listedRuns struct {
		Data []runListItem `json:"data"`
	}
	if err := json.Unmarshal(listRunsRec.Body.Bytes(), &listedRuns); err != nil {
		t.Fatalf("decode list runs: %v", err)
	}
	if len(listedRuns.Data) != 1 || listedRuns.Data[0].Run == nil || listedRuns.Data[0].Run.RunID != created.Run.RunID || len(listedRuns.Data[0].TaskRuns) != 1 {
		t.Fatalf("listed runs = %+v, want created run with related task run", listedRuns.Data)
	}

	parentRunsRec := httptest.NewRecorder()
	mux.ServeHTTP(parentRunsRec, httptest.NewRequest(http.MethodGet, "/api/workspaces/WS/workflow-runs?work_item_id=EPIC-1&status=waiting", nil))
	if parentRunsRec.Code != http.StatusOK {
		t.Fatalf("parent list runs status = %d, want 200; body=%s", parentRunsRec.Code, parentRunsRec.Body.String())
	}
	var parentRuns struct {
		Data []runListItem `json:"data"`
	}
	if err := json.Unmarshal(parentRunsRec.Body.Bytes(), &parentRuns); err != nil {
		t.Fatalf("decode parent list runs: %v", err)
	}
	if len(parentRuns.Data) != 1 || parentRuns.Data[0].Run == nil || parentRuns.Data[0].Run.RunID != created.Run.RunID {
		t.Fatalf("parent listed runs = %+v, want run matched by input parentId", parentRuns.Data)
	}

	showRec := httptest.NewRecorder()
	mux.ServeHTTP(showRec, httptest.NewRequest(http.MethodGet, "/api/workspaces/WS/workflow-runs/"+created.Run.RunID, nil))
	if showRec.Code != http.StatusOK {
		t.Fatalf("show status = %d, want 200; body=%s", showRec.Code, showRec.Body.String())
	}

	eventsRec := httptest.NewRecorder()
	mux.ServeHTTP(eventsRec, httptest.NewRequest(http.MethodGet, "/api/workspaces/WS/workflow-runs/"+created.Run.RunID+"/events", nil))
	if eventsRec.Code != http.StatusOK {
		t.Fatalf("events status = %d, want 200; body=%s", eventsRec.Code, eventsRec.Body.String())
	}
	var events struct {
		Data []domain.RunEvent `json:"data"`
	}
	if err := json.Unmarshal(eventsRec.Body.Bytes(), &events); err != nil {
		t.Fatalf("decode events: %v", err)
	}
	if !hasRunEvent(events.Data, "workflow_ts_context_started") || !hasRunEvent(events.Data, "workflow_log") || !hasRunEvent(events.Data, "task_run_ensured") || !hasRunEvent(events.Data, "task_run_dispatched") {
		t.Fatalf("events = %+v, want TypeScript WorkflowContext, log, ensure, and dispatch evidence", events.Data)
	}

	tasksRec := httptest.NewRecorder()
	mux.ServeHTTP(tasksRec, httptest.NewRequest(http.MethodGet, "/api/workspaces/WS/workflow-runs/"+created.Run.RunID+"/tasks", nil))
	if tasksRec.Code != http.StatusOK {
		t.Fatalf("tasks status = %d, want 200; body=%s", tasksRec.Code, tasksRec.Body.String())
	}
	var tasks struct {
		Data []domain.TaskRun `json:"data"`
	}
	if err := json.Unmarshal(tasksRec.Body.Bytes(), &tasks); err != nil {
		t.Fatalf("decode tasks: %v", err)
	}
	if len(tasks.Data) != 1 || tasks.Data[0].WorkflowRunID != created.Run.RunID || tasks.Data[0].WorkItemID != "TASK-2" {
		t.Fatalf("tasks = %+v, want run-linked task", tasks.Data)
	}

	if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "WS",
		SessionID:    "session-" + created.Run.RunID,
		AgentID:      "agent-task",
		NodeID:       "node-local",
		Kind:         domain.AgentSessionKindTask,
		TaskID:       "TASK-2",
		Status:       domain.AgentSessionRunning,
		Metadata:     map[string]string{"workflow_run_id": created.Run.RunID},
	}); err != nil {
		t.Fatalf("create workflow session: %v", err)
	}
	if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "WS",
		SessionID:    "session-unrelated",
		AgentID:      "agent-other",
		Kind:         domain.AgentSessionKindTask,
		TaskID:       "TASK-OTHER",
		Status:       domain.AgentSessionRunning,
	}); err != nil {
		t.Fatalf("create unrelated session: %v", err)
	}
	if _, err := st.Artifacts().Create(ctx, store.ArtifactCreate{
		WorkspaceKey: "WS",
		ArtifactID:   "artifact-" + created.Run.RunID,
		AgentID:      "agent-task",
		SessionID:    "session-" + created.Run.RunID,
		TaskID:       "TASK-2",
		Type:         "report",
		URI:          "artifact://workflow/report.json",
		Summary:      "workflow report",
		Metadata:     map[string]string{"workflow_run_id": created.Run.RunID},
	}); err != nil {
		t.Fatalf("create workflow artifact: %v", err)
	}
	if _, err := st.Artifacts().Create(ctx, store.ArtifactCreate{
		WorkspaceKey: "WS",
		ArtifactID:   "artifact-unrelated",
		AgentID:      "agent-other",
		TaskID:       "TASK-OTHER",
		Type:         "report",
		URI:          "artifact://other/report.json",
		Summary:      "unrelated report",
	}); err != nil {
		t.Fatalf("create unrelated artifact: %v", err)
	}
	if _, err := st.AgentSessionOperations().Upsert(ctx, store.AgentSessionOperationUpsert{
		WorkspaceKey:  "WS",
		OperationID:   "operation-" + created.Run.RunID,
		SessionID:     "session-" + created.Run.RunID,
		AgentID:       "agent-task",
		WorkflowRunID: created.Run.RunID,
		TaskID:        "TASK-2",
		Kind:          "prompt",
		Status:        domain.AgentSessionOperationCompleted,
	}); err != nil {
		t.Fatalf("upsert workflow operation: %v", err)
	}
	if _, err := st.AgentSessionOperations().Upsert(ctx, store.AgentSessionOperationUpsert{
		WorkspaceKey:  "WS",
		OperationID:   "operation-unrelated",
		SessionID:     "session-unrelated",
		AgentID:       "agent-other",
		WorkflowRunID: "other-run",
		TaskID:        "TASK-OTHER",
		Kind:          "prompt",
		Status:        domain.AgentSessionOperationCompleted,
	}); err != nil {
		t.Fatalf("upsert unrelated operation: %v", err)
	}
	if _, err := st.AgentSessionToolCalls().Upsert(ctx, store.AgentSessionToolCallUpsert{
		WorkspaceKey:  "WS",
		CallID:        "call-" + created.Run.RunID,
		OperationID:   "operation-" + created.Run.RunID,
		SessionID:     "session-" + created.Run.RunID,
		AgentID:       "agent-task",
		WorkflowRunID: created.Run.RunID,
		TaskID:        "TASK-2",
		Name:          "lookup",
		Status:        "completed",
	}); err != nil {
		t.Fatalf("upsert workflow tool call: %v", err)
	}
	if _, err := st.AgentSessionToolCalls().Upsert(ctx, store.AgentSessionToolCallUpsert{
		WorkspaceKey:  "WS",
		CallID:        "call-unrelated",
		OperationID:   "operation-unrelated",
		SessionID:     "session-unrelated",
		AgentID:       "agent-other",
		WorkflowRunID: "other-run",
		TaskID:        "TASK-OTHER",
		Name:          "lookup",
		Status:        "completed",
	}); err != nil {
		t.Fatalf("upsert unrelated tool call: %v", err)
	}

	sessionsRec := httptest.NewRecorder()
	mux.ServeHTTP(sessionsRec, httptest.NewRequest(http.MethodGet, "/api/workspaces/WS/workflow-runs/"+created.Run.RunID+"/sessions", nil))
	if sessionsRec.Code != http.StatusOK {
		t.Fatalf("sessions status = %d, want 200; body=%s", sessionsRec.Code, sessionsRec.Body.String())
	}
	var sessions struct {
		Data []domain.AgentSession `json:"data"`
	}
	if err := json.Unmarshal(sessionsRec.Body.Bytes(), &sessions); err != nil {
		t.Fatalf("decode sessions: %v", err)
	}
	if len(sessions.Data) != 1 || sessions.Data[0].SessionID != "session-"+created.Run.RunID {
		t.Fatalf("sessions = %+v, want workflow-linked agent session", sessions.Data)
	}

	operationsRec := httptest.NewRecorder()
	mux.ServeHTTP(operationsRec, httptest.NewRequest(http.MethodGet, "/api/workspaces/WS/workflow-runs/"+created.Run.RunID+"/operations", nil))
	if operationsRec.Code != http.StatusOK {
		t.Fatalf("operations status = %d, want 200; body=%s", operationsRec.Code, operationsRec.Body.String())
	}
	var operations struct {
		Data []domain.AgentSessionOperation `json:"data"`
	}
	if err := json.Unmarshal(operationsRec.Body.Bytes(), &operations); err != nil {
		t.Fatalf("decode operations: %v", err)
	}
	if len(operations.Data) != 1 || operations.Data[0].OperationID != "operation-"+created.Run.RunID {
		t.Fatalf("operations = %+v, want workflow-linked agent session operation", operations.Data)
	}

	toolCallsRec := httptest.NewRecorder()
	mux.ServeHTTP(toolCallsRec, httptest.NewRequest(http.MethodGet, "/api/workspaces/WS/workflow-runs/"+created.Run.RunID+"/tool-calls", nil))
	if toolCallsRec.Code != http.StatusOK {
		t.Fatalf("tool calls status = %d, want 200; body=%s", toolCallsRec.Code, toolCallsRec.Body.String())
	}
	var toolCalls struct {
		Data []domain.AgentSessionToolCall `json:"data"`
	}
	if err := json.Unmarshal(toolCallsRec.Body.Bytes(), &toolCalls); err != nil {
		t.Fatalf("decode tool calls: %v", err)
	}
	if len(toolCalls.Data) != 1 || toolCalls.Data[0].CallID != "call-"+created.Run.RunID {
		t.Fatalf("tool calls = %+v, want workflow-linked agent session tool call", toolCalls.Data)
	}

	if _, err := st.AgentSessionOperations().Upsert(ctx, store.AgentSessionOperationUpsert{
		WorkspaceKey:  "WS",
		OperationID:   "operation-cancel",
		SessionID:     "session-" + created.Run.RunID,
		AgentID:       "agent-task",
		WorkflowRunID: created.Run.RunID,
		TaskID:        "TASK-2",
		Kind:          "prompt",
		Status:        domain.AgentSessionOperationRunning,
	}); err != nil {
		t.Fatalf("upsert cancellable operation: %v", err)
	}
	if _, err := st.AgentSessionToolCalls().Upsert(ctx, store.AgentSessionToolCallUpsert{
		WorkspaceKey:  "WS",
		CallID:        "call-cancel",
		OperationID:   "operation-cancel",
		SessionID:     "session-" + created.Run.RunID,
		AgentID:       "agent-task",
		WorkflowRunID: created.Run.RunID,
		TaskID:        "TASK-2",
		Name:          "lookup",
		Status:        "running",
	}); err != nil {
		t.Fatalf("upsert cancellable tool call: %v", err)
	}
	cancelOperationRec := postJSON(t, mux, "/api/workspaces/WS/agent-session-operations/operation-cancel/cancel", map[string]any{"reason": "user stopped"})
	if cancelOperationRec.Code != http.StatusOK {
		t.Fatalf("cancel operation status = %d, want 200; body=%s", cancelOperationRec.Code, cancelOperationRec.Body.String())
	}
	var cancelledOperation domain.AgentSessionOperation
	if err := json.Unmarshal(cancelOperationRec.Body.Bytes(), &cancelledOperation); err != nil {
		t.Fatalf("decode cancelled operation: %v", err)
	}
	if cancelledOperation.Status != domain.AgentSessionOperationCancelled ||
		cancelledOperation.ErrorMessage != "user stopped" ||
		cancelledOperation.Metadata["cancel_reason"] != "user stopped" {
		t.Fatalf("cancelled operation = %+v, want cancelled operation with reason", cancelledOperation)
	}
	cancelledToolCall, err := st.AgentSessionToolCalls().Get(ctx, "WS", "call-cancel")
	if err != nil {
		t.Fatalf("get cancelled tool call: %v", err)
	}
	if cancelledToolCall.Status != "cancelled" || cancelledToolCall.ErrorMessage != "user stopped" {
		t.Fatalf("cancelled tool call = %+v, want propagated cancellation", cancelledToolCall)
	}

	artifactsRec := httptest.NewRecorder()
	mux.ServeHTTP(artifactsRec, httptest.NewRequest(http.MethodGet, "/api/workspaces/WS/workflow-runs/"+created.Run.RunID+"/artifacts?type=report", nil))
	if artifactsRec.Code != http.StatusOK {
		t.Fatalf("artifacts status = %d, want 200; body=%s", artifactsRec.Code, artifactsRec.Body.String())
	}
	var artifacts struct {
		Data []domain.Artifact `json:"data"`
	}
	if err := json.Unmarshal(artifactsRec.Body.Bytes(), &artifacts); err != nil {
		t.Fatalf("decode artifacts: %v", err)
	}
	if len(artifacts.Data) != 1 || artifacts.Data[0].ArtifactID != "artifact-"+created.Run.RunID {
		t.Fatalf("artifacts = %+v, want workflow-linked artifact", artifacts.Data)
	}

	streamRec := httptest.NewRecorder()
	mux.ServeHTTP(streamRec, httptest.NewRequest(http.MethodGet, "/api/workspaces/WS/workflow-runs/"+created.Run.RunID+"/events/stream?once=1", nil))
	if streamRec.Code != http.StatusOK {
		t.Fatalf("event stream status = %d, want 200; body=%s", streamRec.Code, streamRec.Body.String())
	}
	if ct := streamRec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("event stream content type = %q, want text/event-stream", ct)
	}
	if body := streamRec.Body.String(); !strings.Contains(body, "event: workflow_event") || !strings.Contains(body, `"type":"task_run_ensured"`) {
		t.Fatalf("event stream body = %s, want workflow_event SSE with task_run_ensured", body)
	}
	lastEventRec := httptest.NewRecorder()
	lastEventReq := httptest.NewRequest(http.MethodGet, "/api/workspaces/WS/workflow-runs/"+created.Run.RunID+"/events/stream?once=1", nil)
	lastEventReq.Header.Set("Last-Event-ID", "999")
	mux.ServeHTTP(lastEventRec, lastEventReq)
	if strings.Contains(lastEventRec.Body.String(), "event: workflow_event") {
		t.Fatalf("event stream replayed events after Last-Event-ID=999: %s", lastEventRec.Body.String())
	}

	secondRunRec := postJSON(t, mux, "/api/workspaces/WS/workflows/epic-runner/runs", map[string]any{
		"input": map[string]any{
			"parentId":       "EPIC-2",
			"role":           "task",
			"maxConcurrency": 1,
		},
	})
	if secondRunRec.Code != http.StatusCreated {
		t.Fatalf("second run status = %d, want 201; body=%s", secondRunRec.Code, secondRunRec.Body.String())
	}
	var secondCreated runResponse
	if err := json.Unmarshal(secondRunRec.Body.Bytes(), &secondCreated); err != nil {
		t.Fatalf("decode second run: %v", err)
	}
	multiStreamPath := "/api/workspaces/WS/workflow-runs/events/stream?once=1&run_id=" +
		url.QueryEscape(created.Run.RunID) + "&run_id=" + url.QueryEscape(secondCreated.Run.RunID)
	multiStreamRec := httptest.NewRecorder()
	mux.ServeHTTP(multiStreamRec, httptest.NewRequest(http.MethodGet, multiStreamPath, nil))
	if multiStreamRec.Code != http.StatusOK {
		t.Fatalf("multi event stream status = %d, want 200; body=%s", multiStreamRec.Code, multiStreamRec.Body.String())
	}
	if ct := multiStreamRec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("multi event stream content type = %q, want text/event-stream", ct)
	}
	multiBody := multiStreamRec.Body.String()
	if !strings.Contains(multiBody, "id: "+created.Run.RunID+":") ||
		!strings.Contains(multiBody, "id: "+secondCreated.Run.RunID+":") ||
		!strings.Contains(multiBody, "event: workflow_event") {
		t.Fatalf("multi event stream body = %s, want prefixed events for both runs", multiBody)
	}
	multiCursorPath := multiStreamPath + "&since=" + url.QueryEscape(created.Run.RunID+":999") +
		"&since=" + url.QueryEscape(secondCreated.Run.RunID+":999")
	multiCursorRec := httptest.NewRecorder()
	mux.ServeHTTP(multiCursorRec, httptest.NewRequest(http.MethodGet, multiCursorPath, nil))
	if strings.Contains(multiCursorRec.Body.String(), "event: workflow_event") {
		t.Fatalf("multi event stream replayed events after per-run cursors: %s", multiCursorRec.Body.String())
	}

	cancelRec := postJSON(t, mux, "/api/workspaces/WS/workflow-runs/"+created.Run.RunID+"/cancel", map[string]any{})
	if cancelRec.Code != http.StatusOK {
		t.Fatalf("cancel status = %d, want 200; body=%s", cancelRec.Code, cancelRec.Body.String())
	}
	var cancelled domain.WorkflowRun
	if err := json.Unmarshal(cancelRec.Body.Bytes(), &cancelled); err != nil {
		t.Fatalf("decode cancel: %v", err)
	}
	if cancelled.Status != domain.WorkflowRunCancelled {
		t.Fatalf("cancelled status = %s, want cancelled", cancelled.Status)
	}
}

func TestWorkflowRunStreamErrorEnvelope(t *testing.T) {
	rec := httptest.NewRecorder()
	sw, err := newWorkflowAdmissionStreamWriter(rec, http.StatusOK)
	if err != nil {
		t.Fatalf("newWorkflowAdmissionStreamWriter() error = %v", err)
	}
	if err := writeWorkflowRunStreamError(sw, []string{"wrun-1"}, errors.New("run disappeared")); err != nil {
		t.Fatalf("writeWorkflowRunStreamError() error = %v", err)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "event: workflow_run_stream_error") ||
		!strings.Contains(body, "id: error") ||
		!strings.Contains(body, `"run_ids":["wrun-1"]`) ||
		!strings.Contains(body, `"message":"run disappeared"`) ||
		!strings.Contains(body, `"terminal":true`) {
		t.Fatalf("stream error body = %s, want structured stream error envelope", body)
	}
}

func TestWorkflowRouteBindingAPIRunsBoundWorkflow(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "Workflow Store"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	root := t.TempDir()
	if _, err := defspkg.ScaffoldWorkflow(root, "epic-runner"); err != nil {
		t.Fatalf("ScaffoldWorkflow() error = %v", err)
	}
	plan, err := defspkg.Load(root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if err := defspkg.Apply(ctx, st, "WS", "test", plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	ready := backend.IssueData{ID: "TASK-3", Title: "Build route-bound runner", Status: "open"}
	ib := testIssueBackend{ready: []backend.IssueData{ready}, list: []backend.IssueData{ready}}
	mux := workflowMux(st, ib)
	rec := postJSON(t, mux, "/api/workspaces/WS/workflow-routes/workflows/epic-runner/run", map[string]any{
		"input": map[string]any{
			"parentId":       "EPIC-ROUTE",
			"role":           "task",
			"maxConcurrency": 1,
		},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("route run status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var created runResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode route run: %v", err)
	}
	if created.Run == nil || created.Run.WorkflowName != "epic-runner" || created.Run.Status != domain.WorkflowRunWaiting {
		t.Fatalf("route run = %+v, want waiting epic-runner run", created.Run)
	}
	if created.Builtin == nil || len(created.Builtin.TaskRuns) != 1 {
		t.Fatalf("route builtin = %+v, want one ensured task run", created.Builtin)
	}
	events, err := st.RunEvents().List(ctx, "WS", store.RunEventFilter{WorkflowRunID: created.Run.RunID})
	if err != nil {
		t.Fatalf("list run events: %v", err)
	}
	if !hasRunEventPtr(events, "workflow_route_admitted") || !hasRunEventPtr(events, "workflow_ts_context_started") || !hasRunEventPtr(events, "workflow_log") || !hasRunEventPtr(events, "task_run_ensured") || !hasRunEventPtr(events, "task_run_dispatched") {
		t.Fatalf("events = %+v, want TypeScript WorkflowContext, log, ensure, and dispatch evidence", events)
	}

	streamRec := postJSONStream(t, mux, "/api/workspaces/WS/workflow-routes/workflows/epic-runner/run", map[string]any{
		"input": map[string]any{
			"parentId":       "EPIC-ROUTE-STREAM",
			"role":           "task",
			"maxConcurrency": 1,
		},
	})
	if streamRec.Code != http.StatusCreated {
		t.Fatalf("route stream status = %d, want 201; body=%s", streamRec.Code, streamRec.Body.String())
	}
	if ct := streamRec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("route stream content type = %q, want text/event-stream", ct)
	}
	if body := streamRec.Body.String(); !strings.Contains(body, "event: workflow_admission") ||
		!strings.Contains(body, "event: workflow_event") ||
		!strings.Contains(body, `"type":"workflow_route_admitted"`) ||
		!strings.Contains(body, `"type":"task_run_ensured"`) {
		t.Fatalf("route stream body = %s, want admission and replayed workflow events", body)
	}

	terminalMux := workflowMux(st, testIssueBackend{})
	terminalStreamRec := postJSONStream(t, terminalMux, "/api/workspaces/WS/workflow-routes/workflows/epic-runner/run?until=terminal", map[string]any{
		"input": map[string]any{
			"parentId":       "EPIC-ROUTE-DONE",
			"role":           "task",
			"maxConcurrency": 1,
		},
	})
	if terminalStreamRec.Code != http.StatusCreated {
		t.Fatalf("terminal route stream status = %d, want 201; body=%s", terminalStreamRec.Code, terminalStreamRec.Body.String())
	}
	if body := terminalStreamRec.Body.String(); !strings.Contains(body, "event: workflow_run_stream_complete") ||
		!strings.Contains(body, `"status":"completed"`) ||
		!strings.Contains(body, `"workflow_name":"epic-runner"`) {
		t.Fatalf("terminal route stream body = %s, want terminal completion contract", body)
	}
}

func TestWorkflowRouteBindingAPIValidation(t *testing.T) {
	ctx := context.Background()
	st, mux := setupWorkflowTestMux(t, ctx, nil)

	missing := postJSON(t, mux, "/api/workspaces/WS/workflow-routes/missing/route", map[string]any{"once": false})
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing route status = %d, want 404; body=%s", missing.Code, missing.Body.String())
	}

	if _, err := st.RouteBindings().Upsert(ctx, store.RouteBindingUpsert{
		WorkspaceKey:   "WS",
		BindingID:      "workflow:epic-runner:POST:public",
		DefinitionName: "epic-runner",
		DefinitionType: domain.DefinitionTypeWorkflow,
		Path:           "/public",
		Method:         http.MethodPost,
		AuthPolicy:     "public",
		Status:         domain.DefinitionStatusActive,
	}); err != nil {
		t.Fatalf("upsert public route binding: %v", err)
	}
	unsupported := postJSON(t, mux, "/api/workspaces/WS/workflow-routes/public", map[string]any{"once": false})
	if unsupported.Code != http.StatusForbidden {
		t.Fatalf("unsupported auth route status = %d, want 403; body=%s", unsupported.Code, unsupported.Body.String())
	}
}

func TestWorkflowBindingManagementAPI(t *testing.T) {
	ctx := context.Background()
	_, mux := setupWorkflowTestMux(t, ctx, nil)

	routeCreate := postJSON(t, mux, "/api/workspaces/WS/workflows/epic-runner/routes", map[string]any{
		"path": "/api/epic-runner",
		"auth": "workspace",
	})
	if routeCreate.Code != http.StatusCreated {
		t.Fatalf("route create status = %d, want 201; body=%s", routeCreate.Code, routeCreate.Body.String())
	}
	var route domain.RouteBinding
	if err := json.Unmarshal(routeCreate.Body.Bytes(), &route); err != nil {
		t.Fatalf("decode route: %v", err)
	}
	if route.DefinitionName != "epic-runner" || route.Path != "/api/epic-runner" ||
		route.AuthPolicy != "workspace" || route.Status != domain.DefinitionStatusActive {
		t.Fatalf("route = %+v, want active api route binding", route)
	}

	routeList := httptest.NewRecorder()
	mux.ServeHTTP(routeList, httptest.NewRequest(http.MethodGet, "/api/workspaces/WS/workflow-route-bindings?workflow=epic-runner", nil))
	if routeList.Code != http.StatusOK {
		t.Fatalf("route list status = %d, want 200; body=%s", routeList.Code, routeList.Body.String())
	}
	var routes struct {
		Data []domain.RouteBinding `json:"data"`
	}
	if err := json.Unmarshal(routeList.Body.Bytes(), &routes); err != nil {
		t.Fatalf("decode route list: %v", err)
	}
	if !hasRouteBinding(routes.Data, route.BindingID) {
		t.Fatalf("routes = %+v, want created route binding", routes.Data)
	}

	routeDelete := httptest.NewRecorder()
	mux.ServeHTTP(routeDelete, httptest.NewRequest(http.MethodDelete, "/api/workspaces/WS/workflows/epic-runner/routes/api/epic-runner", nil))
	if routeDelete.Code != http.StatusOK {
		t.Fatalf("route delete status = %d, want 200; body=%s", routeDelete.Code, routeDelete.Body.String())
	}
	var disabledRoute domain.RouteBinding
	if err := json.Unmarshal(routeDelete.Body.Bytes(), &disabledRoute); err != nil {
		t.Fatalf("decode disabled route: %v", err)
	}
	if disabledRoute.BindingID != route.BindingID || disabledRoute.Status != domain.DefinitionStatusDisabled {
		t.Fatalf("disabled route = %+v, want disabled created binding", disabledRoute)
	}

	routeListAfter := httptest.NewRecorder()
	mux.ServeHTTP(routeListAfter, httptest.NewRequest(http.MethodGet, "/api/workspaces/WS/workflow-route-bindings?workflow=epic-runner", nil))
	var activeRoutes struct {
		Data []domain.RouteBinding `json:"data"`
	}
	if err := json.Unmarshal(routeListAfter.Body.Bytes(), &activeRoutes); err != nil {
		t.Fatalf("decode route list after disable: %v", err)
	}
	if hasRouteBinding(activeRoutes.Data, route.BindingID) {
		t.Fatalf("active routes after disable = %+v, disabled route still listed", activeRoutes.Data)
	}

	triggerCreate := postJSON(t, mux, "/api/workspaces/WS/workflows/epic-runner/triggers", map[string]any{
		"event":  "github.issue.opened",
		"filter": map[string]any{"repo": "loom"},
	})
	if triggerCreate.Code != http.StatusCreated {
		t.Fatalf("trigger create status = %d, want 201; body=%s", triggerCreate.Code, triggerCreate.Body.String())
	}
	var trigger domain.TriggerBinding
	if err := json.Unmarshal(triggerCreate.Body.Bytes(), &trigger); err != nil {
		t.Fatalf("decode trigger: %v", err)
	}
	if trigger.WorkflowName != "epic-runner" || trigger.EventType != "github.issue.opened" ||
		!strings.Contains(string(trigger.Filter), `"repo":"loom"`) ||
		trigger.Status != domain.DefinitionStatusActive {
		t.Fatalf("trigger = %+v, want active api trigger binding", trigger)
	}

	triggerList := httptest.NewRecorder()
	mux.ServeHTTP(triggerList, httptest.NewRequest(http.MethodGet, "/api/workspaces/WS/workflow-trigger-bindings?workflow=epic-runner", nil))
	if triggerList.Code != http.StatusOK {
		t.Fatalf("trigger list status = %d, want 200; body=%s", triggerList.Code, triggerList.Body.String())
	}
	var triggers struct {
		Data []domain.TriggerBinding `json:"data"`
	}
	if err := json.Unmarshal(triggerList.Body.Bytes(), &triggers); err != nil {
		t.Fatalf("decode trigger list: %v", err)
	}
	if !hasTriggerBinding(triggers.Data, trigger.BindingID) {
		t.Fatalf("triggers = %+v, want created trigger binding", triggers.Data)
	}

	triggerDelete := httptest.NewRecorder()
	mux.ServeHTTP(triggerDelete, httptest.NewRequest(http.MethodDelete, "/api/workspaces/WS/workflows/epic-runner/triggers/github.issue.opened", nil))
	if triggerDelete.Code != http.StatusOK {
		t.Fatalf("trigger delete status = %d, want 200; body=%s", triggerDelete.Code, triggerDelete.Body.String())
	}
	var disabledTrigger domain.TriggerBinding
	if err := json.Unmarshal(triggerDelete.Body.Bytes(), &disabledTrigger); err != nil {
		t.Fatalf("decode disabled trigger: %v", err)
	}
	if disabledTrigger.BindingID != trigger.BindingID || disabledTrigger.Status != domain.DefinitionStatusDisabled {
		t.Fatalf("disabled trigger = %+v, want disabled created binding", disabledTrigger)
	}

	triggerListAfter := httptest.NewRecorder()
	mux.ServeHTTP(triggerListAfter, httptest.NewRequest(http.MethodGet, "/api/workspaces/WS/workflow-trigger-bindings?workflow=epic-runner", nil))
	var activeTriggers struct {
		Data []domain.TriggerBinding `json:"data"`
	}
	if err := json.Unmarshal(triggerListAfter.Body.Bytes(), &activeTriggers); err != nil {
		t.Fatalf("decode trigger list after disable: %v", err)
	}
	if hasTriggerBinding(activeTriggers.Data, trigger.BindingID) {
		t.Fatalf("active triggers after disable = %+v, disabled trigger still listed", activeTriggers.Data)
	}
}

func TestWorkflowTriggerBindingAPIRunsMatchingWorkflow(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "Workflow Store"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	root := t.TempDir()
	if _, err := defspkg.ScaffoldWorkflow(root, "epic-runner"); err != nil {
		t.Fatalf("ScaffoldWorkflow() error = %v", err)
	}
	plan, err := defspkg.Load(root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if err := defspkg.Apply(ctx, st, "WS", "test", plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	ready := backend.IssueData{ID: "TASK-4", Title: "Build trigger-bound runner", Status: "open"}
	ib := testIssueBackend{ready: []backend.IssueData{ready}, list: []backend.IssueData{ready}}
	mux := workflowMux(st, ib)
	rec := postJSON(t, mux, "/api/workspaces/WS/workflow-triggers/issue.label_added", map[string]any{
		"input": map[string]any{
			"parentId":       "EPIC-TRIGGER",
			"label":          "epic-runner",
			"type":           "epic",
			"maxConcurrency": 1,
		},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("trigger status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var created triggerResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode trigger response: %v", err)
	}
	if created.Event != "issue.label_added" || len(created.Runs) != 1 {
		t.Fatalf("trigger response = %+v, want one issue.label_added run", created)
	}
	run := created.Runs[0].Run
	if run == nil || run.WorkflowName != "epic-runner" || run.Status != domain.WorkflowRunWaiting {
		t.Fatalf("trigger run = %+v, want waiting epic-runner run", run)
	}
	if created.Runs[0].Builtin == nil || len(created.Runs[0].Builtin.TaskRuns) != 1 {
		t.Fatalf("trigger builtin = %+v, want one ensured task run", created.Runs[0].Builtin)
	}
	events, err := st.RunEvents().List(ctx, "WS", store.RunEventFilter{WorkflowRunID: run.RunID})
	if err != nil {
		t.Fatalf("list run events: %v", err)
	}
	if !hasRunEventPtr(events, "workflow_trigger_admitted") || !hasRunEventPtr(events, "task_run_ensured") {
		t.Fatalf("events = %+v, want trigger admission and ensured task evidence", events)
	}

	streamRec := postJSONStream(t, mux, "/api/workspaces/WS/workflow-triggers/issue.label_added", map[string]any{
		"input": map[string]any{
			"parentId":       "EPIC-TRIGGER-STREAM",
			"label":          "epic-runner",
			"type":           "epic",
			"maxConcurrency": 1,
		},
	})
	if streamRec.Code != http.StatusCreated {
		t.Fatalf("trigger stream status = %d, want 201; body=%s", streamRec.Code, streamRec.Body.String())
	}
	if ct := streamRec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("trigger stream content type = %q, want text/event-stream", ct)
	}
	if body := streamRec.Body.String(); !strings.Contains(body, "event: workflow_trigger_admission") ||
		!strings.Contains(body, "event: workflow_event") ||
		!strings.Contains(body, `"type":"workflow_trigger_admitted"`) ||
		!strings.Contains(body, `"type":"task_run_ensured"`) {
		t.Fatalf("trigger stream body = %s, want trigger admission and replayed workflow events", body)
	}

	terminalMux := workflowMux(st, testIssueBackend{})
	terminalStreamRec := postJSONStream(t, terminalMux, "/api/workspaces/WS/workflow-triggers/issue.label_added?until=terminal", map[string]any{
		"input": map[string]any{
			"parentId":       "EPIC-TRIGGER-DONE",
			"label":          "epic-runner",
			"type":           "epic",
			"maxConcurrency": 1,
		},
	})
	if terminalStreamRec.Code != http.StatusCreated {
		t.Fatalf("terminal trigger stream status = %d, want 201; body=%s", terminalStreamRec.Code, terminalStreamRec.Body.String())
	}
	if body := terminalStreamRec.Body.String(); !strings.Contains(body, "event: workflow_run_stream_complete") ||
		!strings.Contains(body, `"status":"completed"`) ||
		!strings.Contains(body, `"workflow_name":"epic-runner"`) {
		t.Fatalf("terminal trigger stream body = %s, want terminal completion contract", body)
	}
}

func TestSlackCloneTypeScriptFirstWorkflowRunsThroughTrigger(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "Workflow Store"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	root := t.TempDir()
	workflowPath := filepath.Join(root, ".loom", "workflows", "slack-clone-epic.ts")
	if err := os.MkdirAll(filepath.Dir(workflowPath), 0o755); err != nil {
		t.Fatalf("mkdir workflow dir: %v", err)
	}
	if err := os.WriteFile(workflowPath, []byte(`import { defineWorkflow } from '@loom/runtime';

export default defineWorkflow({
  name: 'slack-clone-epic-runner',
  description: 'Runs Slack clone child tasks from a parent epic.',
  path: '/workflows/slack-clone-epic-runner/run',
  auth: 'workspace',
  issueLabelAdded: { label: 'slack-clone', type: 'epic' },
  singleton: (input) => `+"`"+`slack:${input.parentId}`+"`"+`,
  tools: ['workItems.readyChildren', 'taskRuns.ensure'],
  async run(ctx) {
    ctx.log.info('slack clone epic started', { parentId: ctx.input.parentId });
    const ready = await ctx.workItems.readyChildren(String(ctx.input.parentId));
    for (const issue of ready) {
      await ctx.taskRuns.ensure({
        workItemId: issue.id,
        role: 'task',
        reason: issue.title,
        metadata: { app: 'slack-clone', surface: 'channel-sidebar' },
      });
    }
    return { delegated: ready.length };
  },
});
`), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}
	plan, err := defspkg.Load(root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(plan.Workflows) != 1 || plan.Workflows[0].Name != "slack-clone-epic-runner" || plan.Workflows[0].Runner != "workflow-context-v1" {
		t.Fatalf("workflows = %+v, want TypeScript-first Slack clone WorkflowContext runner", plan.Workflows)
	}
	if err := defspkg.Apply(ctx, st, "WS", "test", plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	ready := backend.IssueData{ID: "SLACK-2", Title: "Build channel sidebar", Status: "open"}
	ib := testIssueBackend{ready: []backend.IssueData{ready}, list: []backend.IssueData{ready}}
	mux := workflowMux(st, ib)
	rec := postJSON(t, mux, "/api/workspaces/WS/workflow-triggers/issue.label_added", map[string]any{
		"input": map[string]any{
			"parentId": "SLACK-1",
			"label":    "slack-clone",
			"type":     "epic",
		},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("trigger status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var created triggerResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode trigger response: %v", err)
	}
	if created.Event != "issue.label_added" || len(created.Runs) != 1 {
		t.Fatalf("trigger response = %+v, want one issue.label_added run", created)
	}
	run := created.Runs[0].Run
	if run == nil || run.WorkflowName != "slack-clone-epic-runner" || run.Status != domain.WorkflowRunWaiting {
		t.Fatalf("trigger run = %+v, want waiting TypeScript Slack clone run", run)
	}
	if created.Runs[0].Builtin == nil || len(created.Runs[0].Builtin.TaskRuns) != 1 || created.Runs[0].Builtin.DispatchedCount != 1 {
		t.Fatalf("trigger builtin = %+v, want one ensured and dispatched Slack task run", created.Runs[0].Builtin)
	}
	taskRuns, err := st.TaskRuns().List(ctx, "WS", store.TaskRunFilter{WorkflowRunID: run.RunID, WorkItemID: ready.ID})
	if err != nil {
		t.Fatalf("list task runs: %v", err)
	}
	if len(taskRuns) != 1 || taskRuns[0].Status != domain.TaskRunStarting || taskRuns[0].Metadata["app"] != "slack-clone" || taskRuns[0].Metadata["surface"] != "channel-sidebar" {
		t.Fatalf("taskRuns = %+v, want TypeScript-created Slack clone task run metadata", taskRuns)
	}
	cmds, err := st.AgentCommands().List(ctx, "WS", store.AgentCommandFilter{TargetAgentID: taskRuns[0].AgentID})
	if err != nil {
		t.Fatalf("list agent commands: %v", err)
	}
	if len(cmds) != 1 || cmds[0].Payload["workflow_run_id"] != run.RunID || cmds[0].Payload["task_run_id"] != taskRuns[0].TaskRunID {
		t.Fatalf("commands = %+v, want one agent command linked to Slack workflow task run", cmds)
	}
	events, err := st.RunEvents().List(ctx, "WS", store.RunEventFilter{WorkflowRunID: run.RunID})
	if err != nil {
		t.Fatalf("list run events: %v", err)
	}
	if !hasRunEventPtr(events, "workflow_ts_context_started") || !hasRunEventPtr(events, "workflow_log") || !hasRunEventPtr(events, "task_run_ensured") || !hasRunEventPtr(events, "task_run_dispatched") {
		t.Fatalf("events = %+v, want TypeScript WorkflowContext, log, ensure, and dispatch evidence", events)
	}
}

func TestWorkflowTriggerBindingAPIValidation(t *testing.T) {
	ctx := context.Background()
	_, mux := setupWorkflowTestMux(t, ctx, nil)

	filterMiss := postJSON(t, mux, "/api/workspaces/WS/workflow-triggers/issue.label_added", map[string]any{
		"once":  false,
		"input": map[string]any{"label": "other", "type": "epic", "parentId": "EPIC"},
	})
	if filterMiss.Code != http.StatusNotFound {
		t.Fatalf("filter miss status = %d, want 404; body=%s", filterMiss.Code, filterMiss.Body.String())
	}

	eventMiss := postJSON(t, mux, "/api/workspaces/WS/workflow-triggers/issue.closed", map[string]any{
		"once":  false,
		"input": map[string]any{"parentId": "EPIC"},
	})
	if eventMiss.Code != http.StatusNotFound {
		t.Fatalf("event miss status = %d, want 404; body=%s", eventMiss.Code, eventMiss.Body.String())
	}
}

func TestWorkflowRunAPIValidationAndOnceFalse(t *testing.T) {
	ctx := context.Background()
	st, mux := setupWorkflowTestMux(t, ctx, nil)

	bad := httptest.NewRecorder()
	mux.ServeHTTP(bad, httptest.NewRequest(http.MethodPost, "/api/workspaces/WS/workflows/epic-runner/runs", strings.NewReader(`{"input":`)))
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("bad request status = %d, want 400; body=%s", bad.Code, bad.Body.String())
	}

	runRec := postJSON(t, mux, "/api/workspaces/WS/workflows/epic-runner/runs", map[string]any{
		"once":  false,
		"input": map[string]any{"parentId": "EPIC-1"},
	})
	if runRec.Code != http.StatusCreated {
		t.Fatalf("run once=false status = %d, want 201; body=%s", runRec.Code, runRec.Body.String())
	}
	var created runResponse
	if err := json.Unmarshal(runRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode run: %v", err)
	}
	if created.Run == nil || created.Run.Status != domain.WorkflowRunQueued || created.Builtin != nil {
		t.Fatalf("created = %+v, want queued run without builtin result", created)
	}

	runs, err := st.WorkflowRuns().List(ctx, "WS", store.WorkflowRunFilter{})
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("runs = %d, want 1", len(runs))
	}
}

func TestWorkflowRunAPIDefaultOnceRequiresIssueBackend(t *testing.T) {
	ctx := context.Background()
	_, mux := setupWorkflowTestMux(t, ctx, nil)

	runRec := postJSON(t, mux, "/api/workspaces/WS/workflows/epic-runner/runs", map[string]any{
		"input": map[string]any{"parentId": "EPIC-1"},
	})
	if runRec.Code != http.StatusServiceUnavailable {
		t.Fatalf("run status = %d, want 503; body=%s", runRec.Code, runRec.Body.String())
	}
}

func workflowMux(st store.Store, ib backend.IssueBackend) *http.ServeMux {
	mux := http.NewServeMux()
	NewModule(st).
		WithIssueBackendFn(func(context.Context) backend.IssueBackend { return ib }).
		Register(mux)
	return mux
}

func setupWorkflowTestMux(t *testing.T, ctx context.Context, ib backend.IssueBackend) (store.Store, *http.ServeMux) {
	t.Helper()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "Workflow Store"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	root := t.TempDir()
	if _, err := defspkg.ScaffoldWorkflow(root, "epic-runner"); err != nil {
		t.Fatalf("ScaffoldWorkflow() error = %v", err)
	}
	plan, err := defspkg.Load(root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if err := defspkg.Apply(ctx, st, "WS", "test", plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	return st, workflowMux(st, ib)
}

func postJSON(t *testing.T, mux *http.ServeMux, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	return postJSONWithAccept(t, mux, path, body, "")
}

func postJSONStream(t *testing.T, mux *http.ServeMux, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	return postJSONWithAccept(t, mux, path, body, "text/event-stream")
}

func postJSONWithAccept(t *testing.T, mux *http.ServeMux, path string, body any, accept string) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	req.Header.Set("X-Actor", "tester")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func hasWorkflowDefinition(defs []domain.WorkflowDefinition, name string) bool {
	for _, def := range defs {
		if def.Name == name {
			return true
		}
	}
	return false
}

func hasRunEvent(events []domain.RunEvent, typ string) bool {
	for _, event := range events {
		if event.Type == typ {
			return true
		}
	}
	return false
}

func hasRunEventPtr(events []*domain.RunEvent, typ string) bool {
	for _, event := range events {
		if event != nil && event.Type == typ {
			return true
		}
	}
	return false
}

func hasRouteBinding(routes []domain.RouteBinding, bindingID string) bool {
	for _, route := range routes {
		if route.BindingID == bindingID {
			return true
		}
	}
	return false
}

func hasTriggerBinding(triggers []domain.TriggerBinding, bindingID string) bool {
	for _, trigger := range triggers {
		if trigger.BindingID == bindingID {
			return true
		}
	}
	return false
}

type testIssueBackend struct {
	backend.IssueBackend
	ready   []backend.IssueData
	blocked []backend.IssueData
	list    []backend.IssueData
}

func (b testIssueBackend) Ready(context.Context, backend.ReadyOpts) ([]backend.IssueData, error) {
	return b.ready, nil
}

func (b testIssueBackend) Blocked(context.Context, backend.BlockedOpts) ([]backend.IssueData, error) {
	return b.blocked, nil
}

func (b testIssueBackend) List(context.Context, backend.ListOpts) ([]backend.IssueData, error) {
	return b.list, nil
}

func (b testIssueBackend) Get(_ context.Context, id string) (*backend.IssueDetailData, error) {
	for _, issue := range append(append([]backend.IssueData{}, b.ready...), append(b.blocked, b.list...)...) {
		if issue.ID == id {
			got := backend.IssueDetailData{IssueData: issue}
			return &got, nil
		}
	}
	return nil, errors.New("issue not found")
}

func (b testIssueBackend) BackendName() string {
	return "test"
}

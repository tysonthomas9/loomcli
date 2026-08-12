package workflows

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

const taskWorkflowRunRoute = "internal.task.ready"

func seedTaskWorkflowBinding(t *testing.T, ctx context.Context, st store.Store) {
	t.Helper()
	if _, err := st.TriggerBindings().Create(ctx, store.TriggerBindingCreate{
		WorkspaceKey: "TEST", BindingID: "task-automation", Name: "task automation",
		SourceKind: "internal", RouteKey: taskWorkflowRunRoute,
		DriverID: "demo", DriverVersionID: "version-1", TargetEntrypoint: "run", Enabled: true,
	}); err != nil {
		t.Fatalf("create task workflow binding: %v", err)
	}
}

func dispatchTaskWorkflowRun(t *testing.T, ctx context.Context, st store.Store, taskID, idempotencyKey string, payload json.RawMessage) *domain.DriverRun {
	t.Helper()
	eventID := "event-" + idempotencyKey
	journal, ok := st.TriggerEvents().(store.TriggerEventAppender)
	if !ok {
		t.Fatal("task workflow fixture requires the current event journal port")
	}
	_, err := journal.AppendTriggerEvent(ctx, &automation.Event{
		WorkspaceKey: "TEST", EventID: eventID, SourceEventID: idempotencyKey,
		EventType: "task.ready", SourceKind: automation.SourceKindInternal,
		SubjectRef: "issue:" + taskID, Origin: automation.EventOriginSystem,
		OccurredAt: time.Now().UTC(), ReceivedAt: time.Now().UTC(), Payload: payload,
	})
	if err != nil {
		t.Fatalf("append task workflow event: %v", err)
	}
	run, err := st.DriverRuns().Create(ctx, store.DriverRunCreate{
		WorkspaceKey: "TEST", RunID: "run-" + idempotencyKey,
		DriverID: "demo", DriverVersionID: "version-1", Entrypoint: "run",
		SourceKind: automation.SourceKindInternal, SourceRef: eventID,
		TriggerBindingID: "task-automation", IdempotencyKey: idempotencyKey, Payload: payload,
	})
	if err != nil {
		t.Fatalf("create task workflow run: %v", err)
	}
	return run
}

func finishTaskWorkflowRun(t *testing.T, ctx context.Context, st store.Store, runID string) {
	t.Helper()
	claimed, err := st.DriverRuns().Claim(ctx, "TEST", runID, "node-1", "lease-"+runID)
	if err != nil {
		t.Fatalf("claim task workflow run: %v", err)
	}
	if _, err := st.DriverRuns().Finish(ctx, "TEST", runID, store.DriverRunFinish{
		NodeID: "node-1", LeaseID: "lease-" + runID, FencingToken: claimed.FencingToken,
		Status:  domain.DriverRunCompleted,
		Summary: "Repository selection is required before an agent task can start.",
		Output:  map[string]string{"skipped": "true", "blocker": "repository_required"},
	}); err != nil {
		t.Fatalf("finish task workflow run: %v", err)
	}
}

func seedTaskRunForWorkflow(t *testing.T, ctx context.Context, st store.Store, taskRunID, driverRunID string) {
	t.Helper()
	if _, err := st.TaskRuns().Create(ctx, store.TaskRunCreate{
		WorkspaceKey: "TEST", TaskRunID: taskRunID, DriverRunID: driverRunID,
		TaskID: "TASK-1", Runner: "local", Status: domain.TaskRunQueued,
	}); err != nil {
		t.Fatalf("create task run %s: %v", taskRunID, err)
	}
}

func seedTaskAgentSession(t *testing.T, ctx context.Context, st store.Store, sessionID string, metadata map[string]string) {
	t.Helper()
	if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "TEST", SessionID: sessionID, AgentID: "task-agent-" + sessionID,
		Kind: domain.AgentSessionKindTask, TaskID: "TASK-1", Status: domain.AgentSessionRunning,
		Metadata: metadata,
	}); err != nil {
		t.Fatalf("create task agent session %s: %v", sessionID, err)
	}
}

func listTaskWorkflowRuns(t *testing.T, mux *http.ServeMux, taskID string) (*httptest.ResponseRecorder, taskWorkflowRunsResponse) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/TEST/tasks/"+taskID+"/workflow-runs", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	var response taskWorkflowRunsResponse
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode task workflow runs: %v; body=%s", err, rec.Body.String())
		}
	}
	return rec, response
}

func TestListTaskWorkflowRunsUsesExactSubjectAndExcludesExecutionRepresentedDrivers(t *testing.T) {
	ctx := t.Context()
	st := seededWorkflowStore(t, ctx)
	seedTaskWorkflowBinding(t, ctx, st)

	preclaim := dispatchTaskWorkflowRun(t, ctx, st, "TASK-1", "event-preclaim", json.RawMessage(`{"task_id":"WRONG-PAYLOAD"}`))
	finishTaskWorkflowRun(t, ctx, st, preclaim.RunID)
	// An idempotent event replay must not duplicate the run in the task view.
	dispatchTaskWorkflowRun(t, ctx, st, "TASK-1", "event-preclaim", json.RawMessage(`{"task_id":"WRONG-PAYLOAD"}`))

	// Once Execution has created a TaskRun, the ordinary task history owns that
	// batch attempt and the supplemental workflow-only lane must dedupe it.
	executionRepresented := dispatchTaskWorkflowRun(t, ctx, st, "TASK-1", "event-task-run", json.RawMessage(`{"task_id":"TASK-1"}`))
	seedTaskRunForWorkflow(t, ctx, st, "task-run-1", executionRepresented.RunID)

	// A legacy Interaction shadow must not influence the projection. Without an
	// Execution TaskRun, the DriverRun remains visible here.
	shadowOnly := dispatchTaskWorkflowRun(t, ctx, st, "TASK-1", "event-shadow-only", json.RawMessage(`{"task_id":"TASK-1"}`))
	seedTaskAgentSession(t, ctx, st, "session-shadow-only", map[string]string{
		"driver_run_id": shadowOnly.RunID,
	})

	// A near-prefix subject carrying the requested id only in mutable payload
	// must never leak into TASK-1's run history.
	dispatchTaskWorkflowRun(t, ctx, st, "TASK-10", "event-near-prefix", json.RawMessage(`{"task_id":"TASK-1"}`))

	mux := http.NewServeMux()
	newWorkflowTestModule(st).Register(mux)
	rec, response := listTaskWorkflowRuns(t, mux, "TASK-1")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if response.TaskID != "TASK-1" || response.SubjectRef != "issue:TASK-1" {
		t.Fatalf("response association = %+v, want TASK-1/issue:TASK-1", response)
	}
	gotRunIDs := make(map[string]struct{}, len(response.Runs))
	var gotPreclaim *domain.DriverRun
	for _, run := range response.Runs {
		gotRunIDs[run.RunID] = struct{}{}
		if run.RunID == preclaim.RunID {
			gotPreclaim = run
		}
	}
	if len(gotRunIDs) != 2 {
		t.Fatalf("runs = %+v, want preclaim and shadow-only DriverRuns", response.Runs)
	}
	if _, ok := gotRunIDs[preclaim.RunID]; !ok {
		t.Fatalf("runs = %+v, missing preclaim run %q", response.Runs, preclaim.RunID)
	}
	if _, ok := gotRunIDs[shadowOnly.RunID]; !ok {
		t.Fatalf("runs = %+v, legacy Interaction shadow suppressed DriverRun %q", response.Runs, shadowOnly.RunID)
	}
	if _, leaked := gotRunIDs[executionRepresented.RunID]; leaked {
		t.Fatalf("runs = %+v, Execution TaskRun representation was not deduped", response.Runs)
	}
	if gotPreclaim == nil || gotPreclaim.Summary == "" || gotPreclaim.Output["blocker"] != "repository_required" {
		t.Fatalf("preclaim explanation = run %+v", gotPreclaim)
	}
}

func TestListTaskWorkflowRunsReturnsNonNullEmptyList(t *testing.T) {
	st := seededWorkflowStore(t, t.Context())
	mux := http.NewServeMux()
	newWorkflowTestModule(st).Register(mux)
	rec, response := listTaskWorkflowRuns(t, mux, "TASK-NONE")
	if rec.Code != http.StatusOK || response.Runs == nil || len(response.Runs) != 0 {
		t.Fatalf("empty response = code %d, %+v; want 200 and []", rec.Code, response)
	}
}

func TestListTaskWorkflowRunsFailsClosedWithoutProjection(t *testing.T) {
	mux := http.NewServeMux()
	NewModule(Config{}).Register(mux)
	rec, _ := listTaskWorkflowRuns(t, mux, "TASK-1")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", rec.Code, rec.Body.String())
	}
}

func TestListTaskWorkflowRunsUsesCanonicalWorkspaceFromContext(t *testing.T) {
	ctx := t.Context()
	st := seededWorkflowStore(t, ctx)
	seedTaskWorkflowBinding(t, ctx, st)
	want := dispatchTaskWorkflowRun(t, ctx, st, "TASK-ALIAS", "event-canonical-workspace", nil)

	mux := http.NewServeMux()
	newWorkflowTestModule(st).Register(mux)
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/friendly-alias/tasks/TASK-ALIAS/workflow-runs", nil)
	req = req.WithContext(middleware.WithWorkspaceRef(req.Context(), middleware.WorkspaceRef{
		RequestedID: "friendly-alias",
		CanonicalID: "TEST",
	}))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var response taskWorkflowRunsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode canonical workspace response: %v", err)
	}
	if len(response.Runs) != 1 || response.Runs[0].RunID != want.RunID || response.Runs[0].WorkspaceKey != "TEST" {
		t.Fatalf("canonical workspace runs = %+v, want %q in TEST", response.Runs, want.RunID)
	}
}

func TestListTaskWorkflowRunsRejectsInvalidTaskID(t *testing.T) {
	st := seededWorkflowStore(t, t.Context())
	mux := http.NewServeMux()
	newWorkflowTestModule(st).Register(mux)
	rec, _ := listTaskWorkflowRuns(t, mux, "bad!")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

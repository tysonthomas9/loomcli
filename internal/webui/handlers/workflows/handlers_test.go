package workflows

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	if !hasRunEvent(events.Data, "workflow_ts_reconciled") || !hasRunEvent(events.Data, "task_run_ensured") {
		t.Fatalf("events = %+v, want TypeScript reconcile and task-run evidence", events.Data)
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
	if !hasRunEventPtr(events, "workflow_ts_reconciled") || !hasRunEventPtr(events, "task_run_ensured") {
		t.Fatalf("events = %+v, want TypeScript reconcile and task-run evidence", events)
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
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
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

func (b testIssueBackend) BackendName() string {
	return "test"
}

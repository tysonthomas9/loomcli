package workflows

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
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
	ib := clitest.NewMockIssueBackend()
	ib.ReadyResult = []backend.IssueData{ready}
	ib.ListResult = []backend.IssueData{ready}
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

func workflowMux(st store.Store, ib backend.IssueBackend) *http.ServeMux {
	mux := http.NewServeMux()
	NewModule(st).
		WithIssueBackendFn(func(context.Context) backend.IssueBackend { return ib }).
		Register(mux)
	return mux
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

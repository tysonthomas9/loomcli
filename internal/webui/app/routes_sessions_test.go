package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/app/query/sessionarchive"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	"github.com/tysonthomas9/loomcli/internal/webui"

	"github.com/tysonthomas9/loomcli/internal/webui/handlers/misc"
)

type emptyExecutionSessionQueries struct{}

func (emptyExecutionSessionQueries) GetTaskRun(context.Context, string, string) (*execution.TaskRun, error) {
	return nil, execution.ErrNotFound
}
func (emptyExecutionSessionQueries) ListTaskRuns(context.Context, execution.TaskRunArchiveQuery) ([]*execution.TaskRun, error) {
	return []*execution.TaskRun{}, nil
}
func (emptyExecutionSessionQueries) ListActiveTaskRuns(context.Context, execution.ActiveTaskRunQuery) ([]*execution.TaskRun, error) {
	return nil, nil
}
func (emptyExecutionSessionQueries) ListTaskRunEvents(context.Context, execution.TaskRunEventQuery) ([]*execution.TaskRunEvent, error) {
	return nil, nil
}

type emptyInteractionSessionQueries struct{}

func (emptyInteractionSessionQueries) GetSession(context.Context, string, string) (*interaction.AgentSession, error) {
	return nil, interaction.ErrNotFound
}
func (emptyInteractionSessionQueries) ListSessions(context.Context, interaction.SessionArchiveQuery) ([]*interaction.AgentSession, error) {
	return []*interaction.AgentSession{}, nil
}

func newEmptySessionArchive() sessionarchive.SessionService {
	return sessionarchive.NewSessionService(
		emptyExecutionSessionQueries{}, emptyInteractionSessionQueries{}, nil, nil,
	)
}

// TestSessionRouteMigration_OldFlatRoutesReturn404 verifies that the old flat
// session audit trail routes (e.g. GET /api/tasks/{taskId}/sessions) are no
// longer registered on the main mux and return 404 from the SPA catch-all.
// These routes were removed in loomcli-rocpq; the workspace-scoped equivalents
// at /api/workspaces/{ws}/tasks/{taskId}/sessions/... remain.
func TestSessionRouteMigration_OldFlatRoutesReturn404(t *testing.T) {
	// Create a sessions store so it is non-nil (handlers would return 503
	// if nil, not 404 — we need to distinguish "route not registered" from
	// "handler returns error").
	app := &Server{config: webui.ServerConfig{}, wsResolveFn: testWorkspaceResolver("test-ws")}
	app.sessSvc = newEmptySessionArchive()
	setupTestRoutes(t, app)

	// Old flat routes that should have been removed — each must return 404.
	oldRoutes := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/tasks/loom-abc123/sessions"},
		{http.MethodGet, "/api/tasks/loom-abc123/sessions/some-session-id"},
		{http.MethodGet, "/api/tasks/loom-abc123/sessions/some-session-id/transcript"},
		{http.MethodGet, "/api/tasks/loom-abc123/sessions/some-session-id/diff"},
	}

	for _, tc := range oldRoutes {
		t.Run("old_"+tc.method+"_"+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			rr := httptest.NewRecorder()
			app.mux.ServeHTTP(rr, req)

			if rr.Code != http.StatusNotFound {
				t.Errorf("old flat route %s %s: expected status %d, got %d (body: %s)",
					tc.method, tc.path, http.StatusNotFound, rr.Code, rr.Body.String())
			}

			ct := rr.Header().Get("Content-Type")
			if ct != "application/json" {
				t.Errorf("old flat route %s %s: expected Content-Type 'application/json', got %q",
					tc.method, tc.path, ct)
			}
		})
	}
}

// TestSessionRouteMigration_WorkspaceScopedRoutesRegistered verifies that the
// workspace-scoped session audit trail routes are registered and handled
// (i.e. they do NOT fall through to the SPA catch-all).
func TestSessionRouteMigration_WorkspaceScopedRoutesRegistered(t *testing.T) {
	app := &Server{config: webui.ServerConfig{}, wsResolveFn: testWorkspaceResolver("test-ws")}
	app.sessSvc = newEmptySessionArchive()
	setupTestRoutes(t, app)

	// New workspace-scoped routes that should be registered.
	scopedRoutes := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/workspaces/test-ws/tasks/loom-abc123/sessions"},
		{http.MethodGet, "/api/workspaces/test-ws/tasks/loom-abc123/sessions/some-session-id"},
		{http.MethodGet, "/api/workspaces/test-ws/tasks/loom-abc123/sessions/some-session-id/transcript"},
		{http.MethodGet, "/api/workspaces/test-ws/tasks/loom-abc123/sessions/some-session-id/diff"},
	}

	for _, tc := range scopedRoutes {
		t.Run("scoped_"+tc.method+"_"+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			rr := httptest.NewRecorder()
			app.mux.ServeHTTP(rr, req)

			// The SPA catch-all returns exactly {"error":"not found"} for
			// unregistered /api/* paths. If the route is properly registered,
			// the handler produces a different response (even if it is still
			// a 404 with a more specific error like "session not found").
			var body map[string]interface{}
			if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
				t.Fatalf("workspace-scoped route %s %s: failed to parse JSON body: %v",
					tc.method, tc.path, err)
			}

			errMsg, _ := body["error"].(string)
			if rr.Code == http.StatusNotFound && errMsg == "not found" {
				t.Errorf("workspace-scoped route %s %s fell through to SPA catch-all "+
					"(got generic 404 %q); route is not registered",
					tc.method, tc.path, errMsg)
			}
		})
	}
}

// TestSessionRouteMigration_UnknownWorkspaceReturns404 verifies that the
// workspace-scoped session routes return 404 when the workspace ID is not
// recognized by the WorkspaceMiddleware.
func TestSessionRouteMigration_UnknownWorkspaceReturns404(t *testing.T) {
	app := &Server{config: webui.ServerConfig{}, wsResolveFn: testWorkspaceResolver("test-ws")}
	app.sessSvc = newEmptySessionArchive()
	setupTestRoutes(t, app)

	// Routes with a non-existent workspace should return 404 from WorkspaceMiddleware.
	routes := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/workspaces/nonexistent-ws/tasks/loom-abc123/sessions"},
		{http.MethodGet, "/api/workspaces/nonexistent-ws/tasks/loom-abc123/sessions/some-session-id"},
		{http.MethodGet, "/api/workspaces/nonexistent-ws/tasks/loom-abc123/sessions/some-session-id/transcript"},
		{http.MethodGet, "/api/workspaces/nonexistent-ws/tasks/loom-abc123/sessions/some-session-id/diff"},
	}

	for _, tc := range routes {
		t.Run("unknown_ws_"+tc.method+"_"+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			rr := httptest.NewRecorder()
			app.mux.ServeHTTP(rr, req)

			if rr.Code != http.StatusNotFound {
				t.Errorf("route %s %s with unknown workspace: expected status %d, got %d",
					tc.method, tc.path, http.StatusNotFound, rr.Code)
			}
		})
	}
}

// TestSessionRouteMigration_WorkspaceScopedListReturnsJSON verifies that the
// workspace-scoped list sessions endpoint returns a proper JSON response,
// confirming it is wired to the correct handler (not just registered).
func TestSessionRouteMigration_WorkspaceScopedListReturnsJSON(t *testing.T) {
	app := &Server{config: webui.ServerConfig{}, wsResolveFn: testWorkspaceResolver("test-ws")}
	app.sessSvc = newEmptySessionArchive()
	setupTestRoutes(t, app)

	// GET /api/workspaces/{ws}/tasks/{taskId}/sessions should return 200 with
	// a proper JSON response from the handler (empty sessions list).
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/test-ws/tasks/loom-task42/sessions", nil)
	rr := httptest.NewRecorder()
	app.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("GET /api/workspaces/test-ws/tasks/loom-task42/sessions: expected %d, got %d (body: %s)",
			http.StatusOK, rr.Code, rr.Body.String())
	}

	ct := rr.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("expected Content-Type 'application/json', got %q", ct)
	}

	var resp misc.SessionListResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode JSON response: %v", err)
	}

	if !resp.Success {
		t.Fatalf("success = false, error = %q", resp.Error)
	}
	if resp.Data == nil {
		t.Fatal("data is nil")
	}
	if resp.Data.TaskID != "loom-task42" {
		t.Errorf("task_id = %q, want %q", resp.Data.TaskID, "loom-task42")
	}
	if len(resp.Data.Sessions) != 0 {
		t.Errorf("sessions length = %d, want 0", len(resp.Data.Sessions))
	}
}

// TestSessionRouteMigration_WorkspaceScopedSessionWithData verifies that the
// workspace-scoped session detail endpoint returns data for a real session.

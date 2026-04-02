package webui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
	"github.com/tysonthomas9/loomcli/internal/webui/tabmeta"
)

// TestTerminalStateRouteMigration_OldFlatRoutesReturn404 verifies that the
// old flat terminal state and scrollback/export routes (e.g. GET /api/terminal/state,
// GET /api/terminal/sessions/{session}/scrollback) are no longer registered on
// the main mux and return 404 from the SPA catch-all.
func TestTerminalStateRouteMigration_OldFlatRoutesReturn404(t *testing.T) {
	// Create dependencies needed for route registration.
	multiPool := daemon.NewMultiPool(WorkspaceFromContext, 1)
	_ = multiPool.Register("test-ws", &stubPool{})
	wsExistsFn := func(id string) bool { return multiPool.PoolForWorkspace(id) != nil }

	// Create a tabMetaStore backed by miniredis (needed for state routes).
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })
	tms := tabmeta.NewStore(rdb, nil)

	// Create a termManager (needed for scrollback/export routes).
	// Skip this portion if tmux is unavailable.
	termMgr, err := NewTerminalManager("bash", "", 0)
	if err == ErrTmuxNotFound {
		t.Skip("tmux not installed, skipping test")
	}
	if err != nil {
		t.Fatalf("NewTerminalManager() error: %v", err)
	}
	t.Cleanup(func() { termMgr.Shutdown() })

	app := &Server{multiPool: multiPool, termMgr: termMgr, tabMetaStore: tms, wsExistsFn: wsExistsFn}
	setupTestRoutes(t, app)

	// Old flat routes that should have been removed — each must return 404.
	oldRoutes := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/terminal/sessions/test-session/scrollback"},
		{http.MethodGet, "/api/terminal/sessions/test-session/export"},
		{http.MethodGet, "/api/terminal/sessions/test-session/scrollback-info"},
		{http.MethodGet, "/api/terminal/state"},
		{http.MethodPatch, "/api/terminal/state"},
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

// TestTerminalStateRouteMigration_NewWorkspaceScopedRoutesRegistered verifies
// that the workspace-scoped equivalents of the migrated terminal routes are
// registered and handled (i.e. they do NOT fall through to the SPA catch-all).
func TestTerminalStateRouteMigration_NewWorkspaceScopedRoutesRegistered(t *testing.T) {
	multiPool := daemon.NewMultiPool(WorkspaceFromContext, 1)
	_ = multiPool.Register("test-ws", &stubPool{})
	wsExistsFn := func(id string) bool { return multiPool.PoolForWorkspace(id) != nil }

	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })
	tms := tabmeta.NewStore(rdb, nil)

	termMgr, err := NewTerminalManager("bash", "", 0)
	if err == ErrTmuxNotFound {
		t.Skip("tmux not installed, skipping test")
	}
	if err != nil {
		t.Fatalf("NewTerminalManager() error: %v", err)
	}
	t.Cleanup(func() { termMgr.Shutdown() })

	app := &Server{multiPool: multiPool, termMgr: termMgr, tabMetaStore: tms, wsExistsFn: wsExistsFn}
	setupTestRoutes(t, app)

	// New workspace-scoped routes that should be registered.
	scopedRoutes := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/workspaces/test-ws/terminal/sessions/test-session/scrollback"},
		{http.MethodGet, "/api/workspaces/test-ws/terminal/sessions/test-session/export"},
		{http.MethodGet, "/api/workspaces/test-ws/terminal/sessions/test-session/scrollback-info"},
		{http.MethodGet, "/api/workspaces/test-ws/terminal/state"},
		{http.MethodPatch, "/api/workspaces/test-ws/terminal/state"},
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

// TestTerminalStateRouteMigration_UnknownWorkspaceReturns404 verifies that
// the workspace-scoped routes return 404 when the workspace ID is not recognized
// by the WorkspaceMiddleware.
func TestTerminalStateRouteMigration_UnknownWorkspaceReturns404(t *testing.T) {
	multiPool := daemon.NewMultiPool(WorkspaceFromContext, 1)
	_ = multiPool.Register("test-ws", &stubPool{})
	wsExistsFn := func(id string) bool { return multiPool.PoolForWorkspace(id) != nil }

	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })
	tms := tabmeta.NewStore(rdb, nil)

	termMgr, err := NewTerminalManager("bash", "", 0)
	if err == ErrTmuxNotFound {
		t.Skip("tmux not installed, skipping test")
	}
	if err != nil {
		t.Fatalf("NewTerminalManager() error: %v", err)
	}
	t.Cleanup(func() { termMgr.Shutdown() })

	app := &Server{multiPool: multiPool, termMgr: termMgr, tabMetaStore: tms, wsExistsFn: wsExistsFn}
	setupTestRoutes(t, app)

	// Routes with a non-existent workspace should return 404 from WorkspaceMiddleware.
	routes := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/workspaces/nonexistent-ws/terminal/sessions/test-session/scrollback"},
		{http.MethodGet, "/api/workspaces/nonexistent-ws/terminal/sessions/test-session/export"},
		{http.MethodGet, "/api/workspaces/nonexistent-ws/terminal/sessions/test-session/scrollback-info"},
		{http.MethodGet, "/api/workspaces/nonexistent-ws/terminal/state"},
		{http.MethodPatch, "/api/workspaces/nonexistent-ws/terminal/state"},
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

// TestTerminalStateRouteMigration_StateEndpointsReturnJSON verifies that the
// workspace-scoped terminal state endpoints actually serve JSON responses,
// confirming they are wired to the correct handlers (not just registered).
func TestTerminalStateRouteMigration_StateEndpointsReturnJSON(t *testing.T) {
	multiPool := daemon.NewMultiPool(WorkspaceFromContext, 1)
	_ = multiPool.Register("test-ws", &stubPool{})
	wsExistsFn := func(id string) bool { return multiPool.PoolForWorkspace(id) != nil }

	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })
	tms := tabmeta.NewStore(rdb, nil)

	// termManager is nil here since state routes only need tabMetaStore.
	app := &Server{multiPool: multiPool, tabMetaStore: tms, wsExistsFn: wsExistsFn}
	setupTestRoutes(t, app)

	// GET /api/workspaces/{ws}/terminal/state should return 200 with an
	// active_tab field (empty when no state has been set).
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/test-ws/terminal/state", nil)
	rr := httptest.NewRecorder()
	app.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("GET /api/workspaces/test-ws/terminal/state: expected %d, got %d (body: %s)",
			http.StatusOK, rr.Code, rr.Body.String())
	}

	ct := rr.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("expected Content-Type 'application/json', got %q", ct)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode JSON response: %v", err)
	}

	// The handler returns {"active_tab": ""} for empty state.
	if _, ok := resp["active_tab"]; !ok {
		t.Error("expected 'active_tab' field in response")
	}
}

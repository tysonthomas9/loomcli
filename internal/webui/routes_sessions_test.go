package webui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
)

// TestSessionRouteMigration_OldFlatRoutesReturn404 verifies that the old flat
// session audit trail routes (e.g. GET /api/tasks/{taskId}/sessions) are no
// longer registered on the main mux and return 404 from the SPA catch-all.
// These routes were removed in loomcli-rocpq; the workspace-scoped equivalents
// at /api/workspaces/{ws}/tasks/{taskId}/sessions/... remain.
func TestSessionRouteMigration_OldFlatRoutesReturn404(t *testing.T) {
	// Create a sessions store so it is non-nil (handlers would return 503
	// if nil, not 404 — we need to distinguish "route not registered" from
	// "handler returns error").
	sessStore := newTestSessionStore(t)

	multiPool := daemon.NewMultiPool(WorkspaceFromContext, 1)
	_ = multiPool.Register("test-ws", &stubPool{})
	wsExistsFn := func(id string) bool { return multiPool.PoolForWorkspace(id) != nil }

	mux := http.NewServeMux()
	//                    mux  pool multiPool hub  getMut termMgr termAuth fleetReg tokenCfg apiKey authOn origins fleetRegCfg claimMetrics fleetOn devMode devDir loomURL gitOps fileOps tabMeta issueTabs wsCfg  wsDel  setDef clrDef wsCreate bkOps sessHist sessSt   wsExists   initWS  wsCfgByID extAuth
	setupRoutes(mux, nil, multiPool, nil, nil, nil, nil, nil, nil, nil, nil, nil, false, "", "", nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, sessStore, wsExistsFn, nil, "", nil, nil, "")

	// Old flat routes that should have been removed — each must return 404.
	oldRoutes := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/tasks/bd-abc123/sessions"},
		{http.MethodGet, "/api/tasks/bd-abc123/sessions/some-session-id"},
		{http.MethodGet, "/api/tasks/bd-abc123/sessions/some-session-id/transcript"},
		{http.MethodGet, "/api/tasks/bd-abc123/sessions/some-session-id/diff"},
	}

	for _, tc := range oldRoutes {
		t.Run("old_"+tc.method+"_"+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, req)

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
	sessStore := newTestSessionStore(t)

	multiPool := daemon.NewMultiPool(WorkspaceFromContext, 1)
	_ = multiPool.Register("test-ws", &stubPool{})
	wsExistsFn := func(id string) bool { return multiPool.PoolForWorkspace(id) != nil }

	mux := http.NewServeMux()
	setupRoutes(mux, nil, multiPool, nil, nil, nil, nil, nil, nil, nil, nil, nil, false, "", "", nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, sessStore, wsExistsFn, nil, "", nil, nil, "")

	// New workspace-scoped routes that should be registered.
	scopedRoutes := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/workspaces/test-ws/tasks/bd-abc123/sessions"},
		{http.MethodGet, "/api/workspaces/test-ws/tasks/bd-abc123/sessions/some-session-id"},
		{http.MethodGet, "/api/workspaces/test-ws/tasks/bd-abc123/sessions/some-session-id/transcript"},
		{http.MethodGet, "/api/workspaces/test-ws/tasks/bd-abc123/sessions/some-session-id/diff"},
	}

	for _, tc := range scopedRoutes {
		t.Run("scoped_"+tc.method+"_"+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, req)

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
	sessStore := newTestSessionStore(t)

	multiPool := daemon.NewMultiPool(WorkspaceFromContext, 1)
	_ = multiPool.Register("test-ws", &stubPool{})
	wsExistsFn := func(id string) bool { return multiPool.PoolForWorkspace(id) != nil }

	mux := http.NewServeMux()
	setupRoutes(mux, nil, multiPool, nil, nil, nil, nil, nil, nil, nil, nil, nil, false, "", "", nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, sessStore, wsExistsFn, nil, "", nil, nil, "")

	// Routes with a non-existent workspace should return 404 from WorkspaceMiddleware.
	routes := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/workspaces/nonexistent-ws/tasks/bd-abc123/sessions"},
		{http.MethodGet, "/api/workspaces/nonexistent-ws/tasks/bd-abc123/sessions/some-session-id"},
		{http.MethodGet, "/api/workspaces/nonexistent-ws/tasks/bd-abc123/sessions/some-session-id/transcript"},
		{http.MethodGet, "/api/workspaces/nonexistent-ws/tasks/bd-abc123/sessions/some-session-id/diff"},
	}

	for _, tc := range routes {
		t.Run("unknown_ws_"+tc.method+"_"+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, req)

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
	sessStore := newTestSessionStore(t)

	multiPool := daemon.NewMultiPool(WorkspaceFromContext, 1)
	_ = multiPool.Register("test-ws", &stubPool{})
	wsExistsFn := func(id string) bool { return multiPool.PoolForWorkspace(id) != nil }

	mux := http.NewServeMux()
	setupRoutes(mux, nil, multiPool, nil, nil, nil, nil, nil, nil, nil, nil, nil, false, "", "", nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, sessStore, wsExistsFn, nil, "", nil, nil, "")

	// GET /api/workspaces/{ws}/tasks/{taskId}/sessions should return 200 with
	// a proper JSON response from the handler (empty sessions list).
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/test-ws/tasks/bd-task42/sessions", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("GET /api/workspaces/test-ws/tasks/bd-task42/sessions: expected %d, got %d (body: %s)",
			http.StatusOK, rr.Code, rr.Body.String())
	}

	ct := rr.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("expected Content-Type 'application/json', got %q", ct)
	}

	var resp SessionListResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode JSON response: %v", err)
	}

	if !resp.Success {
		t.Fatalf("success = false, error = %q", resp.Error)
	}
	if resp.Data == nil {
		t.Fatal("data is nil")
	}
	if resp.Data.TaskID != "bd-task42" {
		t.Errorf("task_id = %q, want %q", resp.Data.TaskID, "bd-task42")
	}
	if len(resp.Data.Sessions) != 0 {
		t.Errorf("sessions length = %d, want 0", len(resp.Data.Sessions))
	}
}

// TestSessionRouteMigration_WorkspaceScopedSessionWithData verifies that the
// workspace-scoped session detail endpoint returns data for a real session.
func TestSessionRouteMigration_WorkspaceScopedSessionWithData(t *testing.T) {
	sessStore := newTestSessionStore(t)
	sess := createTestSession(t, sessStore, "bd-routed")

	multiPool := daemon.NewMultiPool(WorkspaceFromContext, 1)
	_ = multiPool.Register("test-ws", &stubPool{})
	wsExistsFn := func(id string) bool { return multiPool.PoolForWorkspace(id) != nil }

	mux := http.NewServeMux()
	setupRoutes(mux, nil, multiPool, nil, nil, nil, nil, nil, nil, nil, nil, nil, false, "", "", nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, sessStore, wsExistsFn, nil, "", nil, nil, "")

	// GET session detail via workspace-scoped route.
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/test-ws/tasks/bd-routed/sessions/"+sess.SessionID(), nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected %d, got %d (body: %s)", http.StatusOK, rr.Code, rr.Body.String())
	}

	var resp SessionDetailResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Success {
		t.Fatalf("success = false, error = %q", resp.Error)
	}
	if resp.Data == nil {
		t.Fatal("data is nil")
	}
	if resp.Data.SessionID != sess.SessionID() {
		t.Errorf("session_id = %q, want %q", resp.Data.SessionID, sess.SessionID())
	}
}

// TestSessionRouteMigration_WorkspaceScopedDiffEndpoint verifies that the
// workspace-scoped diff endpoint is wired through the mux correctly.
func TestSessionRouteMigration_WorkspaceScopedDiffEndpoint(t *testing.T) {
	sessStore := newTestSessionStore(t)
	sess := createTestSession(t, sessStore, "bd-diffrouted")

	multiPool := daemon.NewMultiPool(WorkspaceFromContext, 1)
	_ = multiPool.Register("test-ws", &stubPool{})
	wsExistsFn := func(id string) bool { return multiPool.PoolForWorkspace(id) != nil }

	mux := http.NewServeMux()
	setupRoutes(mux, nil, multiPool, nil, nil, nil, nil, nil, nil, nil, nil, nil, false, "", "", nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, sessStore, wsExistsFn, nil, "", nil, nil, "")

	// GET diff via workspace-scoped route — createTestSession includes a DiffPatch.
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/test-ws/tasks/bd-diffrouted/sessions/"+sess.SessionID()+"/diff", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected %d, got %d (body: %s)", http.StatusOK, rr.Code, rr.Body.String())
	}

	ct := rr.Header().Get("Content-Type")
	if ct != "text/plain" {
		t.Errorf("Content-Type = %q, want %q", ct, "text/plain")
	}

	body := rr.Body.String()
	if body == "" {
		t.Error("body is empty, expected diff content")
	}
}

// TestSessionRouteMigration_WorkspaceScopedTranscriptEndpoint verifies that
// the workspace-scoped transcript endpoint is wired through the mux correctly.
func TestSessionRouteMigration_WorkspaceScopedTranscriptEndpoint(t *testing.T) {
	sessStore := newTestSessionStore(t)
	sess := createTestSession(t, sessStore, "bd-transrouted")

	// Append a transcript entry.
	err := sessStore.AppendTranscript(sess.SessionID(), sessions.TranscriptEntry{
		Seq:     1,
		Role:    "user",
		Type:    "text",
		Content: "Hello via workspace route",
	})
	if err != nil {
		t.Fatalf("AppendTranscript: %v", err)
	}

	multiPool := daemon.NewMultiPool(WorkspaceFromContext, 1)
	_ = multiPool.Register("test-ws", &stubPool{})
	wsExistsFn := func(id string) bool { return multiPool.PoolForWorkspace(id) != nil }

	mux := http.NewServeMux()
	setupRoutes(mux, nil, multiPool, nil, nil, nil, nil, nil, nil, nil, nil, nil, false, "", "", nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, sessStore, wsExistsFn, nil, "", nil, nil, "")

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/test-ws/tasks/bd-transrouted/sessions/"+sess.SessionID()+"/transcript", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected %d, got %d (body: %s)", http.StatusOK, rr.Code, rr.Body.String())
	}

	var resp TranscriptResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Success {
		t.Fatalf("success = false, error = %q", resp.Error)
	}
	if resp.Data == nil {
		t.Fatal("data is nil")
	}
	if len(resp.Data.Entries) != 1 {
		t.Fatalf("entries length = %d, want 1", len(resp.Data.Entries))
	}
	if resp.Data.Entries[0].Content != "Hello via workspace route" {
		t.Errorf("content = %q, want %q", resp.Data.Entries[0].Content, "Hello via workspace route")
	}
}

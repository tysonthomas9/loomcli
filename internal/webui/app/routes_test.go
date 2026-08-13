package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
	"github.com/tysonthomas9/loomcli/internal/webui"
	"github.com/tysonthomas9/loomcli/internal/webui/agentcoord"
	"github.com/tysonthomas9/loomcli/internal/webui/sessioncoord"
	"github.com/tysonthomas9/loomcli/internal/webui/workspacecoord"

	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/store"
	healthhandlers "github.com/tysonthomas9/loomcli/internal/webui/handlers/health"
	hterminal "github.com/tysonthomas9/loomcli/internal/webui/handlers/terminal"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	"github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

// setupTestRoutes constructs handlers and registers routes on app.mux.
// Cleans up rate limiter goroutines when the test finishes.
func setupTestRoutes(t *testing.T, app *Server) {
	t.Helper()
	app.mux = http.NewServeMux()
	app.buildHandlers()
	app.buildModules()
	app.registerRoutes()
	t.Cleanup(func() {
		if app.handlers != nil {
			if app.handlers.ClientErrLimiter != nil {
				app.handlers.ClientErrLimiter.Stop()
			}
			if app.handlers.AuthCfgLimiter != nil {
				app.handlers.AuthCfgLimiter.Stop()
			}
		}
	})
}

// TestStatsResponse_SuccessSerialization tests successful healthhandlers.StatsResponse serialization.
func TestStatsResponse_SuccessSerialization(t *testing.T) {
	stats := &workitems.Stats{
		TotalIssues:      100,
		OpenIssues:       50,
		InProgressIssues: 20,
		ClosedIssues:     30,
		BlockedIssues:    5,
		DeferredIssues:   10,
		ReadyIssues:      15,
		TombstoneIssues:  2,
		PinnedIssues:     3,
		AverageLeadTime:  24.5,
	}

	resp := healthhandlers.StatsResponse{
		Success: true,
		Data:    stats,
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("failed to marshal response: %v", err)
	}

	var parsed healthhandlers.StatsResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if !parsed.Success {
		t.Error("expected Success to be true")
	}

	if parsed.Data == nil {
		t.Fatal("expected Data to be non-nil")
	}

	if parsed.Data.TotalIssues != 100 {
		t.Errorf("expected TotalIssues 100, got %d", parsed.Data.TotalIssues)
	}

	if parsed.Data.OpenIssues != 50 {
		t.Errorf("expected OpenIssues 50, got %d", parsed.Data.OpenIssues)
	}

	if parsed.Data.AverageLeadTime != 24.5 {
		t.Errorf("expected AverageLeadTime 24.5, got %f", parsed.Data.AverageLeadTime)
	}
}

// TestStatsResponse_ErrorSerialization tests error healthhandlers.StatsResponse serialization.
func TestStatsResponse_ErrorSerialization(t *testing.T) {
	resp := healthhandlers.StatsResponse{
		Success: false,
		Error:   "connection failed",
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("failed to marshal response: %v", err)
	}

	var parsed healthhandlers.StatsResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if parsed.Success {
		t.Error("expected Success to be false")
	}

	if parsed.Error != "connection failed" {
		t.Errorf("expected Error 'connection failed', got %q", parsed.Error)
	}

	if parsed.Data != nil {
		t.Error("expected Data to be nil")
	}
}

// TestStatsResponse_ErrorOmitsDataField verifies that error responses omit the data field.
func TestStatsResponse_ErrorOmitsDataField(t *testing.T) {
	resp := healthhandlers.StatsResponse{
		Success: false,
		Error:   "some error",
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("failed to marshal response: %v", err)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("failed to unmarshal to map: %v", err)
	}

	if _, hasData := raw["data"]; hasData {
		t.Error("expected 'data' field to be omitted in error response")
	}
}

// TestStatsResponse_SuccessOmitsErrorField verifies that success responses omit the error field.
func TestStatsResponse_SuccessOmitsErrorField(t *testing.T) {
	resp := healthhandlers.StatsResponse{
		Success: true,
		Data:    &workitems.Stats{TotalIssues: 10},
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("failed to marshal response: %v", err)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("failed to unmarshal to map: %v", err)
	}

	if _, hasError := raw["error"]; hasError {
		t.Error("expected 'error' field to be omitted in success response")
	}
}

// TestSetupRoutes_FlatTerminalWSEndpointReturns404 tests that
// the flat terminal WebSocket endpoint returns 404 (removed in favor of workspace-scoped).
func TestSetupRoutes_FlatTerminalWSEndpointReturns404(t *testing.T) {
	app := &Server{}
	setupTestRoutes(t, app)

	req := httptest.NewRequest(http.MethodGet, "/api/terminal/ws?session=test", nil)
	rr := httptest.NewRecorder()
	app.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected flat /api/terminal/ws to return 404, got %d", rr.Code)
	}

	ct := rr.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}
}

// TestSetupRoutes_FlatTerminalRoutesReturn404 verifies that flat terminal routes
// return 404 even when termManager is non-nil (they have been removed in favor
// of workspace-scoped equivalents).
func TestSetupRoutes_FlatTerminalRoutesReturn404(t *testing.T) {
	ptyMgr := terminal.NewMultiPTYManager("bash", 0)
	defer ptyMgr.Close()

	app := &Server{ptyMgr: ptyMgr}
	setupTestRoutes(t, app)

	flatRoutes := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/terminal/sessions"},
		{http.MethodGet, "/api/terminal/ws?session=test"},
		{http.MethodPost, "/api/terminal/restart?session=test"},
		{http.MethodPost, "/api/terminal/kill?session=test"},
		{http.MethodGet, "/api/terminal/session-status?session=test"},
		{http.MethodPost, "/api/terminal/spawn"},
		{http.MethodPost, "/api/terminal/sessions/test/seed"},
		{http.MethodPost, "/api/terminal/sessions/test/kill"},
		{http.MethodPost, "/api/terminal/sessions/close-all"},
	}

	for _, tc := range flatRoutes {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			rr := httptest.NewRecorder()
			app.mux.ServeHTTP(rr, req)

			if rr.Code != http.StatusNotFound {
				t.Errorf("flat route %s %s: expected 404, got %d", tc.method, tc.path, rr.Code)
			}
		})
	}
}

// TestSetupRoutes_TerminalEndpointNilManagerReturns503 tests that
// calling handleTerminalWS directly with nil manager returns 503.
// This complements the route registration test by verifying handler behavior.
func TestSetupRoutes_TerminalEndpointNilManagerReturns503(t *testing.T) {
	handler := hterminal.HandleTerminalWS(nil, nil, nil, "", nil, nil, nil, time.Time{})

	req := httptest.NewRequest(http.MethodGet, "/api/terminal/ws?session=test", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status %d for nil manager, got %d", http.StatusServiceUnavailable, rr.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp["success"] != false {
		t.Error("expected success to be false")
	}

	if resp["error"] != "terminal manager not initialized" {
		t.Errorf("expected error 'terminal manager not initialized', got %q", resp["error"])
	}
}

// TestSetupRoutes_StatsEndpointDeleted verifies the unscoped /api/stats route
// is no longer registered — it had no FE caller and 503'd in fleet mode.
// The workspace-scoped /api/workspaces/{ws}/stats remains the canonical path.
func TestSetupRoutes_StatsEndpointDeleted(t *testing.T) {
	app := &Server{}
	setupTestRoutes(t, app)

	for _, method := range []string{http.MethodGet, http.MethodPost} {
		req := httptest.NewRequest(method, "/api/stats", nil)
		rr := httptest.NewRecorder()
		app.mux.ServeHTTP(rr, req)
		if rr.Code != http.StatusNotFound {
			t.Errorf("%s /api/stats: expected 404, got %d", method, rr.Code)
		}
		if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
			t.Errorf("%s /api/stats: Content-Type = %q, want %q", method, ct, "application/json")
		}
	}
}

// TestSetupRoutes_FleetEndpointsNotRegisteredWhenDisabled tests that
// fleet endpoints are NOT registered when fleetEnabled is false.
func TestSetupRoutes_FleetEndpointsNotRegisteredWhenDisabled(t *testing.T) {
	app := &Server{}
	setupTestRoutes(t, app) // fleetEnabled=false

	// Request to fleet endpoint should return 404 JSON since the route is not
	// registered and the SPA catch-all rejects /api/* paths
	req := httptest.NewRequest(http.MethodPost, "/api/fleet/claim", nil)
	rr := httptest.NewRecorder()
	app.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected /api/fleet/claim to return 404 when fleetEnabled is false, got %d", rr.Code)
	}

	ct := rr.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}
}

// TestSetupRoutes_FlatFleetRoutesReturn404 verifies that flat fleet routes
// return 404 even when fleet is enabled (they have been removed in favor of
// workspace-scoped equivalents).
func TestSetupRoutes_FlatFleetRoutesReturn404(t *testing.T) {
	app := &Server{}
	setupTestRoutes(t, app) // fleetEnabled=true

	req := httptest.NewRequest(http.MethodPost, "/api/fleet/claim", nil)
	rr := httptest.NewRecorder()
	app.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected flat /api/fleet/claim to return 404, got %d", rr.Code)
	}

	ct := rr.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}
}

// --- Mock server infrastructure for routes tests ---

// --- SSE route conditional registration tests ---

// TestSetupRoutes_RemovedSSEEndpointReturns404 verifies that GET /api/events
// The flat endpoint returns 404 now that SSE is workspace-scoped.
func TestSetupRoutes_RemovedSSEEndpointReturns404(t *testing.T) {
	app := &Server{}
	setupTestRoutes(t, app)

	req := httptest.NewRequest(http.MethodGet, "/api/events", nil)
	rr := httptest.NewRecorder()
	app.mux.ServeHTTP(rr, req)

	// Removed SSE endpoint; SPA catch-all rejects /api/* with 404 JSON.
	if rr.Code != http.StatusNotFound {
		t.Errorf("expected /api/events to return 404, got %d", rr.Code)
	}

	ct := rr.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}
}

// TestSetupRoutes_SSEEndpointRegisteredOnWorkspaceScope verifies that
// GET /api/workspaces/{ws}/events is handled by the SSE handler when
// the SSE hub is available.
func TestSetupRoutes_SSEEndpointRegisteredOnWorkspaceScope(t *testing.T) {
	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()

	app := &Server{hub: hub, wsResolveFn: testWorkspaceResolver("test-ws")}
	app.sessSvc = sessioncoord.NewSessionService(nil, nil, nil)
	setupTestRoutes(t, app)

	// Use a context with short timeout because the SSE handler streams forever
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/test-ws/events", nil).WithContext(ctx)
	rr := httptest.NewRecorder()
	app.mux.ServeHTTP(rr, req)

	// With a non-nil hub, the workspace-scoped SSE route is registered.
	// The SSE handler sets Content-Type to text/event-stream.
	ct := rr.Header().Get("Content-Type")
	if ct == "text/html; charset=utf-8" {
		t.Error("expected SSE route to be registered, but request fell through to frontend handler")
	}
}

func TestSetupRoutes_SSEEndpointUsesCanonicalWorkspace(t *testing.T) {
	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()

	app := &Server{
		hub: hub,
		wsResolveFn: func(_ context.Context, requestedID string) (middleware.WorkspaceRef, bool) {
			if requestedID != "alias-ws" {
				t.Fatalf("requested workspace = %q, want alias-ws", requestedID)
			}
			return middleware.WorkspaceRef{RequestedID: requestedID, CanonicalID: "canonical-ws"}, true
		},
	}
	app.sessSvc = sessioncoord.NewSessionService(nil, nil, nil)
	setupTestRoutes(t, app)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/alias-ws/events", nil).WithContext(ctx)
	rr := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		app.mux.ServeHTTP(rr, req)
		close(done)
	}()

	waitForWorkspaceClientCount(t, hub, "canonical-ws", 1)
	if got := hub.ClientCountForWorkspace("alias-ws"); got != 0 {
		t.Fatalf("alias workspace client count = %d, want 0", got)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("SSE handler did not exit after request context cancellation")
	}
}

func TestSetupRoutes_WorkspaceMonitorStatusInjectsWorkspace(t *testing.T) {
	app := &Server{
		wsResolveFn: testWorkspaceResolver("test-ws"),
		config: webui.ServerConfig{
			MonitorHandlers: webui.MonitorHandlers{
				Status: func(w http.ResponseWriter, r *http.Request) {
					if got := middleware.WorkspaceFromContext(r.Context()); got != "test-ws" {
						t.Errorf("workspace context = %q, want test-ws", got)
					}
					if got := r.URL.Query().Get("workspace"); got != "test-ws" {
						t.Errorf("workspace query = %q, want test-ws", got)
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"ok":true}`))
				},
			},
		},
	}
	app.sessSvc = sessioncoord.NewSessionService(nil, nil, nil)
	setupTestRoutes(t, app)

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/test-ws/monitor/status", nil)
	rr := httptest.NewRecorder()
	app.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
}

func TestSetupRoutes_WorkspaceGetUsesCanonicalWorkspace(t *testing.T) {
	st := memstore.New()
	if _, err := st.Workspaces().Create(context.Background(), store.WorkspaceCreate{Key: "canonical-ws", Name: "Canonical"}); err != nil {
		t.Fatal(err)
	}
	catalog, err := NewWorkspaceCapability(st.Workspaces(), st.Repos())
	if err != nil {
		t.Fatal(err)
	}
	wsSvc := workspacecoord.NewWorkspaceService(workspacecoord.WorkspaceServiceConfig{Topology: st, Workspace: catalog})
	app := &Server{
		config:           webui.ServerConfig{Store: st},
		workspaceSvc:     wsSvc,
		workspaceCatalog: catalog,
		workspaceStore:   st.Workspaces(),
		wsResolveFn: func(_ context.Context, requestedID string) (middleware.WorkspaceRef, bool) {
			if requestedID != "alias-ws" {
				t.Fatalf("requested workspace = %q, want alias-ws", requestedID)
			}
			return middleware.WorkspaceRef{RequestedID: requestedID, CanonicalID: "canonical-ws"}, true
		},
	}
	app.sessSvc = sessioncoord.NewSessionService(nil, nil, nil)
	setupTestRoutes(t, app)

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/alias-ws", nil)
	rr := httptest.NewRecorder()
	app.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", rr.Code, rr.Body.String())
	}
}

func waitForWorkspaceClientCount(t *testing.T, hub *realtime.Hub, wsID string, expected int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if hub.ClientCountForWorkspace(wsID) == expected {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("workspace %q client count = %d, want %d", wsID, hub.ClientCountForWorkspace(wsID), expected)
}

func TestSetupRoutes_WorkspaceBackendGetEndpoint(t *testing.T) {
	wsSvc := &mockWorkspaceService{
		getWorkspaceBackendFn: func(_ context.Context, wsID string) (*workspacecoord.BackendConfigData, error) {
			if wsID != "test-ws" {
				t.Errorf("workspace id = %q, want test-ws", wsID)
			}
			return &workspacecoord.BackendConfigData{
				Backend:   "codex",
				Source:    "workspace",
				Available: []string{"claude", "codex"},
			}, nil
		},
	}
	app := &Server{config: webui.ServerConfig{}, wsResolveFn: testWorkspaceResolver("test-ws"), workspaceSvc: wsSvc}
	app.sessSvc = sessioncoord.NewSessionService(nil, nil, nil)
	setupTestRoutes(t, app)

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/test-ws/config/backend", nil)
	rr := httptest.NewRecorder()
	app.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("Content-Type") == "text/html; charset=utf-8" {
		t.Fatal("expected workspace backend GET route to be registered, but request fell through to frontend handler")
	}

	var body map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to parse response JSON: %v", err)
	}
	if success, _ := body["success"].(bool); !success {
		t.Fatalf("response success = false, want true; body=%v", body)
	}
	data, ok := body["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("response missing data object: %v", body)
	}
	if backend, _ := data["backend"].(string); backend != "codex" {
		t.Errorf("data.backend = %q, want codex", backend)
	}
}

// TestSetupRoutes_WorkspaceBackendPatchEndpoint verifies that
// PATCH /api/workspaces/{ws}/config/backend is handled by handleWorkspaceBackendPatch
// (which returns workspaceResponse shape) rather than handlePatchBackendConfig.
func TestSetupRoutes_WorkspaceBackendPatchEndpoint(t *testing.T) {
	wsSvc := &mockWorkspaceService{
		patchWorkspaceBackendFn: func(_ context.Context, _ string, _ string) (*ops.WorkspaceData, error) {
			return &ops.WorkspaceData{Name: "test-ws", Path: "/tmp/test"}, nil
		},
	}
	app := &Server{config: webui.ServerConfig{}, wsResolveFn: testWorkspaceResolver("test-ws"), workspaceSvc: wsSvc}
	app.sessSvc = sessioncoord.NewSessionService(nil, nil, nil)
	setupTestRoutes(t, app)

	req := httptest.NewRequest(http.MethodPatch, "/api/workspaces/test-ws/config/backend",
		strings.NewReader(`{"backend":"codex"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	app.mux.ServeHTTP(rr, req)

	// The route must be registered (not fall through to SPA catch-all)
	ct := rr.Header().Get("Content-Type")
	if ct == "text/html; charset=utf-8" {
		t.Fatal("expected workspace backend PATCH route to be registered, but request fell through to frontend handler")
	}

	// Response must be JSON with workspaceResponse shape (has "success" field)
	var body map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to parse response JSON: %v", err)
	}

	if _, ok := body["success"]; !ok {
		t.Error("response missing 'success' field — expected workspaceResponse shape")
	}

	// Verify the handler returned data with WorkspaceData shape (has "name" field)
	// which only handleWorkspaceBackendPatch provides, not handlePatchBackendConfig
	if rr.Code == http.StatusOK {
		data, ok := body["data"].(map[string]interface{})
		if !ok {
			t.Error("expected 'data' field in successful response")
		} else if _, hasName := data["name"]; !hasName {
			t.Error("expected 'name' field in data — handleWorkspaceBackendPatch returns WorkspaceData")
		}
	}
}

// TestSetupRoutes_WorkspaceRenamePatchEndpoint verifies that
// PATCH /api/workspaces/{ws}/name is registered on the outer mux and that the
// request body reaches the handler. The latter is a regression guard: these
// PATCH routes are deliberately registered on the outer mux (not via the
// nested wsMux subtree) because Go 1.22+ http.ServeMux has a bug where
// r.Body.Read() hangs for PATCH requests routed through a nested mux via
// wildcard subtree pattern. If someone moves these routes back into wsMux,
// body decoding would break and this test would catch it.
func TestSetupRoutes_WorkspaceRenamePatchEndpoint(t *testing.T) {
	st := memstore.New()
	if _, err := st.Workspaces().Create(context.Background(), store.WorkspaceCreate{Key: "test-ws", Name: "test-ws"}); err != nil {
		t.Fatal(err)
	}
	catalog, err := NewWorkspaceCapability(st.Workspaces(), st.Repos())
	if err != nil {
		t.Fatal(err)
	}
	wsSvc := workspacecoord.NewWorkspaceService(workspacecoord.WorkspaceServiceConfig{Topology: st, Workspace: catalog})
	app := &Server{
		config:           webui.ServerConfig{Store: st},
		wsResolveFn:      testWorkspaceResolver("test-ws"),
		workspaceSvc:     wsSvc,
		workspaceCatalog: catalog,
		workspaceStore:   st.Workspaces(),
	}
	app.sessSvc = sessioncoord.NewSessionService(nil, nil, nil)
	setupTestRoutes(t, app)

	req := httptest.NewRequest(http.MethodPatch, "/api/workspaces/test-ws/name",
		strings.NewReader(`{"new_name":"renamed-ws"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	app.mux.ServeHTTP(rr, req)

	// The route must be registered (not fall through to SPA catch-all)
	ct := rr.Header().Get("Content-Type")
	if ct == "text/html; charset=utf-8" {
		t.Fatal("expected workspace rename PATCH route to be registered, but request fell through to frontend handler")
	}

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", rr.Code, rr.Body.String())
	}

	updated, err := st.Workspaces().Get(context.Background(), "test-ws")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != "renamed-ws" {
		t.Errorf("handler did not persist new_name; got %q, want %q", updated.Name, "renamed-ws")
	}

	var body map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to parse response JSON: %v", err)
	}
	if success, _ := body["success"].(bool); !success {
		t.Errorf("response success = false, want true; body=%v", body)
	}
	if data, ok := body["data"].(map[string]interface{}); !ok {
		t.Error("expected 'data' field in successful response")
	} else if name, _ := data["name"].(string); name != "renamed-ws" {
		t.Errorf("data.name = %q, want %q", name, "renamed-ws")
	}
}

// TestSetupRoutes_WorkspaceBackendPatchReadsBody is a regression guard that
// verifies the PATCH /config/backend route not only registers but actually
// receives the request body at the handler. Complements
// TestSetupRoutes_WorkspaceBackendPatchEndpoint which only asserts the shape.
func TestSetupRoutes_WorkspaceBackendPatchReadsBody(t *testing.T) {
	var capturedBackend string
	wsSvc := &mockWorkspaceService{
		patchWorkspaceBackendFn: func(_ context.Context, _ string, backend string) (*ops.WorkspaceData, error) {
			capturedBackend = backend
			return &ops.WorkspaceData{Name: "test-ws", Path: "/tmp/test"}, nil
		},
	}
	app := &Server{config: webui.ServerConfig{}, wsResolveFn: testWorkspaceResolver("test-ws"), workspaceSvc: wsSvc}
	app.sessSvc = sessioncoord.NewSessionService(nil, nil, nil)
	setupTestRoutes(t, app)

	req := httptest.NewRequest(http.MethodPatch, "/api/workspaces/test-ws/config/backend",
		strings.NewReader(`{"backend":"codex"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	app.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", rr.Code, rr.Body.String())
	}

	if capturedBackend != "codex" {
		t.Errorf("handler did not receive backend from body; got %q, want %q", capturedBackend, "codex")
	}
}

// --- Terminal token route conditional registration tests ---

// TestSetupRoutes_FlatTerminalTokenReturns404 verifies that GET /api/terminal/token
// returns 404 (flat route removed, only workspace-scoped route exists).
func TestSetupRoutes_FlatTerminalTokenReturns404(t *testing.T) {
	ptyMgr := terminal.NewMultiPTYManager("bash", 0)
	t.Cleanup(func() { ptyMgr.Close() })

	termAuth, err := realtime.NewTerminalAuth()
	if err != nil {
		t.Fatalf("failed to create terminal auth: %v", err)
	}
	t.Cleanup(func() { termAuth.Stop() })

	app := &Server{ptyMgr: ptyMgr, termAuth: termAuth}
	setupTestRoutes(t, app)

	req := httptest.NewRequest(http.MethodGet, "/api/terminal/token?session=test", nil)
	rr := httptest.NewRecorder()
	app.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected flat /api/terminal/token to return 404, got %d", rr.Code)
	}

	ct := rr.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}
}

// --- Health endpoint method restriction test ---

// TestSetupRoutes_HealthEndpoint_GETOnly verifies that GET /health returns 200 JSON
// and POST /health falls through to the frontend.
func TestSetupRoutes_HealthEndpoint_GETOnly(t *testing.T) {
	app := &Server{}
	setupTestRoutes(t, app)

	// GET should return JSON health response
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()
	app.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("GET /health: expected status %d, got %d", http.StatusOK, rr.Code)
	}

	ct := rr.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("GET /health: expected Content-Type 'application/json', got %q", ct)
	}

	// POST should fall through to frontend handler
	req = httptest.NewRequest(http.MethodPost, "/health", nil)
	rr = httptest.NewRecorder()
	app.mux.ServeHTTP(rr, req)

	ct = rr.Header().Get("Content-Type")
	if ct == "application/json" {
		t.Error("POST /health: should fall through to frontend, not return JSON")
	}
}

// --- Issue endpoints method restriction tests ---

// TestSetupRoutes_IssueEndpoints_MethodRestrictions verifies HTTP method restrictions
// on the removed flat issue endpoints.
func TestSetupRoutes_IssueEndpoints_MethodRestrictions(t *testing.T) {
	app := &Server{}
	setupTestRoutes(t, app)

	tests := []struct {
		name        string
		method      string
		path        string
		expectRoute bool // true if route is registered for this method
	}{
		{"GET /api/issues", http.MethodGet, "/api/issues", false},
		{"POST /api/issues", http.MethodPost, "/api/issues", false},
		{"DELETE /api/issues", http.MethodDelete, "/api/issues", false},
		{"DELETE /api/issues/test-id", http.MethodDelete, "/api/issues/test-id", false},
		{"GET /api/issues/test-id", http.MethodGet, "/api/issues/test-id", false},
		{"PATCH /api/issues/test-id", http.MethodPatch, "/api/issues/test-id", false},
		{"PUT /api/issues/test-id", http.MethodPut, "/api/issues/test-id", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			rr := httptest.NewRecorder()
			app.mux.ServeHTTP(rr, req)

			ct := rr.Header().Get("Content-Type")

			if tt.expectRoute {
				// Route should be handled by a registered handler (returns JSON, not 404)
				if rr.Code == http.StatusNotFound && ct == "application/json" {
					t.Errorf("%s %s: expected route handler, but got 404 JSON (unregistered API path)",
						tt.method, tt.path)
				}
			} else {
				// Unregistered /api/* paths should return 404 JSON
				if rr.Code != http.StatusNotFound {
					t.Errorf("%s %s: expected 404 for unregistered API path, got %d",
						tt.method, tt.path, rr.Code)
				}
			}
		})
	}
}

// --- Dependency endpoints method restriction tests ---

// TestSetupRoutes_DependencyEndpoints_MethodRestrictions verifies HTTP method
// restrictions on removed flat dependency endpoints.
func TestSetupRoutes_DependencyEndpoints_MethodRestrictions(t *testing.T) {
	app := &Server{}
	setupTestRoutes(t, app)

	tests := []struct {
		name        string
		method      string
		path        string
		expectRoute bool
	}{
		{"POST /api/issues/id/dependencies", http.MethodPost, "/api/issues/test-id/dependencies", false},
		{"DELETE /api/issues/id/dependencies/depId", http.MethodDelete, "/api/issues/test-id/dependencies/dep-1", false},
		{"GET /api/issues/id/dependencies", http.MethodGet, "/api/issues/test-id/dependencies", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			rr := httptest.NewRecorder()
			app.mux.ServeHTTP(rr, req)

			ct := rr.Header().Get("Content-Type")

			if tt.expectRoute {
				if rr.Code == http.StatusNotFound && ct == "application/json" {
					t.Errorf("%s %s: expected route handler, but got 404 JSON (unregistered API path)",
						tt.method, tt.path)
				}
			} else {
				// Unregistered /api/* paths should return 404 JSON
				if rr.Code != http.StatusNotFound {
					t.Errorf("%s %s: expected 404 for unregistered API path, got %d",
						tt.method, tt.path, rr.Code)
				}
			}
		})
	}
}

// --- Fleet endpoints all routes test ---

// TestSetupRoutes_FleetEndpoints_AllFlatRoutesReturn404 verifies that all flat
// fleet endpoints return 404 (removed in favor of workspace-scoped equivalents).
func TestSetupRoutes_FleetEndpoints_AllFlatRoutesReturn404(t *testing.T) {
	app := &Server{}
	setupTestRoutes(t, app)

	flatRoutes := []string{
		"/api/fleet/register",
		"/api/fleet/claim",
		"/api/fleet/done/test-id",
		"/api/fleet/heartbeat",
	}

	for _, path := range flatRoutes {
		t.Run("POST "+path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, path, nil)
			rr := httptest.NewRecorder()
			app.mux.ServeHTTP(rr, req)

			if rr.Code != http.StatusNotFound {
				t.Errorf("POST %s: expected 404 for removed flat route, got %d", path, rr.Code)
			}

			ct := rr.Header().Get("Content-Type")
			if ct != "application/json" {
				t.Errorf("POST %s: Content-Type = %q, want %q", path, ct, "application/json")
			}
		})
	}
}

// --- Unregistered /api/ catch-all test ---

// TestUnregisteredAPIPathReturnsJSONNotFound verifies that any unmatched path
// under /api/ returns a JSON 404 with {"error":"not found"}, replacing the
// former SPA catch-all's API guard now that the frontend is served externally.
func TestUnregisteredAPIPathReturnsJSONNotFound(t *testing.T) {
	app := &Server{}
	setupTestRoutes(t, app)

	paths := []string{"/api/nonexistent", "/api/some/deep/path", "/api/auth/token"}
	for _, p := range paths {
		t.Run(p, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, p, nil)
			rr := httptest.NewRecorder()
			app.mux.ServeHTTP(rr, req)

			if rr.Code != http.StatusNotFound {
				t.Errorf("status = %d, want 404", rr.Code)
			}
			if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", ct)
			}
			var body map[string]string
			if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
				t.Fatalf("response is not JSON: %v", err)
			}
			if body["error"] != "not found" {
				t.Errorf("error = %q, want %q", body["error"], "not found")
			}
		})
	}
}

// --- Tab metadata route conditional registration tests ---

// TestSetupRoutes_TabMetadataReturns404WhenStoreNil verifies that GET /api/terminal/tabs
// returns a 404 JSON response when tabMetaStore is nil. When Redis is not
// configured the tab metadata routes are not registered, so the request falls
// through to the /api/ JSON-404 catch-all.
func TestSetupRoutes_TabMetadataReturns404WhenStoreNil(t *testing.T) {
	// All nil params — tabMetaStore (param 21) is nil, so tab metadata routes are not registered.
	app := &Server{}
	setupTestRoutes(t, app)

	req := httptest.NewRequest(http.MethodGet, "/api/terminal/tabs", nil)
	rr := httptest.NewRecorder()
	app.mux.ServeHTTP(rr, req)

	// Route is not registered; /api/ catch-all rejects with 404 JSON
	if rr.Code != http.StatusNotFound {
		t.Errorf("expected /api/terminal/tabs to return %d when tabMetaStore is nil, got %d",
			http.StatusNotFound, rr.Code)
	}

	ct := rr.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("expected Content-Type 'application/json', got %q", ct)
	}
}

// TestFlatAgentRoutesRemoved verifies that the flat agent routes
// (e.g. POST /api/agents/{name}/git/push) have been removed and return 404,
// while the workspace-scoped equivalents (e.g.
// POST /api/workspaces/{ws}/agents/{name}/git/push) still work.
func TestFlatAgentRoutesRemoved(t *testing.T) {
	// Register workspace identity so workspace-scoped routes are functional.
	app := &Server{wsResolveFn: testWorkspaceResolver("test-ws"), agentSvc: agentcoord.NewAgentService(nil, nil)}
	sourcePorts := &stubFileService{}
	app.sourceBrowse = sourcePorts
	app.sourceMutate = sourcePorts
	app.sourceCheckout = sourcePorts
	app.issueDiff = &stubIssueDiff{}
	app.sessSvc = sessioncoord.NewSessionService(nil, nil, nil)
	setupTestRoutes(t, app)

	// Removed flat routes; each must return 404.
	noConfigRoutes := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/git/push-all"},
		{http.MethodPost, "/api/agents/alice/git/push"},
		{http.MethodPost, "/api/agents/alice/git/pull"},
		{http.MethodPost, "/api/agents/alice/git/sync"},
		{http.MethodPost, "/api/agents/alice/git/pr"},
		{http.MethodPost, "/api/agents/alice/git/reset"},
		{http.MethodGet, "/api/agents/alice/git/status"},
		{http.MethodPatch, "/api/agents/alice/git/target"},
		{http.MethodGet, "/api/issues/ISSUE-1/git/diff-stat"},
		{http.MethodGet, "/api/agents/alice/diff/commits"},
		{http.MethodGet, "/api/agents/alice/diff/files"},
		{http.MethodGet, "/api/agents/alice/diff/file"},
		{http.MethodGet, "/api/agents/alice/files/tree"},
		{http.MethodGet, "/api/agents/alice/files"},
		{http.MethodPut, "/api/agents/alice/files"},
	}

	for _, tc := range noConfigRoutes {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			rr := httptest.NewRecorder()
			app.mux.ServeHTTP(rr, req)

			if rr.Code != http.StatusNotFound {
				t.Errorf("removed route %s %s: expected status %d, got %d",
					tc.method, tc.path, http.StatusNotFound, rr.Code)
			}

			ct := rr.Header().Get("Content-Type")
			if ct != "application/json" {
				t.Errorf("removed route %s %s: expected Content-Type 'application/json', got %q",
					tc.method, tc.path, ct)
			}
		})
	}

	// Workspace-scoped equivalents should be handled by the wsMux routes.
	// The mock ops return "not found" for agent resolution, so handlers may
	// still return 404 with an agent-specific error. The key assertion is that
	// the response body differs from the SPA catch-all's generic {"error":"not found"}.
	// If the route were truly unregistered, the SPA catch-all would respond with
	// exactly that generic message.
	scopedRoutes := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/workspaces/test-ws/git/push-all"},
		{http.MethodPost, "/api/workspaces/test-ws/agents/alice/git/push"},
		{http.MethodPost, "/api/workspaces/test-ws/agents/alice/git/pull"},
		{http.MethodPost, "/api/workspaces/test-ws/agents/alice/git/sync"},
		{http.MethodPost, "/api/workspaces/test-ws/agents/alice/git/pr"},
		{http.MethodPost, "/api/workspaces/test-ws/agents/alice/git/reset"},
		{http.MethodGet, "/api/workspaces/test-ws/agents/alice/git/status"},
		{http.MethodPatch, "/api/workspaces/test-ws/agents/alice/git/target"},
		{http.MethodGet, "/api/workspaces/test-ws/agents/alice/diff/commits"},
		{http.MethodGet, "/api/workspaces/test-ws/agents/alice/diff/files"},
		{http.MethodGet, "/api/workspaces/test-ws/agents/alice/diff/file"},
		{http.MethodGet, "/api/workspaces/test-ws/files/git-status"},
		{http.MethodGet, "/api/workspaces/test-ws/files/diff"},
		{http.MethodGet, "/api/workspaces/test-ws/files/history"},
		{http.MethodGet, "/api/workspaces/test-ws/files/blame"},
	}

	for _, tc := range scopedRoutes {
		t.Run("scoped "+tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			rr := httptest.NewRecorder()
			app.mux.ServeHTTP(rr, req)

			// The SPA catch-all returns exactly {"error":"not found"} for
			// unregistered /api/* paths. If the route is properly registered,
			// the handler produces a different response (even if it is still
			// a 404 with a more specific error message like "agent worktree
			// \"alice\" not found").
			var body map[string]interface{}
			if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
				t.Fatalf("workspace-scoped route %s %s: failed to parse JSON body: %v",
					tc.method, tc.path, err)
			}

			errMsg, _ := body["error"].(string)
			if rr.Code == http.StatusNotFound && errMsg == "not found" {
				t.Errorf("workspace-scoped route %s %s fell through to SPA catch-all (got generic 404 %q); route is not registered",
					tc.method, tc.path, errMsg)
			}
		})
	}
}

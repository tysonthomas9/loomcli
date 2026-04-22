package webui_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/webui"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// --- HandleWsDaemonSupervisor tests ---

func TestHandleWsDaemonSupervisor_HappyPath(t *testing.T) {
	fn := func(wsID string) (*webui.DaemonSupervisorData, error) {
		return &webui.DaemonSupervisorData{
			PID:           42,
			StartedAt:     time.Now().Add(-1 * time.Hour),
			UptimeSeconds: 3600,
			Agents: []webui.DaemonAgentEntry{
				{Worktree: "falcon", Role: "planner", PID: 43, Status: "running"},
			},
		}, nil
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/workspaces/{ws}/daemon/supervisor", webui.HandleWsDaemonSupervisor(fn))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/workspaces/ws-1/daemon/supervisor", nil)
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "ws-1"))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Success bool                       `json:"success"`
		Data    webui.DaemonSupervisorData `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.Success {
		t.Error("success = false, want true")
	}
	if resp.Data.PID != 42 {
		t.Errorf("PID = %d, want 42", resp.Data.PID)
	}
	if len(resp.Data.Agents) != 1 || resp.Data.Agents[0].Worktree != "falcon" {
		t.Errorf("Agents = %+v, want [falcon]", resp.Data.Agents)
	}
}

func TestHandleWsDaemonSupervisor_DaemonNotRunning(t *testing.T) {
	fn := func(wsID string) (*webui.DaemonSupervisorData, error) {
		return nil, os.ErrNotExist
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/workspaces/ws-1/daemon/supervisor", nil)
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "ws-1"))
	webui.HandleWsDaemonSupervisor(fn).ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	var resp struct {
		Success bool   `json:"success"`
		Code    string `json:"code"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Code != "daemon_not_running" {
		t.Errorf("code = %q, want daemon_not_running", resp.Code)
	}
}

func TestHandleWsDaemonSupervisor_ResolverError(t *testing.T) {
	fn := func(wsID string) (*webui.DaemonSupervisorData, error) {
		return nil, errors.New(`resolve workspace "missing" daemon: workspace not found`)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/workspaces/missing/daemon/supervisor", nil)
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "missing"))
	webui.HandleWsDaemonSupervisor(fn).ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	var resp struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Code != "daemon_unavailable" {
		t.Errorf("code = %q, want daemon_unavailable", resp.Code)
	}
}

func TestHandleWsDaemonSupervisor_PassesWorkspaceID(t *testing.T) {
	var captured string
	fn := func(wsID string) (*webui.DaemonSupervisorData, error) {
		captured = wsID
		return &webui.DaemonSupervisorData{PID: 1}, nil
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/workspaces/ws-uuid-123/daemon/supervisor", nil)
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "ws-uuid-123"))
	webui.HandleWsDaemonSupervisor(fn).ServeHTTP(rec, req)

	if captured != "ws-uuid-123" {
		t.Errorf("wsID = %q, want ws-uuid-123", captured)
	}
}

// --- HandleWsDaemonConfig tests ---

func TestHandleWsDaemonConfig_HappyPath(t *testing.T) {
	raw := json.RawMessage(`{"backend":"claude","daemon":{"restart_policy":"always"}}`)
	fn := func(wsID string) (json.RawMessage, error) {
		return raw, nil
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/workspaces/ws-1/daemon/config", nil)
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "ws-1"))
	webui.HandleWsDaemonConfig(fn).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp struct {
		Success bool            `json:"success"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.Success {
		t.Error("success = false, want true")
	}
	var data map[string]any
	_ = json.Unmarshal(resp.Data, &data)
	if data["backend"] != "claude" {
		t.Errorf("data.backend = %v, want claude", data["backend"])
	}
}

func TestHandleWsDaemonConfig_LoadError(t *testing.T) {
	fn := func(wsID string) (json.RawMessage, error) {
		return nil, errors.New("syntax error in loom.yaml")
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/workspaces/ws-1/daemon/config", nil)
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "ws-1"))
	webui.HandleWsDaemonConfig(fn).ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	var resp struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Code != "config_error" {
		t.Errorf("code = %q, want config_error", resp.Code)
	}
}

func TestHandleWsDaemonConfig_PassesWorkspaceID(t *testing.T) {
	var captured string
	fn := func(wsID string) (json.RawMessage, error) {
		captured = wsID
		return json.RawMessage(`{}`), nil
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/workspaces/ws-abc/daemon/config", nil)
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "ws-abc"))
	webui.HandleWsDaemonConfig(fn).ServeHTTP(rec, req)

	if captured != "ws-abc" {
		t.Errorf("wsID = %q, want ws-abc", captured)
	}
}

// --- DaemonModule registration tests ---

func TestDaemonModule_Register_BothFunctions(t *testing.T) {
	var capturedSup, capturedCfg string
	supFn := func(wsID string) (*webui.DaemonSupervisorData, error) {
		capturedSup = wsID
		return &webui.DaemonSupervisorData{PID: 1}, nil
	}
	cfgFn := func(wsID string) (json.RawMessage, error) {
		capturedCfg = wsID
		return json.RawMessage(`{}`), nil
	}

	mod := webui.NewDaemonModule(supFn, cfgFn)
	mux := http.NewServeMux()
	mod.Register(mux)

	// Supervisor route — full Register → mux → handler path must preserve wsID.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/workspaces/ws-1/daemon/supervisor", nil)
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "ws-1"))
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("supervisor: status = %d, want 200", rec.Code)
	}
	if capturedSup != "ws-1" {
		t.Errorf("supervisor wsID = %q, want ws-1", capturedSup)
	}

	// Config route
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/api/workspaces/ws-2/daemon/config", nil)
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "ws-2"))
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("config: status = %d, want 200", rec.Code)
	}
	if capturedCfg != "ws-2" {
		t.Errorf("config wsID = %q, want ws-2", capturedCfg)
	}
}

func TestDaemonModule_Register_SupervisorOnly(t *testing.T) {
	supFn := func(wsID string) (*webui.DaemonSupervisorData, error) {
		return &webui.DaemonSupervisorData{PID: 1}, nil
	}
	mod := webui.NewDaemonModule(supFn, nil)
	mux := http.NewServeMux()
	mod.Register(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/workspaces/ws-1/daemon/supervisor", nil)
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "ws-1"))
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("supervisor: status = %d, want 200", rec.Code)
	}

	// Config route should not be registered -> 404
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/api/workspaces/ws-1/daemon/config", nil)
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "ws-1"))
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("config: status = %d, want 404 (not registered when configFn is nil)", rec.Code)
	}
}

func TestDaemonModule_Register_ConfigOnly(t *testing.T) {
	cfgFn := func(wsID string) (json.RawMessage, error) {
		return json.RawMessage(`{}`), nil
	}
	mod := webui.NewDaemonModule(nil, cfgFn)
	mux := http.NewServeMux()
	mod.Register(mux)

	// Supervisor route should not be registered -> 404
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/workspaces/ws-1/daemon/supervisor", nil)
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "ws-1"))
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("supervisor: status = %d, want 404", rec.Code)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/api/workspaces/ws-1/daemon/config", nil)
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "ws-1"))
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("config: status = %d, want 200", rec.Code)
	}
}

package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
)

// stubPool is a minimal daemon.Pool for handler tests.
type stubPool struct{}

func (s *stubPool) Get(_ context.Context) (*rpc.Client, error) { return &rpc.Client{}, nil }
func (s *stubPool) Put(_ *rpc.Client)                          {}
func (s *stubPool) Discard(_ *rpc.Client)                      {}
func (s *stubPool) Stats() daemon.PoolStats {
	return daemon.PoolStats{Size: 10, Created: 2, Active: 1, Available: 1}
}
func (s *stubPool) Close() error { return nil }

func TestHandleListWorkspaces(t *testing.T) {
	mp := daemon.NewMultiPool(WorkspaceFromContext, 10)
	_ = mp.Register("ws-alpha", &stubPool{})
	_ = mp.Register("ws-beta", &stubPool{})

	configFn := func() (*WorkspaceData, error) {
		return &WorkspaceData{
			Workspaces: []WorkspaceSummary{
				{Name: "ws-alpha", Path: "/path/alpha", Active: true},
				{Name: "ws-beta", Path: "/path/beta", Active: false},
			},
		}, nil
	}

	handler := handleListWorkspaces(mp, configFn)
	req := httptest.NewRequest("GET", "/api/workspaces", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp struct {
		Success    bool                `json:"success"`
		Workspaces []workspaceListItem `json:"workspaces"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if !resp.Success {
		t.Error("expected success=true")
	}
	if len(resp.Workspaces) != 2 {
		t.Fatalf("expected 2 workspaces, got %d", len(resp.Workspaces))
	}
}

func TestHandleListWorkspaces_NilConfig(t *testing.T) {
	mp := daemon.NewMultiPool(WorkspaceFromContext, 10)
	_ = mp.Register("ws", &stubPool{})

	handler := handleListWorkspaces(mp, nil)
	req := httptest.NewRequest("GET", "/api/workspaces", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestHandleGetWorkspace_Found(t *testing.T) {
	mp := daemon.NewMultiPool(WorkspaceFromContext, 10)
	_ = mp.Register("ws-alpha", &stubPool{})

	configFn := func() (*WorkspaceData, error) {
		return &WorkspaceData{
			Workspaces: []WorkspaceSummary{
				{Name: "ws-alpha", Path: "/path/alpha", Active: true},
			},
		}, nil
	}

	handler := handleGetWorkspace(mp, configFn)

	// Use a mux to exercise PathValue
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/workspaces/{ws}", handler)

	req := httptest.NewRequest("GET", "/api/workspaces/ws-alpha", nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp struct {
		Success   bool              `json:"success"`
		Workspace workspaceListItem `json:"workspace"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if !resp.Success {
		t.Error("expected success=true")
	}
	if resp.Workspace.ID != "ws-alpha" {
		t.Errorf("expected ID 'ws-alpha', got %q", resp.Workspace.ID)
	}
	if resp.Workspace.Path != "/path/alpha" {
		t.Errorf("expected path '/path/alpha', got %q", resp.Workspace.Path)
	}
	if resp.Workspace.Pool == nil {
		t.Error("expected non-nil pool stats")
	}
}

func TestHandleGetWorkspace_NotFound(t *testing.T) {
	mp := daemon.NewMultiPool(WorkspaceFromContext, 10)

	handler := handleGetWorkspace(mp, nil)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/workspaces/{ws}", handler)

	req := httptest.NewRequest("GET", "/api/workspaces/nonexistent", nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestHandleGetWorkspace_MissingPathParam(t *testing.T) {
	mp := daemon.NewMultiPool(WorkspaceFromContext, 10)
	handler := handleGetWorkspace(mp, nil)

	// Call directly without mux so PathValue("ws") returns ""
	req := httptest.NewRequest("GET", "/api/workspaces/", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

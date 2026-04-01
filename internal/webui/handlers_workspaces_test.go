package webui

import (
	"context"
	"encoding/json"
	"fmt"
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
func (s *stubPool) PutAfterError(_ *rpc.Client)                {}
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
	configByIDFn := func(id string) (*WorkspaceData, error) {
		if id == "ws-alpha" {
			return &WorkspaceData{
				ID:   "ws-alpha",
				Name: "ws-alpha",
				Path: "/path/alpha",
				Workspaces: []WorkspaceSummary{
					{ID: "ws-alpha", Name: "ws-alpha", Path: "/path/alpha"},
				},
			}, nil
		}
		return nil, fmt.Errorf("not found")
	}

	wsExistsFn := func(id string) bool { return id == "ws-alpha" }
	handler := handleGetWorkspace(wsExistsFn, configByIDFn)

	// Use a mux to exercise PathValue
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/workspaces/{ws}", handler)

	req := httptest.NewRequest("GET", "/api/workspaces/ws-alpha", nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp workspaceResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if !resp.Success {
		t.Error("expected success=true")
	}
	if resp.Data == nil {
		t.Fatal("expected non-nil data")
	}
	if resp.Data.ID != "ws-alpha" {
		t.Errorf("expected ID 'ws-alpha', got %q", resp.Data.ID)
	}
	if resp.Data.Path != "/path/alpha" {
		t.Errorf("expected path '/path/alpha', got %q", resp.Data.Path)
	}
}

func TestHandleGetWorkspace_NotFound(t *testing.T) {
	wsExistsFn := func(id string) bool { return false }
	configByIDFn := func(id string) (*WorkspaceData, error) { return nil, fmt.Errorf("not found") }
	handler := handleGetWorkspace(wsExistsFn, configByIDFn)

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
	wsExistsFn := func(id string) bool { return false }
	configByIDFn := func(id string) (*WorkspaceData, error) { return nil, fmt.Errorf("not found") }
	handler := handleGetWorkspace(wsExistsFn, configByIDFn)

	// Call directly without mux so PathValue("ws") returns ""
	req := httptest.NewRequest("GET", "/api/workspaces/", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

// TestHandleListWorkspaces_UUIDKeyedPools tests post-T2 behavior where pools
// are registered by UUID and config summaries carry ID fields. The response
// should contain the human-readable name and the UUID id.
func TestHandleListWorkspaces_UUIDKeyedPools(t *testing.T) {
	mp := daemon.NewMultiPool(WorkspaceFromContext, 10)
	// Register pools by UUID (post-T2)
	_ = mp.Register("aaaa-1111-uuid", &stubPool{})
	_ = mp.Register("bbbb-2222-uuid", &stubPool{})

	configFn := func() (*WorkspaceData, error) {
		return &WorkspaceData{
			Workspaces: []WorkspaceSummary{
				{ID: "aaaa-1111-uuid", Name: "ws-alpha", Path: "/path/alpha", Active: true},
				{ID: "bbbb-2222-uuid", Name: "ws-beta", Path: "/path/beta", Active: false},
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

	// Build a map for easier lookup (order from WorkspaceIDs is not guaranteed)
	byID := make(map[string]workspaceListItem, len(resp.Workspaces))
	for _, ws := range resp.Workspaces {
		byID[ws.ID] = ws
	}

	alpha, ok := byID["aaaa-1111-uuid"]
	if !ok {
		t.Fatal("expected workspace with ID 'aaaa-1111-uuid'")
	}
	if alpha.Name != "ws-alpha" {
		t.Errorf("expected name 'ws-alpha', got %q", alpha.Name)
	}
	if alpha.Path != "/path/alpha" {
		t.Errorf("expected path '/path/alpha', got %q", alpha.Path)
	}
	if !alpha.Active {
		t.Error("expected ws-alpha to be active")
	}

	beta, ok := byID["bbbb-2222-uuid"]
	if !ok {
		t.Fatal("expected workspace with ID 'bbbb-2222-uuid'")
	}
	if beta.Name != "ws-beta" {
		t.Errorf("expected name 'ws-beta', got %q", beta.Name)
	}
	if beta.Active {
		t.Error("expected ws-beta to be inactive")
	}
}

// TestHandleListWorkspaces_MixedState tests a transitional state where the
// pool is still keyed by name (pre-T2) but config summaries already carry
// UUID IDs. The response should enrich the id from the config summary.
func TestHandleListWorkspaces_MixedState(t *testing.T) {
	mp := daemon.NewMultiPool(WorkspaceFromContext, 10)
	// Pool registered by name (pre-T2 style)
	_ = mp.Register("ws-alpha", &stubPool{})

	configFn := func() (*WorkspaceData, error) {
		return &WorkspaceData{
			Workspaces: []WorkspaceSummary{
				{ID: "aaaa-1111-uuid", Name: "ws-alpha", Path: "/path/alpha", Active: true},
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

	if len(resp.Workspaces) != 1 {
		t.Fatalf("expected 1 workspace, got %d", len(resp.Workspaces))
	}

	ws := resp.Workspaces[0]
	// Name-keyed pool should be enriched with UUID from config
	if ws.ID != "aaaa-1111-uuid" {
		t.Errorf("expected ID 'aaaa-1111-uuid', got %q", ws.ID)
	}
	if ws.Name != "ws-alpha" {
		t.Errorf("expected Name 'ws-alpha', got %q", ws.Name)
	}
	if ws.Path != "/path/alpha" {
		t.Errorf("expected path '/path/alpha', got %q", ws.Path)
	}
	if !ws.Active {
		t.Error("expected ws-alpha to be active")
	}
}

// TestHandleGetWorkspace_ByUUID tests that a workspace can be retrieved by UUID
// and returns full WorkspaceData.
func TestHandleGetWorkspace_ByUUID(t *testing.T) {
	const uuid = "cccc-3333-uuid"

	configByIDFn := func(id string) (*WorkspaceData, error) {
		if id == uuid {
			return &WorkspaceData{
				ID:   uuid,
				Name: "ws-gamma",
				Path: "/path/gamma",
				Workspaces: []WorkspaceSummary{
					{ID: uuid, Name: "ws-gamma", Path: "/path/gamma"},
				},
			}, nil
		}
		return nil, fmt.Errorf("not found")
	}

	wsExistsFn := func(id string) bool { return id == uuid }
	handler := handleGetWorkspace(wsExistsFn, configByIDFn)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/workspaces/{ws}", handler)

	req := httptest.NewRequest("GET", "/api/workspaces/"+uuid, nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp workspaceResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if !resp.Success {
		t.Error("expected success=true")
	}
	if resp.Data == nil {
		t.Fatal("expected non-nil data")
	}
	if resp.Data.ID != uuid {
		t.Errorf("expected ID %q, got %q", uuid, resp.Data.ID)
	}
	if resp.Data.Name != "ws-gamma" {
		t.Errorf("expected Name 'ws-gamma', got %q", resp.Data.Name)
	}
	if resp.Data.Path != "/path/gamma" {
		t.Errorf("expected path '/path/gamma', got %q", resp.Data.Path)
	}
	// The requested workspace should be marked active in the list
	if len(resp.Data.Workspaces) != 1 || !resp.Data.Workspaces[0].Active {
		t.Error("expected requested workspace to be marked active")
	}
}

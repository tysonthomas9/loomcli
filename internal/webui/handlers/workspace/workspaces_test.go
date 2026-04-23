package workspace

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

func TestHandleListWorkspaces(t *testing.T) {
	svc := &mockWorkspaceService{
		listWorkspacesFn: func(_ context.Context) ([]service.WorkspaceListItem, error) {
			return []service.WorkspaceListItem{
				{ID: "ws-alpha", Name: "ws-alpha", Path: "/path/alpha", Active: true, RepoCount: 3, IsDefault: true, Pool: &service.PoolStats{Size: 10, Active: 1}},
				{ID: "ws-beta", Name: "ws-beta", Path: "/path/beta", Active: false, RepoCount: 0, IsDefault: false},
			}, nil
		},
	}

	handler := handleListWorkspaces(svc)
	req := httptest.NewRequest("GET", "/api/workspaces", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	rawBody := rec.Body.Bytes()
	var body struct {
		Success    bool                        `json:"success"`
		Workspaces []service.WorkspaceListItem `json:"workspaces"`
	}
	if err := json.Unmarshal(rawBody, &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.Success {
		t.Fatal("expected success=true")
	}
	if len(body.Workspaces) != 2 {
		t.Fatalf("expected 2 workspaces, got %d", len(body.Workspaces))
	}
	if body.Workspaces[0].RepoCount != 3 {
		t.Errorf("expected ws-alpha repo_count=3, got %d", body.Workspaces[0].RepoCount)
	}
	if !body.Workspaces[0].IsDefault {
		t.Errorf("expected ws-alpha is_default=true, got false")
	}
	if body.Workspaces[1].RepoCount != 0 {
		t.Errorf("expected ws-beta repo_count=0, got %d", body.Workspaces[1].RepoCount)
	}
	if body.Workspaces[1].IsDefault {
		t.Errorf("expected ws-beta is_default=false, got true")
	}

	// Verify raw JSON includes is_default=false and repo_count=0 (not omitted).
	var raw struct {
		Workspaces []map[string]any `json:"workspaces"`
	}
	if err := json.Unmarshal(rawBody, &raw); err != nil {
		t.Fatalf("raw decode: %v", err)
	}
	if _, ok := raw.Workspaces[1]["is_default"]; !ok {
		t.Error("is_default should be present in JSON even when false")
	}
	if _, ok := raw.Workspaces[1]["repo_count"]; !ok {
		t.Error("repo_count should be present in JSON even when zero")
	}
}

func TestHandleListWorkspaces_Empty(t *testing.T) {
	svc := &mockWorkspaceService{
		listWorkspacesFn: func(_ context.Context) ([]service.WorkspaceListItem, error) {
			return []service.WorkspaceListItem{}, nil
		},
	}

	handler := handleListWorkspaces(svc)
	req := httptest.NewRequest("GET", "/api/workspaces", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestHandleGetWorkspace(t *testing.T) {
	svc := &mockWorkspaceService{
		getWorkspaceFn: func(_ context.Context, wsID string) (*ops.WorkspaceData, error) {
			if wsID != "ws-alpha" {
				return nil, service.ErrNotFound("workspace not found")
			}
			return &ops.WorkspaceData{
				ID:   "ws-alpha",
				Name: "alpha",
				Path: "/path/alpha",
				Workspaces: []ops.WorkspaceSummary{
					{ID: "ws-alpha", Name: "alpha", Path: "/path/alpha", Active: true},
					{ID: "ws-beta", Name: "beta", Path: "/path/beta"},
				},
				Repos:  []ops.WorkspaceRepo{},
				Groups: []string{},
				Agents: []ops.WorkspaceAgentInfo{},
			}, nil
		},
	}

	handler := handleGetWorkspace(svc)
	req := httptest.NewRequest("GET", "/api/workspaces/ws-alpha", nil)
	req.SetPathValue("ws", "ws-alpha")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp WorkspaceResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Success {
		t.Fatalf("expected success, got error: %s", resp.Error)
	}
	if resp.Data.Name != "alpha" {
		t.Errorf("expected name alpha, got %s", resp.Data.Name)
	}
}

func TestHandleGetWorkspace_NotFound(t *testing.T) {
	svc := &mockWorkspaceService{
		getWorkspaceFn: func(_ context.Context, _ string) (*ops.WorkspaceData, error) {
			return nil, service.ErrNotFound("workspace not found")
		},
	}

	handler := handleGetWorkspace(svc)
	req := httptest.NewRequest("GET", "/api/workspaces/unknown", nil)
	req.SetPathValue("ws", "unknown")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestHandleGetWorkspace_EmptyID(t *testing.T) {
	svc := &mockWorkspaceService{}
	handler := handleGetWorkspace(svc)
	req := httptest.NewRequest("GET", "/api/workspaces/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

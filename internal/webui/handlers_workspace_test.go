package webui

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandleWorkspace_NilConfigFn(t *testing.T) {
	handler := handleWorkspace(nil)
	req := httptest.NewRequest(http.MethodGet, "/api/workspace", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp workspaceResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if !resp.Success {
		t.Fatal("expected success=true")
	}
	if resp.Data == nil {
		t.Fatal("expected non-nil data")
	}
	if len(resp.Data.Repos) != 0 {
		t.Fatalf("expected empty repos, got %d", len(resp.Data.Repos))
	}
}

func TestHandleWorkspace_EmptyRepos(t *testing.T) {
	handler := handleWorkspace(func() (*WorkspaceData, error) {
		return nil, nil
	})
	req := httptest.NewRequest(http.MethodGet, "/api/workspace", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp workspaceResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if !resp.Success {
		t.Fatal("expected success=true")
	}
	if resp.Data == nil {
		t.Fatal("expected non-nil data")
	}
	if len(resp.Data.Repos) != 0 {
		t.Fatalf("expected empty repos, got %d", len(resp.Data.Repos))
	}
}

func TestHandleWorkspace_WithRepos(t *testing.T) {
	handler := handleWorkspace(func() (*WorkspaceData, error) {
		return &WorkspaceData{
			Name: "myworkspace",
			Path: "/workspaces/myworkspace",
			Repos: []WorkspaceRepo{
				{Name: "payments/api", Path: "/workspaces/payments/api", DefaultBranch: "main", Remote: "origin"},
				{Name: "auth/service", Path: "/workspaces/auth/service", DefaultBranch: "develop", Remote: "upstream"},
			},
		}, nil
	})
	req := httptest.NewRequest(http.MethodGet, "/api/workspace", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp workspaceResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if !resp.Success {
		t.Fatal("expected success=true")
	}
	if resp.Data.Name != "myworkspace" {
		t.Errorf("expected name myworkspace, got %s", resp.Data.Name)
	}
	if resp.Data.Path != "/workspaces/myworkspace" {
		t.Errorf("expected path /workspaces/myworkspace, got %s", resp.Data.Path)
	}
	if len(resp.Data.Repos) != 2 {
		t.Fatalf("expected 2 repos, got %d", len(resp.Data.Repos))
	}
	if resp.Data.Repos[0].Name != "payments/api" {
		t.Errorf("expected name payments/api, got %s", resp.Data.Repos[0].Name)
	}
	if resp.Data.Repos[1].Path != "/workspaces/auth/service" {
		t.Errorf("expected path /workspaces/auth/service, got %s", resp.Data.Repos[1].Path)
	}
	if resp.Data.Repos[1].DefaultBranch != "develop" {
		t.Errorf("expected default_branch develop, got %s", resp.Data.Repos[1].DefaultBranch)
	}
	if resp.Data.Repos[1].Remote != "upstream" {
		t.Errorf("expected remote upstream, got %s", resp.Data.Repos[1].Remote)
	}
}

func TestHandleWorkspace_ConfigError(t *testing.T) {
	handler := handleWorkspace(func() (*WorkspaceData, error) {
		return nil, errors.New("config broken")
	})
	req := httptest.NewRequest(http.MethodGet, "/api/workspace", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}

	var resp workspaceResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if resp.Success {
		t.Fatal("expected success=false")
	}
	if resp.Error != "failed to load workspace config" {
		t.Errorf("unexpected error message: %s", resp.Error)
	}
}

func TestHandleWorkspace_NilRepos(t *testing.T) {
	handler := handleWorkspace(func() (*WorkspaceData, error) {
		return &WorkspaceData{Name: "ws", Path: "/ws"}, nil
	})
	req := httptest.NewRequest(http.MethodGet, "/api/workspace", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp workspaceResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if !resp.Success {
		t.Fatal("expected success=true")
	}
	if resp.Data.Repos == nil {
		t.Fatal("expected non-nil repos slice")
	}
	if len(resp.Data.Repos) != 0 {
		t.Fatalf("expected empty repos, got %d", len(resp.Data.Repos))
	}
}

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
	if len(resp.Repos) != 0 {
		t.Fatalf("expected empty repos, got %d", len(resp.Repos))
	}
}

func TestHandleWorkspace_EmptyRepos(t *testing.T) {
	handler := handleWorkspace(func() ([]WorkspaceRepo, error) {
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
	if len(resp.Repos) != 0 {
		t.Fatalf("expected empty repos, got %d", len(resp.Repos))
	}
}

func TestHandleWorkspace_WithRepos(t *testing.T) {
	repos := []WorkspaceRepo{
		{Name: "payments/api", Path: "/workspaces/payments/api"},
		{Name: "auth/service", Path: "/workspaces/auth/service"},
	}
	handler := handleWorkspace(func() ([]WorkspaceRepo, error) {
		return repos, nil
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
	if len(resp.Repos) != 2 {
		t.Fatalf("expected 2 repos, got %d", len(resp.Repos))
	}
	if resp.Repos[0].Name != "payments/api" {
		t.Errorf("expected name payments/api, got %s", resp.Repos[0].Name)
	}
	if resp.Repos[1].Path != "/workspaces/auth/service" {
		t.Errorf("expected path /workspaces/auth/service, got %s", resp.Repos[1].Path)
	}
}

func TestHandleWorkspace_ConfigError(t *testing.T) {
	handler := handleWorkspace(func() ([]WorkspaceRepo, error) {
		return nil, errors.New("config broken")
	})
	req := httptest.NewRequest(http.MethodGet, "/api/workspace", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if resp["error"] != "failed to load workspace config" {
		t.Errorf("unexpected error message: %s", resp["error"])
	}
}

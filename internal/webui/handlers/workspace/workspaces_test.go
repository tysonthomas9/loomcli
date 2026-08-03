package workspace

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
	"github.com/tysonthomas9/loomcli/internal/workspaceerrors"
)

func TestHandleListWorkspaces(t *testing.T) {
	svc := &mockWorkspaceService{
		listWorkspacesFn: func(_ context.Context) ([]service.WorkspaceListItem, error) {
			return []service.WorkspaceListItem{
				{ID: "ws-alpha", Name: "ws-alpha", Path: "/path/alpha", Active: true},
				{ID: "ws-beta", Name: "ws-beta", Path: "/path/beta", Active: false},
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

	var body struct {
		Success    bool                        `json:"success"`
		Workspaces []service.WorkspaceListItem `json:"workspaces"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.Success {
		t.Fatal("expected success=true")
	}
	if len(body.Workspaces) != 2 {
		t.Fatalf("expected 2 workspaces, got %d", len(body.Workspaces))
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

func TestHandleAddWorkspaceRepos_RemoteCloneReturnsAcceptedWithoutSynchronousMutation(t *testing.T) {
	syncCalled := false
	svc := &mockWorkspaceService{
		addWorkspaceReposFn: func(_ context.Context, _ service.WorkspaceAddReposRequest) (*ops.WorkspaceData, error) {
			syncCalled = true
			return nil, nil
		},
		startAsyncAddReposFn: func(_ context.Context, req service.WorkspaceAddReposRequest) (string, error) {
			if req.WorkspaceID != "ALLAGENTS" {
				t.Fatalf("workspace ID = %q, want ALLAGENTS", req.WorkspaceID)
			}
			if len(req.CloneURLs) != 1 || req.CloneURLs[0] != "https://github.com/acme/slow.git" {
				t.Fatalf("clone URLs = %#v", req.CloneURLs)
			}
			return "add-repos-job-123", nil
		},
	}

	handler := HandleAddWorkspaceRepos(svc)
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/workspaces/ALLAGENTS/repos",
		strings.NewReader(`{"clone_urls":["https://github.com/acme/slow.git"]}`),
	)
	req.SetPathValue("ws", "ALLAGENTS")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", rec.Code, rec.Body.String())
	}
	if syncCalled {
		t.Fatal("remote clone must not run in the request-bound synchronous path")
	}
	var body struct {
		Success bool   `json:"success"`
		JobID   string `json:"job_id"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !body.Success || body.JobID != "add-repos-job-123" {
		t.Fatalf("response = %#v, want accepted job", body)
	}
}

func TestHandleAddWorkspaceRepos_LocalPathRemainsSynchronous(t *testing.T) {
	asyncCalled := false
	want := &ops.WorkspaceData{ID: "ALLAGENTS"}
	svc := &mockWorkspaceService{
		addWorkspaceReposFn: func(_ context.Context, req service.WorkspaceAddReposRequest) (*ops.WorkspaceData, error) {
			if len(req.Repos) != 1 || req.Repos[0] != "/workspace/source-repo" {
				t.Fatalf("repos = %#v", req.Repos)
			}
			return want, nil
		},
		startAsyncAddReposFn: func(_ context.Context, _ service.WorkspaceAddReposRequest) (string, error) {
			asyncCalled = true
			return "", nil
		},
	}

	handler := HandleAddWorkspaceRepos(svc)
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/workspaces/ALLAGENTS/repos",
		strings.NewReader(`{"repos":["/workspace/source-repo"]}`),
	)
	req.SetPathValue("ws", "ALLAGENTS")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	if asyncCalled {
		t.Fatal("local path attachment should remain synchronous")
	}
}

func TestHandleAddWorkspaceRepos_NameCollisionReturnsConflict(t *testing.T) {
	const message = `repository name "source-repo" is already registered; repository names must be unique across workspaces`
	svc := service.NewWorkspaceService(service.WorkspaceServiceConfig{
		AddReposFn: func(_ context.Context, _ service.WorkspaceAddReposRequest) (service.WorkspaceCreateResult, error) {
			return service.WorkspaceCreateResult{}, workspaceerrors.New(workspaceerrors.AlreadyExists, message, nil)
		},
	})

	handler := HandleAddWorkspaceRepos(svc)
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/workspaces/ALLAGENTS/repos",
		strings.NewReader(`{"repos":["/workspace/source-repo"]}`),
	)
	req.SetPathValue("ws", "ALLAGENTS")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Error string `json:"error"`
		Kind  string `json:"kind"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Error != message || body.Kind != "conflict" {
		t.Fatalf("response = %#v, want conflict with actionable repository collision message", body)
	}
}

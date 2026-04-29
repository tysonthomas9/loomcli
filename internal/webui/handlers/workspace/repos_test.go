package workspace

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// addRepoRequest builds a POST request with workspace UUID set in context.
func addRepoRequest(wsID, body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/"+wsID+"/repos", strings.NewReader(body))
	ctx := middleware.WithWorkspace(req.Context(), wsID)
	return req.WithContext(ctx)
}

// removeRepoRequest builds a DELETE request with workspace UUID in context
// and the repo name as a path value.
func removeRepoRequest(wsID, repoName string) *http.Request {
	req := httptest.NewRequest(http.MethodDelete, "/api/workspaces/"+wsID+"/repos/"+repoName, nil)
	ctx := middleware.WithWorkspace(req.Context(), wsID)
	req = req.WithContext(ctx)
	req.SetPathValue("repo", repoName)
	return req
}

// --- HandleAddRepo ---

func TestHandleAddRepo_Success(t *testing.T) {
	svc := &mockWorkspaceService{
		addWorkspaceRepoFn: func(_ context.Context, wsID string, params service.AddRepoParams) (*ops.WorkspaceData, error) {
			if wsID != "ws-uuid-1" {
				t.Errorf("wsID = %q, want ws-uuid-1", wsID)
			}
			if params.Name != "newrepo" {
				t.Errorf("name = %q, want newrepo", params.Name)
			}
			if params.Path != "/abs/path" {
				t.Errorf("path = %q, want /abs/path", params.Path)
			}
			if params.DefaultBranch != "main" {
				t.Errorf("default_branch = %q", params.DefaultBranch)
			}
			if params.Remote != "origin" {
				t.Errorf("remote = %q", params.Remote)
			}
			if len(params.Groups) != 1 || params.Groups[0] != "backend" {
				t.Errorf("groups = %v", params.Groups)
			}
			return &ops.WorkspaceData{Name: "alpha"}, nil
		},
	}
	handler := handleAddRepo(svc)

	body := `{"name":"newrepo","path":"/abs/path","default_branch":"main","remote":"origin","groups":["backend"]}`
	req := addRepoRequest("ws-uuid-1", body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp WorkspaceResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Success {
		t.Fatalf("expected success: %s", resp.Error)
	}
}

func TestHandleAddRepo_MissingWorkspaceID(t *testing.T) {
	handler := handleAddRepo(&mockWorkspaceService{})
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/repos", strings.NewReader(`{"path":"/p"}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestHandleAddRepo_MissingPath(t *testing.T) {
	handler := handleAddRepo(&mockWorkspaceService{})
	req := addRepoRequest("ws-1", `{"name":"foo"}`)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
	var resp WorkspaceResponse
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if !strings.Contains(resp.Error, "path") {
		t.Errorf("expected path-required error, got %q", resp.Error)
	}
}

func TestHandleAddRepo_MalformedJSON(t *testing.T) {
	handler := handleAddRepo(&mockWorkspaceService{})
	req := addRepoRequest("ws-1", `{not json}`)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestHandleAddRepo_UnknownWorkspace(t *testing.T) {
	svc := &mockWorkspaceService{
		addWorkspaceRepoFn: func(_ context.Context, _ string, _ service.AddRepoParams) (*ops.WorkspaceData, error) {
			return nil, service.ErrNotFound("workspace not found")
		},
	}
	handler := handleAddRepo(svc)
	req := addRepoRequest("ws-unknown", `{"path":"/p"}`)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestHandleAddRepo_DuplicateConflict(t *testing.T) {
	svc := &mockWorkspaceService{
		addWorkspaceRepoFn: func(_ context.Context, _ string, _ service.AddRepoParams) (*ops.WorkspaceData, error) {
			return nil, service.ErrConflict("repo already exists")
		},
	}
	handler := handleAddRepo(svc)
	req := addRepoRequest("ws-1", `{"path":"/p","name":"dupe"}`)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", rec.Code)
	}
}

func TestHandleAddRepo_InvalidPathValidation(t *testing.T) {
	svc := &mockWorkspaceService{
		addWorkspaceRepoFn: func(_ context.Context, _ string, _ service.AddRepoParams) (*ops.WorkspaceData, error) {
			return nil, service.ErrValidation("repo path does not exist")
		},
	}
	handler := handleAddRepo(svc)
	req := addRepoRequest("ws-1", `{"path":"/nonexistent"}`)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

// --- HandleRemoveRepo ---

func TestHandleRemoveRepo_Success(t *testing.T) {
	svc := &mockWorkspaceService{
		removeWorkspaceRepoFn: func(_ context.Context, wsID string, repoName string) (*ops.WorkspaceData, error) {
			if wsID != "ws-1" {
				t.Errorf("wsID = %q", wsID)
			}
			if repoName != "myrepo" {
				t.Errorf("repoName = %q", repoName)
			}
			return &ops.WorkspaceData{Name: "alpha"}, nil
		},
	}
	handler := handleRemoveRepo(svc)
	req := removeRepoRequest("ws-1", "myrepo")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleRemoveRepo_MissingWorkspaceID(t *testing.T) {
	handler := handleRemoveRepo(&mockWorkspaceService{})
	req := httptest.NewRequest(http.MethodDelete, "/api/workspaces/repos/x", nil)
	req.SetPathValue("repo", "x")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestHandleRemoveRepo_MissingRepoName(t *testing.T) {
	handler := handleRemoveRepo(&mockWorkspaceService{})
	req := httptest.NewRequest(http.MethodDelete, "/api/workspaces/ws-1/repos/", nil)
	ctx := middleware.WithWorkspace(req.Context(), "ws-1")
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestHandleRemoveRepo_UnknownRepo(t *testing.T) {
	svc := &mockWorkspaceService{
		removeWorkspaceRepoFn: func(_ context.Context, _ string, _ string) (*ops.WorkspaceData, error) {
			return nil, service.ErrNotFound("repo not found")
		},
	}
	handler := handleRemoveRepo(svc)
	req := removeRepoRequest("ws-1", "missing")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

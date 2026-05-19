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

func TestWorkspaceRepoHandlers(t *testing.T) {
	repos := []ops.WorkspaceRepo{{Name: "api", Path: "/repo/api", DefaultBranch: "main"}}
	svc := &mockWorkspaceService{
		getWorkspaceFn: func(_ context.Context, wsID string) (*ops.WorkspaceData, error) {
			if wsID != "WS" {
				t.Fatalf("GetWorkspace wsID = %q, want WS", wsID)
			}
			return &ops.WorkspaceData{Name: "Workspace", Repos: repos}, nil
		},
		addWorkspaceReposFn: func(_ context.Context, req service.WorkspaceAddReposRequest) (*ops.WorkspaceData, error) {
			if req.WorkspaceID != "WS" || len(req.Repos) != 1 || req.Repos[0] != "/new/repo" || req.Branch != "main" {
				t.Fatalf("AddWorkspaceRepos req = %+v", req)
			}
			return &ops.WorkspaceData{Name: "Workspace", Repos: repos}, nil
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/WS/repos", nil)
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "WS"))
	rec := httptest.NewRecorder()
	HandleListWorkspaceRepos(svc).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", rec.Code, rec.Body.String())
	}
	var listResp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if listResp["success"] != true {
		t.Fatalf("list response = %#v", listResp)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/workspaces/WS/repos", strings.NewReader(`{"repos":["/new/repo"],"branch":"main"}`))
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "WS"))
	rec = httptest.NewRecorder()
	HandleAddWorkspaceRepos(svc).ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add status=%d body=%s", rec.Code, rec.Body.String())
	}
	var addResp WorkspaceResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &addResp); err != nil {
		t.Fatalf("decode add: %v", err)
	}
	if !addResp.Success || addResp.Data == nil || addResp.Data.Name != "Workspace" {
		t.Fatalf("add response = %+v", addResp)
	}
}

func TestWorkspaceRepoHandlersValidationAndServiceErrors(t *testing.T) {
	svc := &mockWorkspaceService{
		getWorkspaceFn: func(context.Context, string) (*ops.WorkspaceData, error) {
			return nil, service.ErrNotFound("workspace not found")
		},
		addWorkspaceReposFn: func(context.Context, service.WorkspaceAddReposRequest) (*ops.WorkspaceData, error) {
			return nil, service.ErrValidation("bad repo")
		},
	}

	rec := httptest.NewRecorder()
	HandleListWorkspaceRepos(svc).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/workspaces//repos", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing list ws status=%d body=%s", rec.Code, rec.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/WS/repos", nil)
	req.SetPathValue("ws", "WS")
	rec = httptest.NewRecorder()
	HandleListWorkspaceRepos(svc).ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("list service error status=%d body=%s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	HandleAddWorkspaceRepos(svc).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/workspaces//repos", strings.NewReader(`{}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing add ws status=%d body=%s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/api/workspaces/WS/repos", strings.NewReader(`{bad`))
	req.SetPathValue("ws", "WS")
	rec = httptest.NewRecorder()
	HandleAddWorkspaceRepos(svc).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad json status=%d body=%s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/api/workspaces/WS/repos", strings.NewReader(`{"repos":["/bad"]}`))
	req.SetPathValue("ws", "WS")
	rec = httptest.NewRecorder()
	HandleAddWorkspaceRepos(svc).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("add service error status=%d body=%s", rec.Code, rec.Body.String())
	}
}

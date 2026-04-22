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

// repoBranchPatchRequest creates a PATCH request with workspace UUID in context
// and the repo name set as a path value.
func repoBranchPatchRequest(wsID, repoName, body string) *http.Request {
	req := httptest.NewRequest(http.MethodPatch, "/api/workspaces/"+wsID+"/repos/"+repoName+"/default-branch", strings.NewReader(body))
	ctx := middleware.WithWorkspace(req.Context(), wsID)
	req = req.WithContext(ctx)
	req.SetPathValue("repo", repoName)
	return req
}

func TestHandleRepoDefaultBranchPatch_Success(t *testing.T) {
	svc := &mockWorkspaceService{
		patchRepoDefaultBranchFn: func(_ context.Context, wsID, repoName, branch string) (*ops.WorkspaceData, error) {
			if wsID != "ws-uuid-1" {
				t.Errorf("expected wsID %q, got %q", "ws-uuid-1", wsID)
			}
			if repoName != "my-repo" {
				t.Errorf("expected repoName %q, got %q", "my-repo", repoName)
			}
			if branch != "main" {
				t.Errorf("expected branch %q, got %q", "main", branch)
			}
			return &ops.WorkspaceData{Name: "my-ws"}, nil
		},
	}
	handler := handleRepoDefaultBranchPatch(svc)

	req := repoBranchPatchRequest("ws-uuid-1", "my-repo", `{"branch":"main"}`)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp WorkspaceResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Success {
		t.Fatalf("expected success, got error: %s", resp.Error)
	}
}

func TestHandleRepoDefaultBranchPatch_MissingWorkspaceID(t *testing.T) {
	svc := &mockWorkspaceService{}
	handler := handleRepoDefaultBranchPatch(svc)

	// No workspace ID in context
	body := strings.NewReader(`{"branch":"main"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/workspaces/repos/my-repo/default-branch", body)
	req.SetPathValue("repo", "my-repo")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleRepoDefaultBranchPatch_MissingRepoName(t *testing.T) {
	svc := &mockWorkspaceService{}
	handler := handleRepoDefaultBranchPatch(svc)

	// No repo path value
	body := strings.NewReader(`{"branch":"main"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/workspaces/ws-uuid-1/repos//default-branch", body)
	ctx := middleware.WithWorkspace(req.Context(), "ws-uuid-1")
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleRepoDefaultBranchPatch_EmptyBranch(t *testing.T) {
	svc := &mockWorkspaceService{}
	handler := handleRepoDefaultBranchPatch(svc)

	req := repoBranchPatchRequest("ws-uuid-1", "my-repo", `{"branch":""}`)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp WorkspaceResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Success {
		t.Fatal("expected failure")
	}
	if !strings.Contains(resp.Error, "branch is required") {
		t.Errorf("expected 'branch is required' in error, got: %s", resp.Error)
	}
}

func TestHandleRepoDefaultBranchPatch_InvalidBranch(t *testing.T) {
	svc := &mockWorkspaceService{}
	handler := handleRepoDefaultBranchPatch(svc)

	req := repoBranchPatchRequest("ws-uuid-1", "my-repo", `{"branch":"--evil"}`)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp WorkspaceResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Success {
		t.Fatal("expected failure")
	}
	if !strings.Contains(resp.Error, "invalid branch") {
		t.Errorf("expected 'invalid branch' in error, got: %s", resp.Error)
	}
}

func TestHandleRepoDefaultBranchPatch_BranchWithDotDot(t *testing.T) {
	svc := &mockWorkspaceService{}
	handler := handleRepoDefaultBranchPatch(svc)

	req := repoBranchPatchRequest("ws-uuid-1", "my-repo", `{"branch":"foo..bar"}`)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleRepoDefaultBranchPatch_RejectsGitInvalidShapes(t *testing.T) {
	svc := &mockWorkspaceService{
		patchRepoDefaultBranchFn: func(_ context.Context, _, _, _ string) (*ops.WorkspaceData, error) {
			t.Fatal("service must not be called for invalid branch shapes")
			return nil, nil
		},
	}
	handler := handleRepoDefaultBranchPatch(svc)

	for _, tc := range []struct {
		name   string
		branch string
	}{
		{"trailing-slash", "main/"},
		{"trailing-dot", "main."},
		{"lock-suffix", "main.lock"},
		{"double-slash", "feat//x"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"branch":"` + tc.branch + `"}`
			req := repoBranchPatchRequest("ws-uuid-1", "my-repo", body)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400 for branch=%q, got %d: %s", tc.branch, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestHandleRepoDefaultBranchPatch_MalformedJSON(t *testing.T) {
	svc := &mockWorkspaceService{}
	handler := handleRepoDefaultBranchPatch(svc)

	req := repoBranchPatchRequest("ws-uuid-1", "my-repo", `{invalid json}`)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleRepoDefaultBranchPatch_UnknownWorkspace(t *testing.T) {
	svc := &mockWorkspaceService{
		patchRepoDefaultBranchFn: func(_ context.Context, _, _, _ string) (*ops.WorkspaceData, error) {
			return nil, service.ErrNotFound("workspace with ID nonexistent-uuid not found")
		},
	}
	handler := handleRepoDefaultBranchPatch(svc)

	req := repoBranchPatchRequest("nonexistent-uuid", "my-repo", `{"branch":"main"}`)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp WorkspaceResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if !strings.Contains(resp.Error, "not found") {
		t.Errorf("expected 'not found' in error, got: %s", resp.Error)
	}
}

func TestHandleRepoDefaultBranchPatch_UnknownRepo(t *testing.T) {
	svc := &mockWorkspaceService{
		patchRepoDefaultBranchFn: func(_ context.Context, _, _, _ string) (*ops.WorkspaceData, error) {
			return nil, service.ErrNotFound("repo missing-repo not found in workspace ws-uuid-1")
		},
	}
	handler := handleRepoDefaultBranchPatch(svc)

	req := repoBranchPatchRequest("ws-uuid-1", "missing-repo", `{"branch":"main"}`)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp WorkspaceResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if !strings.Contains(resp.Error, "not found") {
		t.Errorf("expected 'not found' in error, got: %s", resp.Error)
	}
}

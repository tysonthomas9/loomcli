package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/workspace"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// TestWorkspaceCreateE2E_ErrorCodes exercises the full handler chain for
// workspace creation error handling via the mock service.
func TestWorkspaceCreateE2E_ErrorCodes(t *testing.T) {
	tests := []struct {
		name         string
		body         string
		svcErr       error
		wantStatus   int
		wantContains string
	}{
		{
			name:         "conflict returns 409",
			body:         `{"name":"my-dup","type":"empty","repos":["/home/user/repo"]}`,
			svcErr:       service.ErrConflict("workspace 'my-dup' already exists"),
			wantStatus:   http.StatusConflict,
			wantContains: "my-dup",
		},
		{
			name:         "validation error returns 400",
			body:         `{"name":"bad-path","type":"empty","repos":["/nonexistent/fake/dir"]}`,
			svcErr:       service.ErrValidation("repo path does not exist: /nonexistent/fake/dir"),
			wantStatus:   http.StatusBadRequest,
			wantContains: "/nonexistent/fake/dir",
		},
		{
			name:         "forbidden returns 403",
			body:         `{"name":"escape","type":"empty","repos":["/a"]}`,
			svcErr:       service.ErrForbidden("path traversal detected"),
			wantStatus:   http.StatusForbidden,
			wantContains: "path traversal",
		},
		{
			name:         "internal error returns 500",
			body:         `{"name":"boom","type":"empty","repos":["/a"]}`,
			svcErr:       service.ErrInternal("failed to create workspace", nil),
			wantStatus:   http.StatusInternalServerError,
			wantContains: "failed to create workspace",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &mockWorkspaceService{
				startAsyncCreateFn: func(_ context.Context, _ service.WorkspaceCreateRequest) (string, error) {
					return "", service.ErrUnavailable("not available")
				},
				createWorkspaceFn: func(_ context.Context, _ service.WorkspaceCreateRequest) (*ops.WorkspaceData, []string, error) {
					return nil, nil, tt.svcErr
				},
			}
			handler := workspace.HandleWorkspaceCreate(svc)

			req := httptest.NewRequest(http.MethodPost, "/api/workspaces", strings.NewReader(tt.body))
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("expected status %d, got %d: %s", tt.wantStatus, rec.Code, rec.Body.String())
			}

			var resp workspace.WorkspaceResponse
			if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if resp.Success {
				t.Fatal("expected success=false for error case")
			}
			if !strings.Contains(resp.Error, tt.wantContains) {
				t.Errorf("expected error to contain %q, got: %q", tt.wantContains, resp.Error)
			}
		})
	}
}

// TestWorkspaceCreateE2E_CloneAsync verifies async creation path with mock service.
func TestWorkspaceCreateE2E_CloneAsync(t *testing.T) {
	svc := &mockWorkspaceService{
		startAsyncCreateFn: func(_ context.Context, req service.WorkspaceCreateRequest) (string, error) {
			return "job-e2e-123", nil
		},
	}

	handler := workspace.HandleWorkspaceCreate(svc)

	body := strings.NewReader(`{"name":"async-e2e","type":"clone","clone_urls":["https://github.com/user/repo.git"]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces", body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", rec.Code, rec.Body.String())
	}

	var createResp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&createResp); err != nil {
		t.Fatalf("decode 202 response: %v", err)
	}
	if createResp["success"] != true {
		t.Fatalf("expected success=true in 202, got %v", createResp["success"])
	}
	jobID, ok := createResp["job_id"].(string)
	if !ok || jobID == "" {
		t.Fatalf("expected non-empty job_id in 202 response, got %v", createResp["job_id"])
	}
}

// TestWorkspaceCreateE2E_CloneAsyncUnavailable verifies clone reports async
// unavailability instead of falling back to a synchronous clone path.
func TestWorkspaceCreateE2E_CloneAsyncUnavailable(t *testing.T) {
	createCalled := false
	svc := &mockWorkspaceService{
		startAsyncCreateFn: func(_ context.Context, _ service.WorkspaceCreateRequest) (string, error) {
			return "", service.ErrUnavailable("async not available")
		},
		createWorkspaceFn: func(_ context.Context, _ service.WorkspaceCreateRequest) (*ops.WorkspaceData, []string, error) {
			createCalled = true
			return &ops.WorkspaceData{Name: "sync-e2e"}, nil, nil
		},
	}

	handler := workspace.HandleWorkspaceCreate(svc)

	body := strings.NewReader(`{"name":"sync-e2e","type":"clone","clone_urls":["https://github.com/user/repo.git"]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces", body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rec.Code, rec.Body.String())
	}
	if createCalled {
		t.Error("createFn should not be called as a sync clone fallback")
	}
}

// TestWorkspaceCreateE2E_EmptySuccess verifies empty workspace creation.
func TestWorkspaceCreateE2E_EmptySuccess(t *testing.T) {
	svc := &mockWorkspaceService{
		startAsyncCreateFn: func(_ context.Context, _ service.WorkspaceCreateRequest) (string, error) {
			return "", service.ErrUnavailable("not available")
		},
		createWorkspaceFn: func(_ context.Context, req service.WorkspaceCreateRequest) (*ops.WorkspaceData, []string, error) {
			if req.Type != "empty" {
				t.Errorf("expected type %q, got %q", "empty", req.Type)
			}
			return &ops.WorkspaceData{Name: "test-ws"}, nil, nil
		},
	}

	handler := workspace.HandleWorkspaceCreate(svc)

	body := strings.NewReader(`{"name":"fresh-ws","type":"empty","repos":["/home/user/project"]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces", body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp workspace.WorkspaceResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Success {
		t.Fatalf("expected success=true, got error: %s", resp.Error)
	}
	if resp.Data == nil {
		t.Fatal("expected non-nil Data in 201 response")
	}
}

// TestWorkspaceCreateE2E_JobPollingUnknownID verifies 404 for unknown job.
func TestWorkspaceCreateE2E_JobPollingUnknownID(t *testing.T) {
	svc := &mockWorkspaceService{
		getWorkspaceJobFn: func(_ context.Context, _ string) (*service.WorkspaceJob, error) {
			return nil, service.ErrNotFound("job not found")
		},
	}
	handler := workspace.HandleGetWorkspaceJob(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/jobs/does-not-exist", nil)
	req.SetPathValue("id", "does-not-exist")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestWorkspaceCreateE2E_JobPollingCompleted verifies completed job polling.
func TestWorkspaceCreateE2E_JobPollingCompleted(t *testing.T) {
	svc := &mockWorkspaceService{
		getWorkspaceJobFn: func(_ context.Context, jobID string) (*service.WorkspaceJob, error) {
			return &service.WorkspaceJob{
				ID:          jobID,
				Status:      service.JobStatusDone,
				WorkspaceID: "ws-poll-done",
			}, nil
		},
	}
	handler := workspace.HandleGetWorkspaceJob(svc)
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/jobs/job-123", nil)
	req.SetPathValue("id", "job-123")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["status"] != string(service.JobStatusDone) {
		t.Errorf("expected status %q, got %v", service.JobStatusDone, resp["status"])
	}
	if resp["workspace_id"] != "ws-poll-done" {
		t.Errorf("expected workspace_id %q, got %v", "ws-poll-done", resp["workspace_id"])
	}
}

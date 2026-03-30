package webui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/workspaceerrors"
)

// TestWorkspaceCreateE2E_ErrorCodes exercises the full handler chain for
// workspace creation error handling, verifying that typed errors from createFn
// produce the correct HTTP status codes and user-facing messages.
func TestWorkspaceCreateE2E_ErrorCodes(t *testing.T) {
	tests := []struct {
		name         string
		body         string
		createErr    error
		wantStatus   int
		wantContains string // substring expected in the error message
	}{
		{
			name: "duplicate workspace returns 409 with workspace name",
			body: `{"name":"my-dup","type":"empty","repos":["/home/user/repo"]}`,
			createErr: workspaceerrors.New(
				workspaceerrors.AlreadyExists,
				"workspace 'my-dup' already exists at /tmp/workspaces/my-dup",
				nil,
			),
			wantStatus:   http.StatusConflict,
			wantContains: "my-dup",
		},
		{
			name: "invalid repo path returns 422 with bad path",
			body: `{"name":"bad-path","type":"empty","repos":["/nonexistent/fake/dir"]}`,
			createErr: workspaceerrors.New(
				workspaceerrors.PathNotFound,
				"repo path does not exist: /nonexistent/fake/dir",
				nil,
			),
			wantStatus:   http.StatusUnprocessableEntity,
			wantContains: "/nonexistent/fake/dir",
		},
		{
			name: "not a git repo returns 422 with git message",
			body: `{"name":"not-git","type":"empty","repos":["/tmp/plain-dir"]}`,
			createErr: workspaceerrors.New(
				workspaceerrors.NotGitRepo,
				"/tmp/plain-dir is not a git repository",
				nil,
			),
			wantStatus:   http.StatusUnprocessableEntity,
			wantContains: "not a git repository",
		},
		{
			name: "security violation returns 403",
			body: `{"name":"escape","type":"empty","repos":["/a"]}`,
			createErr: workspaceerrors.New(
				workspaceerrors.SecurityViolation,
				"path traversal detected in workspace path",
				nil,
			),
			wantStatus:   http.StatusForbidden,
			wantContains: "path traversal",
		},
		{
			name:         "unknown error returns 500 generic message",
			body:         `{"name":"boom","type":"empty","repos":["/a"]}`,
			createErr:    fmt.Errorf("disk full"),
			wantStatus:   http.StatusInternalServerError,
			wantContains: "failed to create workspace",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			createFn := func(ctx context.Context, req WorkspaceCreateRequest) (WorkspaceCreateResult, error) {
				return WorkspaceCreateResult{}, tt.createErr
			}
			handler := handleWorkspaceCreate(createFn, nil, nil)

			req := httptest.NewRequest(http.MethodPost, "/api/workspaces", strings.NewReader(tt.body))
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("expected status %d, got %d: %s", tt.wantStatus, rec.Code, rec.Body.String())
			}

			var resp workspaceResponse
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

// TestWorkspaceCreateE2E_CloneAsync verifies that clone-type creation with a
// jobStore returns 202 Accepted with a job_id, and that the job can be polled
// to completion.
func TestWorkspaceCreateE2E_CloneAsync(t *testing.T) {
	store := NewWorkspaceJobStore()
	defer store.Stop()

	done := make(chan struct{})
	createFn := func(ctx context.Context, req WorkspaceCreateRequest) (WorkspaceCreateResult, error) {
		defer close(done)
		return WorkspaceCreateResult{WorkspaceID: "ws-e2e-async"}, nil
	}

	handler := handleWorkspaceCreate(createFn, nil, store)

	body := strings.NewReader(`{"name":"async-e2e","type":"clone","clone_url":"https://github.com/user/repo.git"}`)
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

	// Wait for the background job to complete.
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for async job to complete")
	}
	// Brief settle for sync.Map store to propagate.
	time.Sleep(20 * time.Millisecond)

	// Poll the job and verify completed state.
	jobHandler := handleGetWorkspaceJob(store)
	pollReq := httptest.NewRequest(http.MethodGet, "/api/workspaces/jobs/"+jobID, nil)
	pollReq.SetPathValue("id", jobID)
	pollRec := httptest.NewRecorder()
	jobHandler.ServeHTTP(pollRec, pollReq)

	if pollRec.Code != http.StatusOK {
		t.Fatalf("expected 200 for completed job, got %d: %s", pollRec.Code, pollRec.Body.String())
	}

	var jobResp map[string]any
	if err := json.NewDecoder(pollRec.Body).Decode(&jobResp); err != nil {
		t.Fatalf("decode job response: %v", err)
	}
	if jobResp["status"] != string(JobStatusDone) {
		t.Errorf("expected job status %q, got %v", JobStatusDone, jobResp["status"])
	}
	if jobResp["workspace_id"] != "ws-e2e-async" {
		t.Errorf("expected workspace_id %q, got %v", "ws-e2e-async", jobResp["workspace_id"])
	}
}

// TestWorkspaceCreateE2E_CloneNilJobStoreSync verifies that clone-type
// creation without a jobStore falls back to synchronous 201.
func TestWorkspaceCreateE2E_CloneNilJobStoreSync(t *testing.T) {
	createCalled := false
	createFn := func(ctx context.Context, req WorkspaceCreateRequest) (WorkspaceCreateResult, error) {
		createCalled = true
		return WorkspaceCreateResult{WorkspaceID: "ws-sync"}, nil
	}

	// nil jobStore => sync fallback
	handler := handleWorkspaceCreate(createFn, nil, nil)

	body := strings.NewReader(`{"name":"sync-e2e","type":"clone","clone_url":"https://github.com/user/repo.git"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces", body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	if !createCalled {
		t.Error("expected createFn to be called synchronously")
	}

	var resp workspaceResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Success {
		t.Fatalf("expected success=true, got error: %s", resp.Error)
	}
}

// TestWorkspaceCreateE2E_EmptySuccess verifies that an empty workspace
// creation returns 201 with WorkspaceData when workspaceConfigFn is provided.
func TestWorkspaceCreateE2E_EmptySuccess(t *testing.T) {
	createFn := func(ctx context.Context, req WorkspaceCreateRequest) (WorkspaceCreateResult, error) {
		if req.Type != "empty" {
			t.Errorf("expected type %q, got %q", "empty", req.Type)
		}
		if req.Name != "fresh-ws" {
			t.Errorf("expected name %q, got %q", "fresh-ws", req.Name)
		}
		return WorkspaceCreateResult{}, nil
	}

	handler := handleWorkspaceCreate(createFn, mockWorkspaceConfigFn, nil)

	body := strings.NewReader(`{"name":"fresh-ws","type":"empty","repos":["/home/user/project"]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces", body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp workspaceResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Success {
		t.Fatalf("expected success=true, got error: %s", resp.Error)
	}
	if resp.Data == nil {
		t.Fatal("expected non-nil Data in 201 response")
	}
	if resp.Data.Name != "test-ws" {
		t.Errorf("expected data.name=%q (from mockWorkspaceConfigFn), got %q", "test-ws", resp.Data.Name)
	}
}

// TestWorkspaceCreateE2E_JobPollingUnknownID verifies that polling a
// non-existent job ID returns 404.
func TestWorkspaceCreateE2E_JobPollingUnknownID(t *testing.T) {
	store := NewWorkspaceJobStore()
	defer store.Stop()

	handler := handleGetWorkspaceJob(store)

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/jobs/does-not-exist", nil)
	req.SetPathValue("id", "does-not-exist")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["error"] != "job not found" {
		t.Errorf("expected error %q, got %v", "job not found", resp["error"])
	}
}

// TestWorkspaceCreateE2E_JobPollingCompleted verifies that polling a completed
// job returns 200 with workspace_id populated.
func TestWorkspaceCreateE2E_JobPollingCompleted(t *testing.T) {
	store := NewWorkspaceJobStore()
	defer store.Stop()

	done := make(chan struct{})
	createFn := func(ctx context.Context, req WorkspaceCreateRequest) (WorkspaceCreateResult, error) {
		defer close(done)
		return WorkspaceCreateResult{WorkspaceID: "ws-poll-done"}, nil
	}

	jobID := store.Start(WorkspaceCreateRequest{Name: "poll-test", Type: "clone"}, createFn)

	// Wait for job to complete.
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for job to complete")
	}
	time.Sleep(20 * time.Millisecond)

	handler := handleGetWorkspaceJob(store)
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/jobs/"+jobID, nil)
	req.SetPathValue("id", jobID)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["status"] != string(JobStatusDone) {
		t.Errorf("expected status %q, got %v", JobStatusDone, resp["status"])
	}
	if resp["workspace_id"] != "ws-poll-done" {
		t.Errorf("expected workspace_id %q, got %v", "ws-poll-done", resp["workspace_id"])
	}
}

package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

func TestHandleGetWorkspaceJob_NotFound(t *testing.T) {
	svc := &mockWorkspaceService{
		getWorkspaceJobFn: func(_ context.Context, _ string) (*service.WorkspaceJob, error) {
			return nil, service.ErrNotFound("job not found")
		},
	}

	handler := handleGetWorkspaceJob(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/jobs/nonexistent", nil)
	req.SetPathValue("id", "nonexistent")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleGetWorkspaceJob_MissingID(t *testing.T) {
	svc := &mockWorkspaceService{}
	handler := handleGetWorkspaceJob(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/jobs/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleGetWorkspaceJob_RunningJob(t *testing.T) {
	svc := &mockWorkspaceService{
		getWorkspaceJobFn: func(_ context.Context, jobID string) (*service.WorkspaceJob, error) {
			return &service.WorkspaceJob{
				ID:       jobID,
				Status:   service.JobStatusRunning,
				Progress: "cloning repository...",
			}, nil
		},
	}

	handler := handleGetWorkspaceJob(svc)
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/jobs/job-123", nil)
	req.SetPathValue("id", "job-123")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp["status"] != string(service.JobStatusRunning) {
		t.Errorf("expected status %q, got %v", service.JobStatusRunning, resp["status"])
	}
}

func TestHandleGetWorkspaceJob_CompletedJob(t *testing.T) {
	svc := &mockWorkspaceService{
		getWorkspaceJobFn: func(_ context.Context, jobID string) (*service.WorkspaceJob, error) {
			return &service.WorkspaceJob{
				ID:          jobID,
				Status:      service.JobStatusDone,
				WorkspaceID: "ws-done",
			}, nil
		},
	}

	handler := handleGetWorkspaceJob(svc)
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/jobs/job-done", nil)
	req.SetPathValue("id", "job-done")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp["status"] != string(service.JobStatusDone) {
		t.Errorf("expected status %q, got %v", service.JobStatusDone, resp["status"])
	}
	if resp["workspace_id"] != "ws-done" {
		t.Errorf("expected workspace_id %q, got %v", "ws-done", resp["workspace_id"])
	}
}

func TestHandleGetWorkspaceJob_FailedJob(t *testing.T) {
	svc := &mockWorkspaceService{
		getWorkspaceJobFn: func(_ context.Context, jobID string) (*service.WorkspaceJob, error) {
			return &service.WorkspaceJob{
				ID:     jobID,
				Status: service.JobStatusFailed,
				Error:  "workspace creation failed",
			}, nil
		},
	}

	handler := handleGetWorkspaceJob(svc)
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/jobs/job-fail", nil)
	req.SetPathValue("id", "job-fail")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp["status"] != string(service.JobStatusFailed) {
		t.Errorf("expected status %q, got %v", service.JobStatusFailed, resp["status"])
	}
	if resp["error"] != "workspace creation failed" {
		t.Errorf("expected error %q, got %v", "workspace creation failed", resp["error"])
	}
}

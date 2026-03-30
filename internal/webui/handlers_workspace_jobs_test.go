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
)

func TestHandleGetWorkspaceJob_NotFound(t *testing.T) {
	store := NewWorkspaceJobStore()
	defer store.Stop()

	handler := handleGetWorkspaceJob(store)

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/jobs/nonexistent", nil)
	req.SetPathValue("id", "nonexistent")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp["error"] != "job not found" {
		t.Errorf("expected 'job not found' error, got: %v", resp["error"])
	}
}

func TestHandleGetWorkspaceJob_NilStore(t *testing.T) {
	handler := handleGetWorkspaceJob(nil)

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/jobs/some-id", nil)
	req.SetPathValue("id", "some-id")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for nil store, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleGetWorkspaceJob_MissingID(t *testing.T) {
	store := NewWorkspaceJobStore()
	defer store.Stop()

	handler := handleGetWorkspaceJob(store)

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/jobs/", nil)
	// Do not set path value — simulates missing {id}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleGetWorkspaceJob_RunningJob(t *testing.T) {
	store := NewWorkspaceJobStore()
	defer store.Stop()

	started := make(chan struct{})
	proceed := make(chan struct{})

	createFn := func(ctx context.Context, req WorkspaceCreateRequest) (WorkspaceCreateResult, error) {
		close(started)
		<-proceed
		return WorkspaceCreateResult{WorkspaceID: "ws-123"}, nil
	}

	id := store.Start(WorkspaceCreateRequest{Name: "test", Type: "clone"}, createFn)
	<-started

	handler := handleGetWorkspaceJob(store)
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/jobs/"+id, nil)
	req.SetPathValue("id", id)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp["status"] != string(JobStatusRunning) {
		t.Errorf("expected status %q, got %v", JobStatusRunning, resp["status"])
	}

	close(proceed)
}

func TestHandleGetWorkspaceJob_CompletedJob(t *testing.T) {
	store := NewWorkspaceJobStore()
	defer store.Stop()

	done := make(chan struct{})
	createFn := func(ctx context.Context, req WorkspaceCreateRequest) (WorkspaceCreateResult, error) {
		defer close(done)
		return WorkspaceCreateResult{WorkspaceID: "ws-done"}, nil
	}

	id := store.Start(WorkspaceCreateRequest{Name: "test", Type: "clone"}, createFn)
	<-done
	time.Sleep(10 * time.Millisecond)

	handler := handleGetWorkspaceJob(store)
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/jobs/"+id, nil)
	req.SetPathValue("id", id)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp["status"] != string(JobStatusDone) {
		t.Errorf("expected status %q, got %v", JobStatusDone, resp["status"])
	}
	if resp["workspace_id"] != "ws-done" {
		t.Errorf("expected workspace_id %q, got %v", "ws-done", resp["workspace_id"])
	}
}

func TestHandleGetWorkspaceJob_FailedJob(t *testing.T) {
	store := NewWorkspaceJobStore()
	defer store.Stop()

	done := make(chan struct{})
	createFn := func(ctx context.Context, req WorkspaceCreateRequest) (WorkspaceCreateResult, error) {
		defer close(done)
		return WorkspaceCreateResult{}, fmt.Errorf("clone failed")
	}

	id := store.Start(WorkspaceCreateRequest{Name: "test", Type: "clone"}, createFn)
	<-done
	time.Sleep(10 * time.Millisecond)

	handler := handleGetWorkspaceJob(store)
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/jobs/"+id, nil)
	req.SetPathValue("id", id)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp["status"] != string(JobStatusFailed) {
		t.Errorf("expected status %q, got %v", JobStatusFailed, resp["status"])
	}
	// Non-CreateError errors are sanitized to a generic message.
	if resp["error"] != "workspace creation failed" {
		t.Errorf("expected error %q, got %v", "workspace creation failed", resp["error"])
	}
}

func TestHandleWorkspaceCreate_CloneAsync202(t *testing.T) {
	store := NewWorkspaceJobStore()
	defer store.Stop()

	createFn := func(ctx context.Context, req WorkspaceCreateRequest) (WorkspaceCreateResult, error) {
		return WorkspaceCreateResult{WorkspaceID: "ws-async"}, nil
	}

	handler := handleWorkspaceCreate(createFn, nil, store)

	body := strings.NewReader(`{"name":"async-ws","type":"clone","clone_url":"https://github.com/user/repo.git"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces", body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["success"] != true {
		t.Fatalf("expected success=true, got %v", resp["success"])
	}
	jobID, ok := resp["job_id"].(string)
	if !ok || jobID == "" {
		t.Fatalf("expected non-empty job_id, got %v", resp["job_id"])
	}
}

func TestHandleWorkspaceCreate_CloneNilJobStoreFallsThrough(t *testing.T) {
	createCalled := false
	createFn := func(ctx context.Context, req WorkspaceCreateRequest) (WorkspaceCreateResult, error) {
		createCalled = true
		return WorkspaceCreateResult{WorkspaceID: "ws-sync"}, nil
	}

	// nil jobStore should fall through to synchronous path
	handler := handleWorkspaceCreate(createFn, nil, nil)

	body := strings.NewReader(`{"name":"sync-ws","type":"clone","clone_url":"https://github.com/user/repo.git"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces", body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 (sync fallback), got %d: %s", rec.Code, rec.Body.String())
	}
	if !createCalled {
		t.Error("expected createFn to be called synchronously")
	}
}

func TestHandleWorkspaceCreate_EmptyTypeNotAffectedByJobStore(t *testing.T) {
	store := NewWorkspaceJobStore()
	defer store.Stop()

	createCalled := false
	createFn := func(ctx context.Context, req WorkspaceCreateRequest) (WorkspaceCreateResult, error) {
		createCalled = true
		return WorkspaceCreateResult{}, nil
	}

	handler := handleWorkspaceCreate(createFn, nil, store)

	body := strings.NewReader(`{"name":"empty-ws","type":"empty","repos":["/a"]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces", body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Empty type should still return 201 synchronously, even with jobStore present.
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	if !createCalled {
		t.Error("expected createFn to be called synchronously for empty type")
	}
}

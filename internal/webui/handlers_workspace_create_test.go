package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

func TestHandleWorkspaceCreate_EmptyType_Success(t *testing.T) {
	createCalled := false
	svc := &mockWorkspaceService{
		createWorkspaceFn: func(_ context.Context, req service.WorkspaceCreateRequest) (*service.WorkspaceData, []string, error) {
			createCalled = true
			if req.Name != "my-ws" {
				t.Errorf("expected name %q, got %q", "my-ws", req.Name)
			}
			if req.Type != "empty" {
				t.Errorf("expected type %q, got %q", "empty", req.Type)
			}
			return &service.WorkspaceData{Name: "test-ws"}, nil, nil
		},
		startAsyncCreateFn: func(_ context.Context, _ service.WorkspaceCreateRequest) (string, error) {
			return "", service.ErrUnavailable("not available")
		},
	}

	handler := handleWorkspaceCreate(svc)

	body := strings.NewReader(`{"name":"my-ws","type":"empty","repos":["/home/user/repo"]}`)
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
		t.Fatalf("expected success, got error: %s", resp.Error)
	}
	if !createCalled {
		t.Error("expected createFn to be called")
	}
	if resp.Data == nil {
		t.Fatal("expected Data to be non-nil")
	}
	if resp.Data.Name != "test-ws" {
		t.Errorf("expected data name %q, got %q", "test-ws", resp.Data.Name)
	}
}

func TestHandleWorkspaceCreate_CloneType_Async(t *testing.T) {
	svc := &mockWorkspaceService{
		startAsyncCreateFn: func(_ context.Context, req service.WorkspaceCreateRequest) (string, error) {
			if req.Name != "cloned-ws" {
				t.Errorf("expected name %q, got %q", "cloned-ws", req.Name)
			}
			return "job-123", nil
		},
	}

	handler := handleWorkspaceCreate(svc)

	body := strings.NewReader(`{"name":"cloned-ws","type":"clone","clone_url":"https://github.com/user/repo.git","branch":"main"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces", body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp["job_id"] != "job-123" {
		t.Errorf("expected job_id %q, got %v", "job-123", resp["job_id"])
	}
}

func TestHandleWorkspaceCreate_CloneType_FallbackToSync(t *testing.T) {
	createCalled := false
	svc := &mockWorkspaceService{
		startAsyncCreateFn: func(_ context.Context, _ service.WorkspaceCreateRequest) (string, error) {
			return "", service.ErrUnavailable("async not available")
		},
		createWorkspaceFn: func(_ context.Context, _ service.WorkspaceCreateRequest) (*service.WorkspaceData, []string, error) {
			createCalled = true
			return &service.WorkspaceData{Name: "cloned-ws"}, nil, nil
		},
	}

	handler := handleWorkspaceCreate(svc)

	body := strings.NewReader(`{"name":"cloned-ws","type":"clone","clone_url":"https://github.com/user/repo.git"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces", body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	if !createCalled {
		t.Error("expected sync createFn to be called when async unavailable")
	}
}

func TestHandleWorkspaceCreate_ValidationError(t *testing.T) {
	svc := &mockWorkspaceService{
		startAsyncCreateFn: func(_ context.Context, _ service.WorkspaceCreateRequest) (string, error) {
			return "", service.ErrUnavailable("not available")
		},
		createWorkspaceFn: func(_ context.Context, _ service.WorkspaceCreateRequest) (*service.WorkspaceData, []string, error) {
			return nil, nil, service.ErrValidation("name is required")
		},
	}

	handler := handleWorkspaceCreate(svc)

	body := strings.NewReader(`{"name":"","type":"empty","repos":["/a"]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces", body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleWorkspaceCreate_ConflictError(t *testing.T) {
	svc := &mockWorkspaceService{
		startAsyncCreateFn: func(_ context.Context, _ service.WorkspaceCreateRequest) (string, error) {
			return "", service.ErrUnavailable("not available")
		},
		createWorkspaceFn: func(_ context.Context, _ service.WorkspaceCreateRequest) (*service.WorkspaceData, []string, error) {
			return nil, nil, service.ErrConflict("workspace already exists")
		},
	}

	handler := handleWorkspaceCreate(svc)

	body := strings.NewReader(`{"name":"existing","type":"empty","repos":["/a"]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces", body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleWorkspaceCreate_InvalidJSON(t *testing.T) {
	svc := &mockWorkspaceService{}
	handler := handleWorkspaceCreate(svc)

	body := strings.NewReader(`{invalid json}`)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces", body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleWorkspaceCreate_WithWarnings(t *testing.T) {
	svc := &mockWorkspaceService{
		startAsyncCreateFn: func(_ context.Context, _ service.WorkspaceCreateRequest) (string, error) {
			return "", service.ErrUnavailable("not available")
		},
		createWorkspaceFn: func(_ context.Context, _ service.WorkspaceCreateRequest) (*service.WorkspaceData, []string, error) {
			return &service.WorkspaceData{Name: "ws"}, []string{"warning 1", "warning 2"}, nil
		},
	}

	handler := handleWorkspaceCreate(svc)

	body := strings.NewReader(`{"name":"ws","type":"empty","repos":["/a"]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces", body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp workspaceResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if !resp.Success {
		t.Fatalf("expected success, got error: %s", resp.Error)
	}
	if len(resp.Warnings) != 2 {
		t.Errorf("expected 2 warnings, got %d", len(resp.Warnings))
	}
}

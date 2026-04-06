package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

func TestHandleSetDefaultWorkspace_Success(t *testing.T) {
	setCalled := false
	svc := &mockWorkspaceService{
		setDefaultWorkspaceFn: func(_ context.Context, name string) (*ops.WorkspaceData, error) {
			setCalled = true
			if name != "my-ws" {
				t.Errorf("expected set called with %q, got %q", "my-ws", name)
			}
			return &ops.WorkspaceData{Name: "test-ws"}, nil
		},
	}

	handler := handleSetDefaultWorkspace(svc)

	body := strings.NewReader(`{"name":"my-ws"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/workspaces/default", body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp workspaceResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Success {
		t.Fatalf("expected success, got error: %s", resp.Error)
	}
	if !setCalled {
		t.Error("expected setFn to be called")
	}
	if resp.Data == nil {
		t.Fatal("expected Data to be non-nil")
	}
	if resp.Data.Name != "test-ws" {
		t.Errorf("expected data name %q, got %q", "test-ws", resp.Data.Name)
	}
}

func TestHandleSetDefaultWorkspace_EmptyBody(t *testing.T) {
	svc := &mockWorkspaceService{}
	handler := handleSetDefaultWorkspace(svc)

	req := httptest.NewRequest(http.MethodPut, "/api/workspaces/default", strings.NewReader(""))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp workspaceResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Success {
		t.Fatal("expected failure")
	}
	if resp.Error != "invalid request body" {
		t.Errorf("expected error %q, got %q", "invalid request body", resp.Error)
	}
}

func TestHandleSetDefaultWorkspace_EmptyName(t *testing.T) {
	svc := &mockWorkspaceService{}
	handler := handleSetDefaultWorkspace(svc)

	body := strings.NewReader(`{"name":""}`)
	req := httptest.NewRequest(http.MethodPut, "/api/workspaces/default", body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp workspaceResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Success {
		t.Fatal("expected failure")
	}
	if resp.Error != "name is required" {
		t.Errorf("expected error %q, got %q", "name is required", resp.Error)
	}
}

func TestHandleSetDefaultWorkspace_Unavailable(t *testing.T) {
	svc := &mockWorkspaceService{
		setDefaultWorkspaceFn: func(_ context.Context, _ string) (*ops.WorkspaceData, error) {
			return nil, service.ErrUnavailable("set default workspace not available")
		},
	}
	handler := handleSetDefaultWorkspace(svc)

	body := strings.NewReader(`{"name":"my-ws"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/workspaces/default", body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleSetDefaultWorkspace_NotFound(t *testing.T) {
	svc := &mockWorkspaceService{
		setDefaultWorkspaceFn: func(_ context.Context, _ string) (*ops.WorkspaceData, error) {
			return nil, service.ErrNotFound("workspace not found")
		},
	}
	handler := handleSetDefaultWorkspace(svc)

	body := strings.NewReader(`{"name":"nonexistent"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/workspaces/default", body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleClearDefaultWorkspace_Success(t *testing.T) {
	clearCalled := false
	svc := &mockWorkspaceService{
		clearDefaultWorkspaceFn: func(_ context.Context) (*ops.WorkspaceData, error) {
			clearCalled = true
			return &ops.WorkspaceData{Name: "test-ws"}, nil
		},
	}

	handler := handleClearDefaultWorkspace(svc)

	req := httptest.NewRequest(http.MethodDelete, "/api/workspaces/default", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp workspaceResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Success {
		t.Fatalf("expected success, got error: %s", resp.Error)
	}
	if !clearCalled {
		t.Error("expected clearFn to be called")
	}
	if resp.Data == nil {
		t.Fatal("expected Data to be non-nil")
	}
}

func TestHandleClearDefaultWorkspace_Unavailable(t *testing.T) {
	svc := &mockWorkspaceService{
		clearDefaultWorkspaceFn: func(_ context.Context) (*ops.WorkspaceData, error) {
			return nil, service.ErrUnavailable("clear default workspace not available")
		},
	}
	handler := handleClearDefaultWorkspace(svc)

	req := httptest.NewRequest(http.MethodDelete, "/api/workspaces/default", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleClearDefaultWorkspace_Error(t *testing.T) {
	svc := &mockWorkspaceService{
		clearDefaultWorkspaceFn: func(_ context.Context) (*ops.WorkspaceData, error) {
			return nil, service.ErrInternal("config write failed", nil)
		},
	}
	handler := handleClearDefaultWorkspace(svc)

	req := httptest.NewRequest(http.MethodDelete, "/api/workspaces/default", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
}

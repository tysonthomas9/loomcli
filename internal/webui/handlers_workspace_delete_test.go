package webui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandleWorkspaceDelete_Success(t *testing.T) {
	deleteCalled := false
	deleteFn := func(name string) error {
		deleteCalled = true
		if name != "my-ws" {
			t.Errorf("expected delete called with %q, got %q", "my-ws", name)
		}
		return nil
	}

	handler := handleWorkspaceDelete(deleteFn, mockWorkspaceConfigFn, nil, nil)

	req := httptest.NewRequest(http.MethodDelete, "/api/workspace/{name}", nil)
	req.SetPathValue("name", "my-ws")
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
	if !deleteCalled {
		t.Error("expected deleteFn to be called")
	}
	if resp.Data == nil {
		t.Fatal("expected Data to be non-nil when workspaceConfigFn provided")
	}
	if resp.Data.Name != "test-ws" {
		t.Errorf("expected data name %q, got %q", "test-ws", resp.Data.Name)
	}
}

func TestHandleWorkspaceDelete_SuccessNilWorkspaceConfigFn(t *testing.T) {
	deleteFn := func(name string) error {
		return nil
	}

	handler := handleWorkspaceDelete(deleteFn, nil, nil, nil)

	req := httptest.NewRequest(http.MethodDelete, "/api/workspace/{name}", nil)
	req.SetPathValue("name", "my-ws")
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
	if resp.Data != nil {
		t.Error("expected Data to be nil when workspaceConfigFn is nil")
	}
}

func TestHandleWorkspaceDelete_MissingName(t *testing.T) {
	deleteFn := func(name string) error {
		t.Fatal("deleteFn should not be called when name is missing")
		return nil
	}

	handler := handleWorkspaceDelete(deleteFn, nil, nil, nil)

	req := httptest.NewRequest(http.MethodDelete, "/api/workspace/", nil)
	// Do not call SetPathValue — name is empty
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
	if resp.Error != "workspace name is required" {
		t.Errorf("expected error %q, got %q", "workspace name is required", resp.Error)
	}
}

func TestHandleWorkspaceDelete_NotFound(t *testing.T) {
	deleteFn := func(name string) error {
		return fmt.Errorf("workspace %q not found", name)
	}

	handler := handleWorkspaceDelete(deleteFn, nil, nil, nil)

	req := httptest.NewRequest(http.MethodDelete, "/api/workspace/{name}", nil)
	req.SetPathValue("name", "nonexistent")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp workspaceResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Success {
		t.Fatal("expected failure")
	}
	if resp.Error != `workspace "nonexistent" not found` {
		t.Errorf("expected not found error, got: %s", resp.Error)
	}
}

func TestHandleWorkspaceDelete_HasRunningAgents(t *testing.T) {
	deleteFn := func(name string) error {
		return fmt.Errorf("workspace %q has running agents", name)
	}

	handler := handleWorkspaceDelete(deleteFn, nil, nil, nil)

	req := httptest.NewRequest(http.MethodDelete, "/api/workspace/{name}", nil)
	req.SetPathValue("name", "busy-ws")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp workspaceResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Success {
		t.Fatal("expected failure")
	}
	if resp.Error != `workspace "busy-ws" has running agents` {
		t.Errorf("expected running agents error, got: %s", resp.Error)
	}
}

func TestHandleWorkspaceDelete_InternalError(t *testing.T) {
	deleteFn := func(name string) error {
		return fmt.Errorf("disk I/O error")
	}

	handler := handleWorkspaceDelete(deleteFn, nil, nil, nil)

	req := httptest.NewRequest(http.MethodDelete, "/api/workspace/{name}", nil)
	req.SetPathValue("name", "some-ws")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp workspaceResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Success {
		t.Fatal("expected failure")
	}
	if resp.Error != "disk I/O error" {
		t.Errorf("expected %q error, got: %s", "disk I/O error", resp.Error)
	}
}

func TestHandleWorkspaceDelete_NilDeleteFn(t *testing.T) {
	handler := handleWorkspaceDelete(nil, nil, nil, nil)

	req := httptest.NewRequest(http.MethodDelete, "/api/workspace/{name}", nil)
	req.SetPathValue("name", "some-ws")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp workspaceResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Success {
		t.Fatal("expected failure")
	}
	if resp.Error != "workspace deletion not available" {
		t.Errorf("expected %q, got: %s", "workspace deletion not available", resp.Error)
	}
}

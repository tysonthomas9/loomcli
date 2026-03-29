package webui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleSetDefaultWorkspace_Success(t *testing.T) {
	setCalled := false
	setFn := func(name string) error {
		setCalled = true
		if name != "my-ws" {
			t.Errorf("expected set called with %q, got %q", "my-ws", name)
		}
		return nil
	}

	handler := handleSetDefaultWorkspace(setFn, mockWorkspaceConfigFn)

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
		t.Fatal("expected Data to be non-nil when workspaceConfigFn provided")
	}
	if resp.Data.Name != "test-ws" {
		t.Errorf("expected data name %q, got %q", "test-ws", resp.Data.Name)
	}
}

func TestHandleSetDefaultWorkspace_EmptyBody(t *testing.T) {
	setFn := func(name string) error {
		t.Fatal("setFn should not be called with empty body")
		return nil
	}

	handler := handleSetDefaultWorkspace(setFn, nil)

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
	setFn := func(name string) error {
		t.Fatal("setFn should not be called with empty name")
		return nil
	}

	handler := handleSetDefaultWorkspace(setFn, nil)

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

func TestHandleSetDefaultWorkspace_NilSetFn(t *testing.T) {
	handler := handleSetDefaultWorkspace(nil, nil)

	body := strings.NewReader(`{"name":"my-ws"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/workspaces/default", body)
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
	if resp.Error != "set default workspace not available" {
		t.Errorf("expected %q, got: %s", "set default workspace not available", resp.Error)
	}
}

func TestHandleSetDefaultWorkspace_NotFound(t *testing.T) {
	setFn := func(name string) error {
		return fmt.Errorf("workspace %q not found", name)
	}

	handler := handleSetDefaultWorkspace(setFn, nil)

	body := strings.NewReader(`{"name":"nonexistent"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/workspaces/default", body)
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

func TestHandleSetDefaultWorkspace_InternalError(t *testing.T) {
	setFn := func(name string) error {
		return fmt.Errorf("disk I/O error")
	}

	handler := handleSetDefaultWorkspace(setFn, nil)

	body := strings.NewReader(`{"name":"my-ws"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/workspaces/default", body)
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

func TestHandleClearDefaultWorkspace_Success(t *testing.T) {
	clearCalled := false
	clearFn := func() error {
		clearCalled = true
		return nil
	}

	handler := handleClearDefaultWorkspace(clearFn, mockWorkspaceConfigFn)

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
		t.Fatal("expected Data to be non-nil when workspaceConfigFn provided")
	}
	if resp.Data.Name != "test-ws" {
		t.Errorf("expected data name %q, got %q", "test-ws", resp.Data.Name)
	}
}

func TestHandleClearDefaultWorkspace_NilClearFn(t *testing.T) {
	handler := handleClearDefaultWorkspace(nil, nil)

	req := httptest.NewRequest(http.MethodDelete, "/api/workspaces/default", nil)
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
	if resp.Error != "clear default workspace not available" {
		t.Errorf("expected %q, got: %s", "clear default workspace not available", resp.Error)
	}
}

func TestHandleClearDefaultWorkspace_Error(t *testing.T) {
	clearFn := func() error {
		return fmt.Errorf("config write failed")
	}

	handler := handleClearDefaultWorkspace(clearFn, nil)

	req := httptest.NewRequest(http.MethodDelete, "/api/workspaces/default", nil)
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
	if resp.Error != "config write failed" {
		t.Errorf("expected %q error, got: %s", "config write failed", resp.Error)
	}
}

package webui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// mockWorkspaceConfigFnWithSummaries returns a workspaceConfigFn that includes workspace summaries.
func mockWorkspaceConfigFnWithSummaries(summaries []WorkspaceSummary) func() (*WorkspaceData, error) {
	return func() (*WorkspaceData, error) {
		return &WorkspaceData{
			Name:       "test-ws",
			Path:       "/tmp/test",
			Workspaces: summaries,
		}, nil
	}
}

// deleteRequest creates a DELETE request with workspace UUID in context.
func deleteRequest(wsID string) *http.Request {
	req := httptest.NewRequest(http.MethodDelete, "/api/workspaces/"+wsID, nil)
	ctx := WithWorkspace(req.Context(), wsID)
	return req.WithContext(ctx)
}

func TestHandleWorkspaceDelete_Success(t *testing.T) {
	deleteCalled := false
	deleteFn := func(name string) error {
		deleteCalled = true
		if name != "my-ws" {
			t.Errorf("expected delete called with %q, got %q", "my-ws", name)
		}
		return nil
	}

	configFn := mockWorkspaceConfigFnWithSummaries([]WorkspaceSummary{
		{ID: "ws-uuid-1", Name: "my-ws", Path: "/tmp/my-ws"},
	})
	handler := handleWorkspaceDelete(deleteFn, configFn)

	req := deleteRequest("ws-uuid-1")
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
}

func TestHandleWorkspaceDelete_MissingWorkspaceID(t *testing.T) {
	deleteFn := func(name string) error {
		t.Fatal("deleteFn should not be called when ID is missing")
		return nil
	}

	handler := handleWorkspaceDelete(deleteFn, nil)

	// No workspace ID in context
	req := httptest.NewRequest(http.MethodDelete, "/api/workspaces/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleWorkspaceDelete_UnknownUUID(t *testing.T) {
	deleteFn := func(name string) error {
		t.Fatal("deleteFn should not be called for unknown UUID")
		return nil
	}

	configFn := mockWorkspaceConfigFnWithSummaries([]WorkspaceSummary{
		{ID: "known-uuid", Name: "known-ws", Path: "/tmp/known"},
	})
	handler := handleWorkspaceDelete(deleteFn, configFn)

	req := deleteRequest("unknown-uuid")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleWorkspaceDelete_NotFound(t *testing.T) {
	deleteFn := func(name string) error {
		return fmt.Errorf("workspace %q not found", name)
	}

	configFn := mockWorkspaceConfigFnWithSummaries([]WorkspaceSummary{
		{ID: "ws-uuid", Name: "nonexistent", Path: "/tmp/ne"},
	})
	handler := handleWorkspaceDelete(deleteFn, configFn)

	req := deleteRequest("ws-uuid")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleWorkspaceDelete_HasRunningAgents(t *testing.T) {
	deleteFn := func(name string) error {
		return fmt.Errorf("workspace %q has running agents", name)
	}

	configFn := mockWorkspaceConfigFnWithSummaries([]WorkspaceSummary{
		{ID: "busy-uuid", Name: "busy-ws", Path: "/tmp/busy"},
	})
	handler := handleWorkspaceDelete(deleteFn, configFn)

	req := deleteRequest("busy-uuid")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleWorkspaceDelete_NilDeleteFn(t *testing.T) {
	handler := handleWorkspaceDelete(nil, nil)

	req := deleteRequest("some-uuid")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501, got %d: %s", rec.Code, rec.Body.String())
	}
}

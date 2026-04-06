package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// deleteRequest creates a DELETE request with workspace UUID in context.
func deleteRequest(wsID string) *http.Request {
	req := httptest.NewRequest(http.MethodDelete, "/api/workspaces/"+wsID, nil)
	ctx := middleware.WithWorkspace(req.Context(), wsID)
	return req.WithContext(ctx)
}

func TestHandleWorkspaceDelete_Success(t *testing.T) {
	deleteCalled := false
	svc := &mockWorkspaceService{
		deleteWorkspaceFn: func(_ context.Context, wsID string) (*ops.WorkspaceData, error) {
			deleteCalled = true
			if wsID != "ws-uuid-1" {
				t.Errorf("expected wsID %q, got %q", "ws-uuid-1", wsID)
			}
			return &ops.WorkspaceData{Name: "test-ws"}, nil
		},
	}
	handler := handleWorkspaceDelete(svc)

	req := deleteRequest("ws-uuid-1")
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
	if !deleteCalled {
		t.Error("expected service DeleteWorkspace to be called")
	}
}

func TestHandleWorkspaceDelete_MissingWorkspaceID(t *testing.T) {
	svc := &mockWorkspaceService{}
	handler := handleWorkspaceDelete(svc)

	// No workspace ID in context
	req := httptest.NewRequest(http.MethodDelete, "/api/workspaces/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleWorkspaceDelete_UnknownUUID(t *testing.T) {
	svc := &mockWorkspaceService{
		deleteWorkspaceFn: func(_ context.Context, wsID string) (*ops.WorkspaceData, error) {
			return nil, service.ErrNotFound(fmt.Sprintf("workspace with ID %q not found", wsID))
		},
	}
	handler := handleWorkspaceDelete(svc)

	req := deleteRequest("unknown-uuid")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleWorkspaceDelete_HasRunningAgents(t *testing.T) {
	svc := &mockWorkspaceService{
		deleteWorkspaceFn: func(_ context.Context, _ string) (*ops.WorkspaceData, error) {
			return nil, service.ErrConflict("workspace has running agents")
		},
	}
	handler := handleWorkspaceDelete(svc)

	req := deleteRequest("busy-uuid")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleWorkspaceDelete_Unavailable(t *testing.T) {
	svc := &mockWorkspaceService{
		deleteWorkspaceFn: func(_ context.Context, _ string) (*ops.WorkspaceData, error) {
			return nil, service.ErrUnavailable("workspace deletion not available")
		},
	}
	handler := handleWorkspaceDelete(svc)

	req := deleteRequest("some-uuid")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rec.Code, rec.Body.String())
	}
}

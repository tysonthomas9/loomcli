package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// renameRequest creates a rename request with workspace UUID in context.
func renameRequest(wsID, newName string) *http.Request {
	body := strings.NewReader(`{"new_name":"` + newName + `"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/workspaces/"+wsID+"/name", body)
	ctx := middleware.WithWorkspace(req.Context(), wsID)
	return req.WithContext(ctx)
}

func TestWorkspaceRename_Success(t *testing.T) {
	svc := &mockWorkspaceService{
		renameWorkspaceFn: func(_ context.Context, wsID string, newName string) (*ops.WorkspaceData, error) {
			if wsID != "uuid-old" {
				t.Errorf("expected wsID %q, got %q", "uuid-old", wsID)
			}
			if newName != "new-name" {
				t.Errorf("expected newName %q, got %q", "new-name", newName)
			}
			return &ops.WorkspaceData{Name: "new-name"}, nil
		},
	}
	handler := handleWorkspaceRename(svc)

	req := renameRequest("uuid-old", "new-name")
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
}

func TestWorkspaceRename_DuplicateName(t *testing.T) {
	svc := &mockWorkspaceService{
		renameWorkspaceFn: func(_ context.Context, _ string, _ string) (*ops.WorkspaceData, error) {
			return nil, service.ErrConflict("workspace name already exists")
		},
	}
	handler := handleWorkspaceRename(svc)

	req := renameRequest("uuid-alpha", "beta")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp workspaceResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Success {
		t.Fatal("expected failure")
	}
	if !strings.Contains(resp.Error, "already exists") {
		t.Errorf("expected 'already exists' in error, got: %s", resp.Error)
	}
}

func TestWorkspaceRename_UnknownUUID(t *testing.T) {
	svc := &mockWorkspaceService{
		renameWorkspaceFn: func(_ context.Context, _ string, _ string) (*ops.WorkspaceData, error) {
			return nil, service.ErrNotFound("workspace not found")
		},
	}
	handler := handleWorkspaceRename(svc)

	req := renameRequest("nonexistent-uuid", "new-name")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp workspaceResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Success {
		t.Fatal("expected failure")
	}
	if !strings.Contains(resp.Error, "not found") {
		t.Errorf("expected 'not found' in error, got: %s", resp.Error)
	}
}

func TestWorkspaceRename_MissingWorkspaceID(t *testing.T) {
	svc := &mockWorkspaceService{}
	handler := handleWorkspaceRename(svc)

	// Request without workspace ID in context
	body := strings.NewReader(`{"new_name":"new-ws"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/workspaces/name", body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestWorkspaceRename_InvalidRequestBody(t *testing.T) {
	svc := &mockWorkspaceService{}
	handler := handleWorkspaceRename(svc)

	body := strings.NewReader(`{invalid json}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/workspaces/uuid/name", body)
	ctx := middleware.WithWorkspace(req.Context(), "some-uuid")
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

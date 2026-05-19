package workspace

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

// backendPatchRequest creates a PATCH request with workspace UUID in context.
func backendPatchRequest(wsID, body string) *http.Request {
	req := httptest.NewRequest(http.MethodPatch, "/api/workspaces/"+wsID+"/config/backend", strings.NewReader(body))
	ctx := middleware.WithWorkspace(req.Context(), wsID)
	return req.WithContext(ctx)
}

func TestHandleWorkspaceBackendPatch_Success(t *testing.T) {
	svc := &mockWorkspaceService{
		patchWorkspaceBackendFn: func(_ context.Context, wsID string, backend string) (*ops.WorkspaceData, error) {
			if wsID != "ws-uuid-1" {
				t.Errorf("expected wsID %q, got %q", "ws-uuid-1", wsID)
			}
			if backend != "codex" {
				t.Errorf("expected backend %q, got %q", "codex", backend)
			}
			return &ops.WorkspaceData{Name: "my-ws"}, nil
		},
	}
	handler := handleWorkspaceBackendPatch(svc)

	req := backendPatchRequest("ws-uuid-1", `{"backend":"codex"}`)
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
}

func TestHandleWorkspaceBackendGet_Success(t *testing.T) {
	svc := &mockWorkspaceService{
		getWorkspaceBackendFn: func(_ context.Context, wsID string) (*service.BackendConfigData, error) {
			if wsID != "ws-uuid-1" {
				t.Fatalf("wsID = %q", wsID)
			}
			return &service.BackendConfigData{Backend: "codex"}, nil
		},
	}
	handler := HandleWorkspaceBackendGet(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/ws-uuid-1/config/backend", nil)
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "ws-uuid-1"))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp BackendConfigResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Success || resp.Data == nil || resp.Data.Backend != "codex" {
		t.Fatalf("response = %+v", resp)
	}
}

func TestHandleWorkspaceBackendGet_MissingWorkspaceID(t *testing.T) {
	handler := HandleWorkspaceBackendGet(&mockWorkspaceService{})
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/config/backend", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleWorkspaceBackendPatch_InvalidBackend(t *testing.T) {
	svc := &mockWorkspaceService{}
	handler := handleWorkspaceBackendPatch(svc)

	req := backendPatchRequest("ws-uuid-1", `{"backend":"invalid-backend"}`)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp WorkspaceResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Success {
		t.Fatal("expected failure")
	}
	if !strings.Contains(resp.Error, "invalid backend") {
		t.Errorf("expected 'invalid backend' in error, got: %s", resp.Error)
	}
}

func TestHandleWorkspaceBackendPatch_EmptyBackend(t *testing.T) {
	svc := &mockWorkspaceService{}
	handler := handleWorkspaceBackendPatch(svc)

	req := backendPatchRequest("ws-uuid-1", `{"backend":""}`)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleWorkspaceBackendPatch_MissingWorkspaceID(t *testing.T) {
	svc := &mockWorkspaceService{}
	handler := handleWorkspaceBackendPatch(svc)

	// No workspace ID in context
	body := strings.NewReader(`{"backend":"claude"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/workspaces/config/backend", body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleWorkspaceBackendPatch_UnknownUUID(t *testing.T) {
	svc := &mockWorkspaceService{
		patchWorkspaceBackendFn: func(_ context.Context, _ string, _ string) (*ops.WorkspaceData, error) {
			return nil, service.ErrNotFound("workspace not found")
		},
	}
	handler := handleWorkspaceBackendPatch(svc)

	req := backendPatchRequest("nonexistent-uuid", `{"backend":"claude"}`)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp WorkspaceResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if !strings.Contains(resp.Error, "not found") {
		t.Errorf("expected 'not found' in error, got: %s", resp.Error)
	}
}

func TestHandleWorkspaceBackendPatch_MalformedJSON(t *testing.T) {
	svc := &mockWorkspaceService{}
	handler := handleWorkspaceBackendPatch(svc)

	req := backendPatchRequest("ws-uuid", `{invalid json}`)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

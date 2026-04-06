package workspace

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

func TestWorkspaceReorder_Success(t *testing.T) {
	reorderCalled := false
	svc := &mockWorkspaceService{
		reorderWorkspacesFn: func(_ context.Context, order []string) (*ops.WorkspaceData, error) {
			reorderCalled = true
			if len(order) != 3 {
				t.Errorf("expected 3 items, got %d", len(order))
			}
			return &ops.WorkspaceData{Name: "test-ws"}, nil
		},
	}

	handler := handleWorkspaceReorder(svc)

	body := strings.NewReader(`{"order":["gamma","alpha","beta"]}`)
	req := httptest.NewRequest(http.MethodPut, "/api/workspaces/order", body)
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
	if !reorderCalled {
		t.Error("expected reorder service to be called")
	}
}

func TestWorkspaceReorder_EmptyBody(t *testing.T) {
	svc := &mockWorkspaceService{}
	handler := handleWorkspaceReorder(svc)

	req := httptest.NewRequest(http.MethodPut, "/api/workspaces/order", strings.NewReader(""))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestWorkspaceReorder_InvalidJSON(t *testing.T) {
	svc := &mockWorkspaceService{}
	handler := handleWorkspaceReorder(svc)

	body := strings.NewReader(`{invalid json}`)
	req := httptest.NewRequest(http.MethodPut, "/api/workspaces/order", body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestWorkspaceReorder_NotFound(t *testing.T) {
	svc := &mockWorkspaceService{
		reorderWorkspacesFn: func(_ context.Context, _ []string) (*ops.WorkspaceData, error) {
			return nil, service.ErrNotFound("no config found")
		},
	}
	handler := handleWorkspaceReorder(svc)

	body := strings.NewReader(`{"order":["alpha"]}`)
	req := httptest.NewRequest(http.MethodPut, "/api/workspaces/order", body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestWorkspaceReorder_RequestBodyTooLarge(t *testing.T) {
	svc := &mockWorkspaceService{}
	handler := handleWorkspaceReorder(svc)

	largeBody := `{"order":["` + strings.Repeat("a", 1<<20+1) + `"]}`
	req := httptest.NewRequest(http.MethodPut, "/api/workspaces/order", strings.NewReader(largeBody))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d: %s", rec.Code, rec.Body.String())
	}
}

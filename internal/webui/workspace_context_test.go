package webui

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWithWorkspace_And_WorkspaceFromContext(t *testing.T) {
	ctx := WithWorkspace(t.Context(), "my-workspace")
	got := WorkspaceFromContext(ctx)
	if got != "my-workspace" {
		t.Errorf("expected 'my-workspace', got %q", got)
	}
}

func TestWorkspaceFromContext_Empty(t *testing.T) {
	got := WorkspaceFromContext(t.Context())
	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestWorkspaceMiddleware_NoHeaderFallback(t *testing.T) {
	var captured string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = WorkspaceFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	wsExists := func(id string) bool { return id == "path-ws" }

	mux := http.NewServeMux()
	mux.Handle("GET /api/workspaces/{ws}/issues", WorkspaceMiddleware(wsExists, inner))

	// Set header to a different value — middleware should use path, not header
	req := httptest.NewRequest("GET", "/api/workspaces/path-ws/issues", nil)
	req.Header.Set("Workspace", "header-ws")
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if captured != "path-ws" {
		t.Errorf("expected path param 'path-ws', got %q (header should not be consulted)", captured)
	}
}

func TestWorkspaceMiddleware_ValidUUID_PassesThrough(t *testing.T) {
	var captured string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = WorkspaceFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	wsExists := func(id string) bool { return id == "path-ws" }

	mux := http.NewServeMux()
	mux.Handle("GET /api/workspaces/{ws}/issues", WorkspaceMiddleware(wsExists, inner))

	req := httptest.NewRequest("GET", "/api/workspaces/path-ws/issues", nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if captured != "path-ws" {
		t.Errorf("expected 'path-ws', got %q", captured)
	}
}

func TestWorkspaceMiddleware_UnknownUUID_Returns404(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("inner handler should not be called for unknown workspace")
	})

	wsExists := func(id string) bool { return false }

	mux := http.NewServeMux()
	mux.Handle("GET /api/workspaces/{ws}/issues", WorkspaceMiddleware(wsExists, inner))

	req := httptest.NewRequest("GET", "/api/workspaces/nonexistent/issues", nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestWorkspaceMiddleware_EmptyPathParam_Returns400(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("inner handler should not be called")
	})

	wsExists := func(id string) bool { return true }
	handler := WorkspaceMiddleware(wsExists, inner)

	// Call directly without mux so PathValue("ws") returns ""
	req := httptest.NewRequest("GET", "/api/something", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestWorkspaceMiddleware_WhitespaceOnly_Returns400(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("inner handler should not be called")
	})

	wsExists := func(id string) bool { return true }
	handler := WorkspaceMiddleware(wsExists, inner)

	req := httptest.NewRequest("GET", "/api/workspaces/%20%20/issues", nil)
	req.SetPathValue("ws", "   ")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestWorkspaceMiddleware_InjectsIntoContext(t *testing.T) {
	var captured string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = WorkspaceFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	wsExists := func(id string) bool { return true }

	mux := http.NewServeMux()
	mux.Handle("GET /api/workspaces/{ws}/issues", WorkspaceMiddleware(wsExists, inner))

	req := httptest.NewRequest("GET", "/api/workspaces/test-uuid/issues", nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if captured != "test-uuid" {
		t.Errorf("WorkspaceFromContext should return 'test-uuid', got %q", captured)
	}
}

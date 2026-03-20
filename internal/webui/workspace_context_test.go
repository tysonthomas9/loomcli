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

func TestWorkspaceMiddleware_FromHeader(t *testing.T) {
	var captured string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = WorkspaceFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	handler := WorkspaceMiddleware(inner)

	req := httptest.NewRequest("GET", "/api/workspaces/ignored/issues", nil)
	req.Header.Set("Workspace", "header-ws")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if captured != "header-ws" {
		t.Errorf("expected 'header-ws', got %q", captured)
	}
}

func TestWorkspaceMiddleware_FromPathValue(t *testing.T) {
	var captured string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = WorkspaceFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	// Use Go 1.22+ ServeMux to test PathValue extraction
	mux := http.NewServeMux()
	mux.Handle("GET /api/workspaces/{ws}/issues", WorkspaceMiddleware(inner))

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

func TestWorkspaceMiddleware_HeaderTakesPrecedence(t *testing.T) {
	var captured string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = WorkspaceFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	mux := http.NewServeMux()
	mux.Handle("GET /api/workspaces/{ws}/issues", WorkspaceMiddleware(inner))

	req := httptest.NewRequest("GET", "/api/workspaces/path-ws/issues", nil)
	req.Header.Set("Workspace", "header-ws")
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if captured != "header-ws" {
		t.Errorf("expected header to take precedence, got %q", captured)
	}
}

func TestWorkspaceMiddleware_MissingWorkspace_Returns400(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("inner handler should not be called")
	})

	handler := WorkspaceMiddleware(inner)

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

	handler := WorkspaceMiddleware(inner)

	req := httptest.NewRequest("GET", "/api/something", nil)
	req.Header.Set("Workspace", "   ")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

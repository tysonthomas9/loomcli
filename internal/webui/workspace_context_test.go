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

// --- OptionalWorkspaceMiddleware tests ---

func TestOptionalWorkspaceMiddleware_HeaderPresent(t *testing.T) {
	var captured string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = WorkspaceFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	called := false
	resolverFn := func() string { called = true; return "default-ws" }
	handler := OptionalWorkspaceMiddleware(resolverFn, inner)

	req := httptest.NewRequest("GET", "/api/issues", nil)
	req.Header.Set("Workspace", "header-ws")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if captured != "header-ws" {
		t.Errorf("expected 'header-ws', got %q", captured)
	}
	if called {
		t.Error("resolver should not be called when header is present")
	}
}

func TestOptionalWorkspaceMiddleware_UsesResolver(t *testing.T) {
	var captured string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = WorkspaceFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	resolverFn := func() string { return "resolved-ws" }
	handler := OptionalWorkspaceMiddleware(resolverFn, inner)

	req := httptest.NewRequest("GET", "/api/issues", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if captured != "resolved-ws" {
		t.Errorf("expected 'resolved-ws', got %q", captured)
	}
}

func TestOptionalWorkspaceMiddleware_ResolverCalledPerRequest(t *testing.T) {
	var captured string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = WorkspaceFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	current := "ws-alpha"
	resolverFn := func() string { return current }
	handler := OptionalWorkspaceMiddleware(resolverFn, inner)

	// First request — resolver returns "ws-alpha"
	req1 := httptest.NewRequest("GET", "/api/issues", nil)
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)
	if captured != "ws-alpha" {
		t.Errorf("request 1: expected 'ws-alpha', got %q", captured)
	}

	// Change the default between requests
	current = "ws-beta"

	// Second request — resolver should return "ws-beta"
	req2 := httptest.NewRequest("GET", "/api/issues", nil)
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	if captured != "ws-beta" {
		t.Errorf("request 2: expected 'ws-beta', got %q", captured)
	}
}

func TestOptionalWorkspaceMiddleware_ResolverReturnsEmpty(t *testing.T) {
	var captured string
	var called bool
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = WorkspaceFromContext(r.Context())
		called = true
		w.WriteHeader(http.StatusOK)
	})

	resolverFn := func() string { return "" }
	handler := OptionalWorkspaceMiddleware(resolverFn, inner)

	req := httptest.NewRequest("GET", "/api/issues", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if !called {
		t.Fatal("inner handler should still be called")
	}
	if captured != "" {
		t.Errorf("expected empty workspace, got %q", captured)
	}
}

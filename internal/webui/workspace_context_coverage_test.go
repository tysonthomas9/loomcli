package webui

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// --- OptionalWorkspaceMiddleware tests ---

// TestOptionalWorkspaceMiddleware_WithHeader tests that the Workspace header
// value is injected into the context when present.
func TestOptionalWorkspaceMiddleware_WithHeader(t *testing.T) {
	var captured string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = WorkspaceFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	handler := OptionalWorkspaceMiddleware(func() string { return "default-ws" }, inner)

	req := httptest.NewRequest("GET", "/api/issues", nil)
	req.Header.Set("Workspace", "custom-ws")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if captured != "custom-ws" {
		t.Errorf("expected workspace %q from header, got %q", "custom-ws", captured)
	}
}

// TestOptionalWorkspaceMiddleware_NoHeader_UsesDefault tests that the default
// workspace is injected when no Workspace header is present.
func TestOptionalWorkspaceMiddleware_NoHeader_UsesDefault(t *testing.T) {
	var captured string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = WorkspaceFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	handler := OptionalWorkspaceMiddleware(func() string { return "fallback-ws" }, inner)

	req := httptest.NewRequest("GET", "/api/issues", nil)
	// No Workspace header
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if captured != "fallback-ws" {
		t.Errorf("expected workspace %q from default, got %q", "fallback-ws", captured)
	}
}

// TestOptionalWorkspaceMiddleware_WhitespaceHeader_UsesDefault tests that a
// whitespace-only Workspace header is treated as empty and falls back to default.
func TestOptionalWorkspaceMiddleware_WhitespaceHeader_UsesDefault(t *testing.T) {
	var captured string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = WorkspaceFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	handler := OptionalWorkspaceMiddleware(func() string { return "default-ws" }, inner)

	req := httptest.NewRequest("GET", "/api/issues", nil)
	req.Header.Set("Workspace", "   ")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if captured != "default-ws" {
		t.Errorf("expected workspace %q (whitespace trimmed to empty, fallback to default), got %q", "default-ws", captured)
	}
}

// TestOptionalWorkspaceMiddleware_EmptyDefault_NoHeader tests that when both
// the default and header are empty, the request passes through without a
// workspace in the context.
func TestOptionalWorkspaceMiddleware_EmptyDefault_NoHeader(t *testing.T) {
	var captured string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = WorkspaceFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	handler := OptionalWorkspaceMiddleware(func() string { return "" }, inner)

	req := httptest.NewRequest("GET", "/api/issues", nil)
	// No Workspace header, empty default
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if captured != "" {
		t.Errorf("expected empty workspace (no header, no default), got %q", captured)
	}
}

// TestOptionalWorkspaceMiddleware_HeaderTrimmed tests that leading/trailing
// whitespace in the Workspace header is trimmed.
func TestOptionalWorkspaceMiddleware_HeaderTrimmed(t *testing.T) {
	var captured string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = WorkspaceFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	handler := OptionalWorkspaceMiddleware(func() string { return "default-ws" }, inner)

	req := httptest.NewRequest("GET", "/api/issues", nil)
	req.Header.Set("Workspace", "  trimmed-ws  ")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if captured != "trimmed-ws" {
		t.Errorf("expected workspace %q (trimmed), got %q", "trimmed-ws", captured)
	}
}

// TestOptionalWorkspaceMiddleware_HeaderOverridesDefault tests that a non-empty
// header takes precedence over the default workspace, even when both are set.
func TestOptionalWorkspaceMiddleware_HeaderOverridesDefault(t *testing.T) {
	var captured string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = WorkspaceFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	handler := OptionalWorkspaceMiddleware(func() string { return "should-be-overridden" }, inner)

	req := httptest.NewRequest("GET", "/api/issues", nil)
	req.Header.Set("Workspace", "explicit-ws")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if captured != "explicit-ws" {
		t.Errorf("expected header workspace %q to override default, got %q", "explicit-ws", captured)
	}
}

// TestOptionalWorkspaceMiddleware_InnerHandlerCalled tests that the inner
// handler is always called regardless of workspace presence.
func TestOptionalWorkspaceMiddleware_InnerHandlerCalled(t *testing.T) {
	tests := []struct {
		name      string
		defaultWS string
		header    string
	}{
		{"with header", "default", "from-header"},
		{"no header with default", "default", ""},
		{"no header no default", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				w.WriteHeader(http.StatusOK)
			})

			handler := OptionalWorkspaceMiddleware(func() string { return tt.defaultWS }, inner)

			req := httptest.NewRequest("GET", "/api/issues", nil)
			if tt.header != "" {
				req.Header.Set("Workspace", tt.header)
			}
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if !called {
				t.Error("inner handler was not called")
			}
			if rec.Code != http.StatusOK {
				t.Errorf("expected 200, got %d", rec.Code)
			}
		})
	}
}

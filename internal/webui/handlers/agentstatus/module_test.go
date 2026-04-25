package agentstatus

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Compile-time assertion: *Module satisfies the Register interface.
type module interface{ Register(*http.ServeMux) }

var _ module = (*Module)(nil)

// TestModule_RegisterAndDispatch verifies that NewModule(handler).Register(mux)
// hooks GET /api/workspaces/{ws}/agents/status and that a request to that path
// reaches the underlying handler.
func TestModule_RegisterAndDispatch(t *testing.T) {
	called := false
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusTeapot) // distinctive sentinel
	})

	mod := NewModule(h)
	mux := http.NewServeMux()
	mod.Register(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/workspaces/ws-1/agents/status", nil)
	mux.ServeHTTP(rec, req)

	if !called {
		t.Fatal("handler was not invoked")
	}
	if rec.Code != http.StatusTeapot {
		t.Errorf("got status %d, expected sentinel 418", rec.Code)
	}
}

// TestModule_NilHandlerSkipsRegistration verifies the nil-handler short-circuit
// path: the route is not registered when the handler is nil.
func TestModule_NilHandlerSkipsRegistration(t *testing.T) {
	mod := NewModule(nil)
	mux := http.NewServeMux()
	mod.Register(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/workspaces/ws-1/agents/status", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 (route not registered), got %d", rec.Code)
	}
}

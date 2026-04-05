package webui

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Compile-time assertion: *TerminalTabModule implements Module.
var _ Module = (*TerminalTabModule)(nil)

func TestTerminalTabModule_RegisterRoutes(t *testing.T) {
	mod := NewTerminalTabModule(&stubTerminalService{})

	mux := http.NewServeMux()
	mod.Register(mux)

	routes := []struct {
		method string
		path   string
	}{
		{"GET", "/api/workspaces/test-ws/terminal/tabs"},
		{"GET", "/api/workspaces/test-ws/terminal/tabs/sess1"},
		{"PUT", "/api/workspaces/test-ws/terminal/tabs/sess1"},
		{"PATCH", "/api/workspaces/test-ws/terminal/tabs/sess1"},
		{"DELETE", "/api/workspaces/test-ws/terminal/tabs/sess1"},
		{"GET", "/api/workspaces/test-ws/terminal/sessions/by-issue"},
		{"GET", "/api/workspaces/test-ws/terminal/state"},
		{"PATCH", "/api/workspaces/test-ws/terminal/state"},
	}

	for _, rt := range routes {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(rt.method, rt.path, nil)
		mux.ServeHTTP(rec, req)

		if rec.Code == http.StatusNotFound {
			t.Errorf("%s %s: got 404, route not registered", rt.method, rt.path)
		}
		if rec.Code == http.StatusMethodNotAllowed {
			t.Errorf("%s %s: got 405, wrong method registered", rt.method, rt.path)
		}
	}
}

func TestTerminalTabModule_WrongMethod_Returns405(t *testing.T) {
	mod := NewTerminalTabModule(&stubTerminalService{})

	mux := http.NewServeMux()
	mod.Register(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/workspaces/test-ws/terminal/tabs", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /api/workspaces/test-ws/terminal/tabs: expected 405, got %d", rec.Code)
	}
}

func TestTerminalTabModule_NilDeps(t *testing.T) {
	mod := NewTerminalTabModule(nil)

	mux := http.NewServeMux()
	mod.Register(mux) // must not panic during registration
}

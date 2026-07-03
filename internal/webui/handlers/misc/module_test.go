package misc

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Compile-time assertion: *FileModule implements the module interface.
var _ module = (*FileModule)(nil)

func TestFileModule_RegisterRoutes(t *testing.T) {
	mod := NewFileModule(&stubFileService{})

	mux := http.NewServeMux()
	mod.Register(mux)

	routes := []struct {
		method string
		path   string
	}{
		{"GET", "/api/workspaces/test-ws/agents/agent1/files/tree"},
		{"GET", "/api/workspaces/test-ws/agents/agent1/files"},
		{"PUT", "/api/workspaces/test-ws/agents/agent1/files"},
		{"GET", "/api/workspaces/test-ws/files/git-status"},
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

func TestFileModule_WrongMethod_Returns405(t *testing.T) {
	mod := NewFileModule(&stubFileService{})

	mux := http.NewServeMux()
	mod.Register(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/workspaces/test-ws/agents/agent1/files", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("DELETE .../agents/agent1/files: expected 405, got %d", rec.Code)
	}
}

func TestFileModule_NilDeps(t *testing.T) {
	mod := NewFileModule(nil)

	mux := http.NewServeMux()
	mod.Register(mux) // must not panic during registration
}

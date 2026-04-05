package webui

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Compile-time assertion: *WorkspaceOpsModule implements Module.
var _ Module = (*WorkspaceOpsModule)(nil)

func TestWorkspaceOpsModule_RegisterRoutes(t *testing.T) {
	mod := NewWorkspaceOpsModule(&mockWorkspaceService{}, &stubErrorPool{})

	mux := http.NewServeMux()
	mod.Register(mux)

	routes := []struct {
		method string
		path   string
	}{
		{"PATCH", "/api/workspaces/test-ws/name"},
		{"GET", "/api/workspaces/test-ws/stats"},
		{"GET", "/api/workspaces/test-ws/ready"},
		{"GET", "/api/workspaces/test-ws/blocked"},
		{"GET", "/api/workspaces/test-ws/issues/graph"},
		{"GET", "/api/workspaces/test-ws/daemon/status"},
		{"GET", "/api/workspaces/test-ws/config/backend"},
		{"PATCH", "/api/workspaces/test-ws/config/backend"},
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

func TestWorkspaceOpsModule_WrongMethod_Returns405(t *testing.T) {
	mod := NewWorkspaceOpsModule(&mockWorkspaceService{}, &stubErrorPool{})

	mux := http.NewServeMux()
	mod.Register(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/workspaces/test-ws/stats", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("DELETE /api/workspaces/test-ws/stats: expected 405, got %d", rec.Code)
	}
}

func TestWorkspaceOpsModule_NilDeps(t *testing.T) {
	mod := NewWorkspaceOpsModule(nil, nil)

	mux := http.NewServeMux()
	mod.Register(mux) // must not panic during registration
}

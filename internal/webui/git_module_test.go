package webui

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Compile-time assertion: *GitModule implements Module.
var _ Module = (*GitModule)(nil)

func TestGitModule_RegisterRoutes(t *testing.T) {
	mod := NewGitModule(&mockAgentService{}, &stubDiffService{})

	mux := http.NewServeMux()
	mod.Register(mux)

	routes := []struct {
		method string
		path   string
	}{
		// Git operations
		{"POST", "/api/workspaces/test-ws/git/push-all"},
		{"POST", "/api/workspaces/test-ws/agents/agent1/git/push"},
		{"POST", "/api/workspaces/test-ws/agents/agent1/git/pull"},
		{"POST", "/api/workspaces/test-ws/agents/agent1/git/sync"},
		{"POST", "/api/workspaces/test-ws/agents/agent1/git/pr"},
		{"POST", "/api/workspaces/test-ws/agents/agent1/git/reset"},
		{"GET", "/api/workspaces/test-ws/agents/agent1/git/status"},
		{"PATCH", "/api/workspaces/test-ws/agents/agent1/git/target"},
		// Diff stat
		{"GET", "/api/workspaces/test-ws/issues/issue1/git/diff-stat"},
		{"GET", "/api/workspaces/test-ws/agents/agent1/git/diff-stat"},
		// Diff endpoints
		{"GET", "/api/workspaces/test-ws/agents/agent1/diff/commits"},
		{"GET", "/api/workspaces/test-ws/agents/agent1/diff/files"},
		{"GET", "/api/workspaces/test-ws/agents/agent1/diff/file"},
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

func TestGitModule_WrongMethod_Returns405(t *testing.T) {
	mod := NewGitModule(&mockAgentService{}, &stubDiffService{})

	mux := http.NewServeMux()
	mod.Register(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/workspaces/test-ws/git/push-all", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET .../git/push-all: expected 405, got %d", rec.Code)
	}
}

func TestGitModule_NilDeps(t *testing.T) {
	mod := NewGitModule(nil, nil)

	mux := http.NewServeMux()
	mod.Register(mux) // must not panic during registration
}

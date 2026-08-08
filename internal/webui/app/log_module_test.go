package app

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Compile-time assertion: *LogModule implements the module interface.
var _ wsModule = (*LogModule)(nil)

func TestLogModule_RegisterRoutes(t *testing.T) {
	mod := NewLogModule(&logModuleMockAgentService{})

	mux := http.NewServeMux()
	mod.Register(mux)

	routes := []struct {
		method string
		path   string
	}{
		{"GET", "/api/workspaces/test-ws/agents/agent1/logs"},
		{"GET", "/api/workspaces/test-ws/tasks/task1/logs"},
		{"GET", "/api/workspaces/test-ws/tasks/task1/logs/phase1"},
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

func TestLogModule_ConditionalRoutes(t *testing.T) {
	t.Run("nil agentSvc omits agent log route", func(t *testing.T) {
		mod := NewLogModule(nil)

		mux := http.NewServeMux()
		mod.Register(mux)

		// Agent log route should NOT be registered
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/api/workspaces/test-ws/agents/agent1/logs", nil)
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Errorf("agent log with nil agentSvc: expected 404, got %d", rec.Code)
		}

		// Task log routes should still be registered
		rec = httptest.NewRecorder()
		req = httptest.NewRequest("GET", "/api/workspaces/test-ws/tasks/task1/logs", nil)
		mux.ServeHTTP(rec, req)

		if rec.Code == http.StatusNotFound {
			t.Error("task log phases route should be registered even with nil agentSvc")
		}
	})

	t.Run("non-nil agentSvc registers agent log route", func(t *testing.T) {
		mod := NewLogModule(&logModuleMockAgentService{})

		mux := http.NewServeMux()
		mod.Register(mux)

		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/api/workspaces/test-ws/agents/agent1/logs", nil)
		mux.ServeHTTP(rec, req)

		if rec.Code == http.StatusNotFound {
			t.Error("agent log with non-nil agentSvc: got 404, route not registered")
		}
	})
}

func TestLogModule_WrongMethod_Returns405(t *testing.T) {
	mod := NewLogModule(&logModuleMockAgentService{})

	mux := http.NewServeMux()
	mod.Register(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/workspaces/test-ws/tasks/task1/logs", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST .../tasks/task1/logs: expected 405, got %d", rec.Code)
	}
}

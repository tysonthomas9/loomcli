package agentcontrol

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Compile-time assertion: *Module implements the module interface.
type module interface{ Register(*http.ServeMux) }

var _ module = (*Module)(nil)

func TestModule_RegisterRoutes(t *testing.T) {
	mockFn := func(op, agentName string, force bool) (*AgentControlResult, error) {
		return &AgentControlResult{Success: true}, nil
	}
	mod := NewModule(mockFn)

	mux := http.NewServeMux()
	mod.Register(mux)

	// GET /api/workspaces/{ws}/agents was deleted as part of the parity work
	// (no FE caller; FE uses /api/monitor/agents instead). Only the four
	// per-agent lifecycle routes remain.
	routes := []struct {
		method string
		path   string
	}{
		{"POST", "/api/workspaces/test-ws/agents/falcon/stop"},
		{"POST", "/api/workspaces/test-ws/agents/falcon/start"},
		{"POST", "/api/workspaces/test-ws/agents/falcon/restart"},
		{"POST", "/api/workspaces/test-ws/agents/falcon/yield"},
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

func TestModule_WrongMethod_Returns405(t *testing.T) {
	mockFn := func(op, agentName string, force bool) (*AgentControlResult, error) {
		return &AgentControlResult{Success: true}, nil
	}
	mod := NewModule(mockFn)

	mux := http.NewServeMux()
	mod.Register(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/workspaces/test-ws/agents/falcon/stop", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET .../stop: expected 405, got %d", rec.Code)
	}
}

package agentcontrol

import (
	"encoding/json"
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
	mockInputFn := func(op, agentName string, args json.RawMessage) (*AgentControlResult, error) {
		return &AgentControlResult{Success: true, Data: json.RawMessage("[]")}, nil
	}
	mod := NewModule(mockFn, mockInputFn)

	mux := http.NewServeMux()
	mod.Register(mux)

	routes := []struct {
		method string
		path   string
	}{
		{"POST", "/api/workspaces/test-ws/agents/falcon/stop"},
		{"POST", "/api/workspaces/test-ws/agents/falcon/start"},
		{"POST", "/api/workspaces/test-ws/agents/falcon/restart"},
		{"POST", "/api/workspaces/test-ws/agents/falcon/yield"},
		{"GET", "/api/workspaces/test-ws/pending-inputs"},
		{"GET", "/api/workspaces/test-ws/agents/falcon/input"},
		{"GET", "/api/workspaces/test-ws/agents"},
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
	mockInputFn := func(op, agentName string, args json.RawMessage) (*AgentControlResult, error) {
		return &AgentControlResult{Success: true, Data: json.RawMessage("[]")}, nil
	}
	mod := NewModule(mockFn, mockInputFn)

	mux := http.NewServeMux()
	mod.Register(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/workspaces/test-ws/agents/falcon/stop", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET .../stop: expected 405, got %d", rec.Code)
	}
}

package webui

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// Compile-time assertion: *TerminalModule implements Module.
var _ Module = (*TerminalModule)(nil)

func TestTerminalModule_RegisterRoutes(t *testing.T) {
	termAuth, err := realtime.NewTerminalAuth()
	if err != nil {
		t.Fatal(err)
	}
	defer termAuth.Stop()

	mod := NewTerminalModule(
		&stubTerminalService{}, // termSvc
		&mockAgentService{},    // agentSvc — non-nil to register all routes
		nil,                    // termMgr
		termAuth,               // termAuth — non-nil to register token routes
		nil,                    // allowedOrigins
		"",                     // loomServerURL
		func(_ string) (*service.WorkspaceData, error) { return nil, nil },
		nil, // tabMetaStore
		nil, // hub
	)

	mux := http.NewServeMux()
	mod.Register(mux)

	routes := []struct {
		method string
		path   string
	}{
		// Agent terminal endpoints
		{"GET", "/api/workspaces/test-ws/agents/agent1/terminal/info"},
		{"GET", "/api/workspaces/test-ws/agents/agent1/terminal/token"},
		{"GET", "/api/workspaces/test-ws/agents/agent1/terminal/ws"},
		// Core session management
		{"GET", "/api/workspaces/test-ws/terminal/sessions"},
		{"GET", "/api/workspaces/test-ws/terminal/token"},
		{"GET", "/api/workspaces/test-ws/terminal/ws"},
		{"POST", "/api/workspaces/test-ws/terminal/restart"},
		{"POST", "/api/workspaces/test-ws/terminal/kill"},
		{"GET", "/api/workspaces/test-ws/terminal/session-status"},
		{"POST", "/api/workspaces/test-ws/terminal/spawn"},
		{"POST", "/api/workspaces/test-ws/terminal/sessions/sess1/seed"},
		{"POST", "/api/workspaces/test-ws/terminal/sessions/sess1/kill"},
		{"POST", "/api/workspaces/test-ws/terminal/sessions/close-all"},
		// Scrollback, export, scrollback-info
		{"GET", "/api/workspaces/test-ws/terminal/sessions/sess1/scrollback"},
		{"GET", "/api/workspaces/test-ws/terminal/sessions/sess1/export"},
		{"GET", "/api/workspaces/test-ws/terminal/sessions/sess1/scrollback-info"},
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

func TestTerminalModule_ConditionalRoutes(t *testing.T) {
	t.Run("nil agentSvc omits agent terminal routes", func(t *testing.T) {
		mod := NewTerminalModule(
			&stubTerminalService{}, nil, nil, nil, nil, "",
			func(_ string) (*service.WorkspaceData, error) { return nil, nil },
			nil, nil,
		)

		mux := http.NewServeMux()
		mod.Register(mux)

		// Agent info route should NOT be registered
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/api/workspaces/test-ws/agents/agent1/terminal/info", nil)
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Errorf("agent terminal info with nil agentSvc: expected 404, got %d", rec.Code)
		}

		// Core terminal routes should still be registered
		rec = httptest.NewRecorder()
		req = httptest.NewRequest("GET", "/api/workspaces/test-ws/terminal/sessions", nil)
		mux.ServeHTTP(rec, req)

		if rec.Code == http.StatusNotFound {
			t.Error("terminal sessions route should be registered even with nil agentSvc")
		}
	})

	t.Run("nil termAuth omits token routes", func(t *testing.T) {
		mod := NewTerminalModule(
			&stubTerminalService{}, &mockAgentService{}, nil, nil, nil, "",
			func(_ string) (*service.WorkspaceData, error) { return nil, nil },
			nil, nil,
		)

		mux := http.NewServeMux()
		mod.Register(mux)

		// Terminal token route should NOT be registered
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/api/workspaces/test-ws/terminal/token", nil)
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Errorf("terminal token with nil termAuth: expected 404, got %d", rec.Code)
		}

		// Agent terminal token route should NOT be registered
		rec = httptest.NewRecorder()
		req = httptest.NewRequest("GET", "/api/workspaces/test-ws/agents/agent1/terminal/token", nil)
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Errorf("agent terminal token with nil termAuth: expected 404, got %d", rec.Code)
		}

		// Agent info route (no termAuth dep) should still be registered
		rec = httptest.NewRecorder()
		req = httptest.NewRequest("GET", "/api/workspaces/test-ws/agents/agent1/terminal/info", nil)
		mux.ServeHTTP(rec, req)

		if rec.Code == http.StatusNotFound {
			t.Error("agent terminal info should be registered with non-nil agentSvc")
		}
	})
}

func TestTerminalModule_WrongMethod_Returns405(t *testing.T) {
	mod := NewTerminalModule(
		&stubTerminalService{}, nil, nil, nil, nil, "",
		func(_ string) (*service.WorkspaceData, error) { return nil, nil },
		nil, nil,
	)

	mux := http.NewServeMux()
	mod.Register(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/workspaces/test-ws/terminal/sessions", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("DELETE .../terminal/sessions: expected 405, got %d", rec.Code)
	}
}

func TestTerminalModule_NilDeps(t *testing.T) {
	mod := NewTerminalModule(nil, nil, nil, nil, nil, "", nil, nil, nil)

	mux := http.NewServeMux()
	mod.Register(mux) // must not panic during registration
}

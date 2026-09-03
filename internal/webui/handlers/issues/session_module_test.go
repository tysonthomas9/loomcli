package issues

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Compile-time assertion: *SessionModule implements Module.
var _ Module = (*SessionModule)(nil)

// noopHandler is a placeholder handler for route registration tests.
var noopHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
})

func TestSessionModule_RegisterRoutes(t *testing.T) {
	mod := NewSessionModule(&stubSessionService{}, SessionModuleOpts{
		ListTaskSessions:                      noopHandler,
		GetSession:                            noopHandler,
		GetSessionTranscript:                  noopHandler,
		GetSessionDiff:                        noopHandler,
		ListWorkspaceSessions:                 noopHandler,
		GetWorkspaceTraceRun:                  noopHandler,
		GetWorkspaceSession:                   noopHandler,
		GetWorkspaceSessionTranscript:         noopHandler,
		GetWorkspaceSessionDiff:               noopHandler,
		ListWorkspaceSessionSubagents:         noopHandler,
		GetWorkspaceSessionSubagentTranscript: noopHandler,
	})

	mux := http.NewServeMux()
	mod.Register(mux)

	routes := []struct {
		method string
		path   string
	}{
		{"GET", "/api/workspaces/test-ws/issues/issue1/sessions"},
		{"GET", "/api/workspaces/test-ws/issues/issue1/sessions/rec1/scrollback"},
		{"GET", "/api/workspaces/test-ws/tasks/task1/sessions"},
		{"GET", "/api/workspaces/test-ws/tasks/task1/sessions/sess1"},
		{"GET", "/api/workspaces/test-ws/tasks/task1/sessions/sess1/transcript"},
		{"GET", "/api/workspaces/test-ws/tasks/task1/sessions/sess1/diff"},
		{"GET", "/api/workspaces/test-ws/sessions"},
		{"GET", "/api/workspaces/test-ws/traces/runs/run1"},
		{"GET", "/api/workspaces/test-ws/sessions/sess1"},
		{"GET", "/api/workspaces/test-ws/sessions/sess1/transcript"},
		{"GET", "/api/workspaces/test-ws/sessions/sess1/diff"},
		{"GET", "/api/workspaces/test-ws/sessions/sess1/subagents"},
		{"GET", "/api/workspaces/test-ws/sessions/sess1/subagents/sub1/transcript"},
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

func TestSessionModule_AllRoutesUnconditional(t *testing.T) {
	// History routes register regardless of whether the underlying stores are nil.
	// The SessionService handles nil stores internally.
	mod := NewSessionModule(&stubSessionService{}, SessionModuleOpts{})

	mux := http.NewServeMux()
	mod.Register(mux) // must not panic

	// Verify session history routes are always registered (not conditional)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/workspaces/test-ws/issues/issue1/sessions", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code == http.StatusNotFound {
		t.Error("session history route should be registered unconditionally")
	}
}

func TestSessionModule_WrongMethod_Returns405(t *testing.T) {
	mod := NewSessionModule(&stubSessionService{}, SessionModuleOpts{
		ListTaskSessions:     noopHandler,
		GetSession:           noopHandler,
		GetSessionTranscript: noopHandler,
		GetSessionDiff:       noopHandler,
	})

	mux := http.NewServeMux()
	mod.Register(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/workspaces/test-ws/tasks/task1/sessions", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST .../tasks/task1/sessions: expected 405, got %d", rec.Code)
	}
}

func TestSessionModule_NilDeps(t *testing.T) {
	mod := NewSessionModule(nil, SessionModuleOpts{})

	mux := http.NewServeMux()
	mod.Register(mux) // must not panic during registration
}

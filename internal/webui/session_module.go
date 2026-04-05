package webui

import "net/http"

// SessionModule registers the 6 workspace-scoped session history and audit
// trail routes on a [*http.ServeMux].
//
// All routes are unconditional — the SessionService handles nil internal
// stores gracefully. The module is always constructed when multiPool is
// available.
type SessionModule struct {
	sessSvc SessionService
}

// NewSessionModule returns a SessionModule that will register routes using
// the given session service.
func NewSessionModule(sessSvc SessionService) *SessionModule {
	return &SessionModule{sessSvc: sessSvc}
}

// Register implements [Module] by registering 6 session routes.
func (m *SessionModule) Register(mux *http.ServeMux) {
	// Session history (issue-scoped)
	mux.HandleFunc("GET /api/workspaces/{ws}/issues/{issueId}/sessions", handleListSessionHistory(m.sessSvc))
	mux.HandleFunc("GET /api/workspaces/{ws}/issues/{issueId}/sessions/{recordId}/scrollback", handleGetSessionScrollback(m.sessSvc))

	// Session audit trail (task-scoped)
	mux.HandleFunc("GET /api/workspaces/{ws}/tasks/{taskId}/sessions", handleListTaskSessions(m.sessSvc))
	mux.HandleFunc("GET /api/workspaces/{ws}/tasks/{taskId}/sessions/{sessionId}", handleGetSession(m.sessSvc))
	mux.HandleFunc("GET /api/workspaces/{ws}/tasks/{taskId}/sessions/{sessionId}/transcript", handleGetSessionTranscript(m.sessSvc))
	mux.HandleFunc("GET /api/workspaces/{ws}/tasks/{taskId}/sessions/{sessionId}/diff", handleGetSessionDiff(m.sessSvc))
}

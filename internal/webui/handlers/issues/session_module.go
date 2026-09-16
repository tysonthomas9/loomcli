package issues

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// SessionModule registers the workspace-scoped session audit-trail routes on a
// [*http.ServeMux].
//
// All routes are unconditional — the SessionService handles nil internal
// stores gracefully. The module is always constructed when multiPool is
// available.
//
// The 4 task-scoped session handlers (list, get, transcript, diff) are
// provided as pre-built HandlerFuncs because they live in a sibling package.
type SessionModule struct {
	sessSvc service.SessionService

	// Task-scoped session handlers injected from the sibling package.
	listTaskSessionsHandler         http.HandlerFunc
	getSessionHandler               http.HandlerFunc
	getSessionTranscriptHandler     http.HandlerFunc
	getSessionTranscriptByIDHandler http.HandlerFunc
	getSessionDiffHandler           http.HandlerFunc
}

// SessionModuleOpts holds the injected task-scoped session handlers.
type SessionModuleOpts struct {
	ListTaskSessions         http.HandlerFunc
	GetSession               http.HandlerFunc
	GetSessionTranscript     http.HandlerFunc
	GetSessionTranscriptByID http.HandlerFunc
	GetSessionDiff           http.HandlerFunc
}

// NewSessionModule returns a SessionModule that will register routes using
// the given session service. Task-scoped session handlers are injected via opts.
func NewSessionModule(sessSvc service.SessionService, opts SessionModuleOpts) *SessionModule {
	return &SessionModule{
		sessSvc:                         sessSvc,
		listTaskSessionsHandler:         opts.ListTaskSessions,
		getSessionHandler:               opts.GetSession,
		getSessionTranscriptHandler:     opts.GetSessionTranscript,
		getSessionTranscriptByIDHandler: opts.GetSessionTranscriptByID,
		getSessionDiffHandler:           opts.GetSessionDiff,
	}
}

// Register implements [Module] by registering the session routes.
func (m *SessionModule) Register(mux *http.ServeMux) {
	// Session audit trail (task-scoped) — handlers injected from sibling package
	if m.listTaskSessionsHandler != nil {
		mux.HandleFunc("GET /api/workspaces/{ws}/tasks/{taskId}/sessions", m.listTaskSessionsHandler)
	}
	if m.getSessionHandler != nil {
		mux.HandleFunc("GET /api/workspaces/{ws}/tasks/{taskId}/sessions/{sessionId}", m.getSessionHandler)
	}
	if m.getSessionTranscriptHandler != nil {
		mux.HandleFunc("GET /api/workspaces/{ws}/tasks/{taskId}/sessions/{sessionId}/transcript", m.getSessionTranscriptHandler)
	}
	if m.getSessionTranscriptByIDHandler != nil {
		mux.HandleFunc("GET /api/workspaces/{ws}/sessions/{sessionId}/transcript", m.getSessionTranscriptByIDHandler)
	}
	if m.getSessionDiffHandler != nil {
		mux.HandleFunc("GET /api/workspaces/{ws}/tasks/{taskId}/sessions/{sessionId}/diff", m.getSessionDiffHandler)
	}
}

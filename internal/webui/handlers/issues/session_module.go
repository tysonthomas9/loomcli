package issues

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/route"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// SessionModule registers the 6 workspace-scoped session history and audit
// trail routes on a [*http.ServeMux].
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
	listTaskSessionsHandler     http.HandlerFunc
	getSessionHandler           http.HandlerFunc
	getSessionTranscriptHandler http.HandlerFunc
	getSessionDiffHandler       http.HandlerFunc
}

// SessionModuleOpts holds the injected task-scoped session handlers.
type SessionModuleOpts struct {
	ListTaskSessions     http.HandlerFunc
	GetSession           http.HandlerFunc
	GetSessionTranscript http.HandlerFunc
	GetSessionDiff       http.HandlerFunc
}

// NewSessionModule returns a SessionModule that will register routes using
// the given session service. Task-scoped session handlers are injected via opts.
func NewSessionModule(sessSvc service.SessionService, opts SessionModuleOpts) *SessionModule {
	return &SessionModule{
		sessSvc:                     sessSvc,
		listTaskSessionsHandler:     opts.ListTaskSessions,
		getSessionHandler:           opts.GetSession,
		getSessionTranscriptHandler: opts.GetSessionTranscript,
		getSessionDiffHandler:       opts.GetSessionDiff,
	}
}

// Register implements [Module] by registering 6 session routes.
func (m *SessionModule) Register(mux route.Router) {
	// Session history (issue-scoped)
	mux.HandleFunc("GET /api/workspaces/{ws}/issues/{issueId}/sessions", handleListSessionHistory(m.sessSvc))
	mux.HandleFunc("GET /api/workspaces/{ws}/issues/{issueId}/sessions/{recordId}/scrollback", handleGetSessionScrollback(m.sessSvc))

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
	if m.getSessionDiffHandler != nil {
		mux.HandleFunc("GET /api/workspaces/{ws}/tasks/{taskId}/sessions/{sessionId}/diff", m.getSessionDiffHandler)
	}
}

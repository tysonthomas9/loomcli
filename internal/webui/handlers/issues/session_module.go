package issues

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// SessionModule registers workspace-scoped session history and audit
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

	// Workspace-scoped session handlers injected from the sibling package.
	listWorkspaceSessionsHandler          http.HandlerFunc
	getWorkspaceTraceRunHandler           http.HandlerFunc
	getWorkspaceSessionHandler            http.HandlerFunc
	getWorkspaceSessionTranscriptHandler  http.HandlerFunc
	getWorkspaceSessionDiffHandler        http.HandlerFunc
	listWorkspaceSessionSubagentsHandler  http.HandlerFunc
	getWorkspaceSessionSubagentTranscript http.HandlerFunc
}

// SessionModuleOpts holds the injected task-scoped session handlers.
type SessionModuleOpts struct {
	ListTaskSessions     http.HandlerFunc
	GetSession           http.HandlerFunc
	GetSessionTranscript http.HandlerFunc
	GetSessionDiff       http.HandlerFunc

	ListWorkspaceSessions                 http.HandlerFunc
	GetWorkspaceTraceRun                  http.HandlerFunc
	GetWorkspaceSession                   http.HandlerFunc
	GetWorkspaceSessionTranscript         http.HandlerFunc
	GetWorkspaceSessionDiff               http.HandlerFunc
	ListWorkspaceSessionSubagents         http.HandlerFunc
	GetWorkspaceSessionSubagentTranscript http.HandlerFunc
}

// NewSessionModule returns a SessionModule that will register routes using
// the given session service. Task-scoped session handlers are injected via opts.
func NewSessionModule(sessSvc service.SessionService, opts SessionModuleOpts) *SessionModule {
	return &SessionModule{
		sessSvc:                               sessSvc,
		listTaskSessionsHandler:               opts.ListTaskSessions,
		getSessionHandler:                     opts.GetSession,
		getSessionTranscriptHandler:           opts.GetSessionTranscript,
		getSessionDiffHandler:                 opts.GetSessionDiff,
		listWorkspaceSessionsHandler:          opts.ListWorkspaceSessions,
		getWorkspaceTraceRunHandler:           opts.GetWorkspaceTraceRun,
		getWorkspaceSessionHandler:            opts.GetWorkspaceSession,
		getWorkspaceSessionTranscriptHandler:  opts.GetWorkspaceSessionTranscript,
		getWorkspaceSessionDiffHandler:        opts.GetWorkspaceSessionDiff,
		listWorkspaceSessionSubagentsHandler:  opts.ListWorkspaceSessionSubagents,
		getWorkspaceSessionSubagentTranscript: opts.GetWorkspaceSessionSubagentTranscript,
	}
}

// Register implements [Module] by registering session routes.
func (m *SessionModule) Register(mux *http.ServeMux) {
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

	// Session audit trail (workspace-scoped)
	if m.listWorkspaceSessionsHandler != nil {
		mux.HandleFunc("GET /api/workspaces/{ws}/sessions", m.listWorkspaceSessionsHandler)
	}
	if m.getWorkspaceTraceRunHandler != nil {
		mux.HandleFunc("GET /api/workspaces/{ws}/traces/runs/{taskRunId}", m.getWorkspaceTraceRunHandler)
	}
	if m.getWorkspaceSessionHandler != nil {
		mux.HandleFunc("GET /api/workspaces/{ws}/sessions/{sessionId}", m.getWorkspaceSessionHandler)
	}
	if m.getWorkspaceSessionTranscriptHandler != nil {
		mux.HandleFunc("GET /api/workspaces/{ws}/sessions/{sessionId}/transcript", m.getWorkspaceSessionTranscriptHandler)
	}
	if m.getWorkspaceSessionDiffHandler != nil {
		mux.HandleFunc("GET /api/workspaces/{ws}/sessions/{sessionId}/diff", m.getWorkspaceSessionDiffHandler)
	}
	if m.listWorkspaceSessionSubagentsHandler != nil {
		mux.HandleFunc("GET /api/workspaces/{ws}/sessions/{sessionId}/subagents", m.listWorkspaceSessionSubagentsHandler)
	}
	if m.getWorkspaceSessionSubagentTranscript != nil {
		mux.HandleFunc("GET /api/workspaces/{ws}/sessions/{sessionId}/subagents/{subagentId}/transcript", m.getWorkspaceSessionSubagentTranscript)
	}
}

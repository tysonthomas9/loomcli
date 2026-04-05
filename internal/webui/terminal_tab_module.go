package webui

import "net/http"

// TerminalTabModule registers the 8 workspace-scoped terminal tab metadata
// and UI state routes on a [*http.ServeMux].
//
// The module is only constructed when termSvc is non-nil. All routes are
// unconditional within this module.
type TerminalTabModule struct {
	termSvc TerminalService
}

// NewTerminalTabModule returns a TerminalTabModule that will register routes
// using the given terminal service.
func NewTerminalTabModule(termSvc TerminalService) *TerminalTabModule {
	return &TerminalTabModule{termSvc: termSvc}
}

// Register implements [Module] by registering 8 terminal tab and state routes.
func (m *TerminalTabModule) Register(mux *http.ServeMux) {
	// Tab metadata CRUD
	mux.HandleFunc("GET /api/workspaces/{ws}/terminal/tabs", handleListTerminalTabs(m.termSvc))
	mux.HandleFunc("GET /api/workspaces/{ws}/terminal/tabs/{session}", handleGetTerminalTab(m.termSvc))
	mux.HandleFunc("PUT /api/workspaces/{ws}/terminal/tabs/{session}", handlePutTerminalTab(m.termSvc))
	mux.HandleFunc("PATCH /api/workspaces/{ws}/terminal/tabs/{session}", handlePatchTerminalTab(m.termSvc))
	mux.HandleFunc("DELETE /api/workspaces/{ws}/terminal/tabs/{session}", handleDeleteTerminalTab(m.termSvc))

	// Cross-workspace session lookup by issue
	mux.HandleFunc("GET /api/workspaces/{ws}/terminal/sessions/by-issue", handleListSessionsByIssue(m.termSvc))

	// Terminal UI state (active tab persistence)
	mux.HandleFunc("GET /api/workspaces/{ws}/terminal/state", handleGetTerminalState(m.termSvc))
	mux.HandleFunc("PATCH /api/workspaces/{ws}/terminal/state", handlePatchTerminalState(m.termSvc))
}

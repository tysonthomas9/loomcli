package terminal

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// TabModule registers the 8 workspace-scoped terminal tab metadata
// and UI state routes on a [*http.ServeMux].
//
// The module is only constructed when termSvc is non-nil. All routes are
// unconditional within this module.
type TabModule struct {
	termSvc service.TerminalService
}

// NewTabModule returns a TabModule that will register routes
// using the given terminal service.
func NewTabModule(termSvc service.TerminalService) *TabModule {
	return &TabModule{termSvc: termSvc}
}

// Register implements [Module] by registering 8 terminal tab and state routes.
func (m *TabModule) Register(mux *http.ServeMux) {
	// Tab metadata CRUD
	mux.HandleFunc("GET /api/workspaces/{ws}/terminal/tabs", HandleListTerminalTabs(m.termSvc))
	mux.HandleFunc("GET /api/workspaces/{ws}/terminal/tabs/{session}", HandleGetTerminalTab(m.termSvc))
	mux.HandleFunc("PUT /api/workspaces/{ws}/terminal/tabs/{session}", HandlePutTerminalTab(m.termSvc))
	mux.HandleFunc("PATCH /api/workspaces/{ws}/terminal/tabs/{session}", HandlePatchTerminalTab(m.termSvc))
	mux.HandleFunc("DELETE /api/workspaces/{ws}/terminal/tabs/{session}", HandleDeleteTerminalTab(m.termSvc))

	// Cross-workspace session lookup by issue
	mux.HandleFunc("GET /api/workspaces/{ws}/terminal/sessions/by-issue", HandleListSessionsByIssue(m.termSvc))

	// Terminal UI state (active tab persistence)
	mux.HandleFunc("GET /api/workspaces/{ws}/terminal/state", HandleGetTerminalState(m.termSvc))
	mux.HandleFunc("PATCH /api/workspaces/{ws}/terminal/state", HandlePatchTerminalState(m.termSvc))
}

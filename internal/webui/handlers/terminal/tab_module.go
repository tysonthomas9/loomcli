package terminal

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
	"github.com/tysonthomas9/loomcli/internal/webui/worktreegroups"
)

// TabModule registers workspace-scoped terminal tab metadata, UI state, and
// setup-control routes on a [*http.ServeMux].
//
// The module is only constructed when termSvc is non-nil. All routes are
// unconditional within this module.
type TabModule struct {
	termSvc            service.TerminalService
	workspaceStore     store.Store
	worktreeGroupStore *worktreegroups.Store
	worktreeSvc        *WorktreeGroupService
}

// NewTabModule returns a TabModule that will register routes
// using the given terminal service.
func NewTabModule(termSvc service.TerminalService, workspaceStore store.Store, worktreeGroupStore *worktreegroups.Store, worktreeSvc *WorktreeGroupService) *TabModule {
	return &TabModule{
		termSvc:            termSvc,
		workspaceStore:     workspaceStore,
		worktreeGroupStore: worktreeGroupStore,
		worktreeSvc:        worktreeSvc,
	}
}

// Register implements [Module] by registering terminal tab, state, and setup routes.
func (m *TabModule) Register(mux *http.ServeMux) {
	// Tab metadata CRUD
	mux.HandleFunc("GET /api/workspaces/{ws}/terminal/tabs", HandleListTerminalTabs(m.termSvc))
	mux.HandleFunc("GET /api/workspaces/{ws}/terminal/tabs/{session}", HandleGetTerminalTab(m.termSvc))
	mux.HandleFunc("PUT /api/workspaces/{ws}/terminal/tabs/{session}", HandlePutTerminalTab(m.termSvc, m.workspaceStore, m.worktreeGroupStore))
	mux.HandleFunc("PATCH /api/workspaces/{ws}/terminal/tabs/{session}", HandlePatchTerminalTab(m.termSvc))
	mux.HandleFunc("DELETE /api/workspaces/{ws}/terminal/tabs/{session}", HandleDeleteTerminalTab(m.termSvc))

	// Cross-workspace session lookup by issue
	mux.HandleFunc("GET /api/workspaces/{ws}/terminal/sessions/by-issue", HandleListSessionsByIssue(m.termSvc))

	if m.worktreeSvc != nil {
		mux.HandleFunc("GET /api/workspaces/{ws}/terminal/worktrees", HandleListWorktreeGroups(m.worktreeSvc))
		mux.HandleFunc("POST /api/workspaces/{ws}/terminal/worktrees", HandleCreateWorktreeGroup(m.worktreeSvc))
	}

	// Terminal UI state (active tab persistence)
	mux.HandleFunc("GET /api/workspaces/{ws}/terminal/state", HandleGetTerminalState(m.termSvc))
	mux.HandleFunc("PATCH /api/workspaces/{ws}/terminal/state", HandlePatchTerminalState(m.termSvc))

	// Backend-owned setup command runner.
	mux.HandleFunc("POST /api/workspaces/{ws}/terminal/setup", HandleStartTerminalSetup(m.termSvc))
}

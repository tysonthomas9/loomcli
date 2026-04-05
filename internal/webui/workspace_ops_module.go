package webui

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// WorkspaceOpsModule registers the 8 workspace-scoped operations, stats,
// and daemon config routes on a [*http.ServeMux].
//
// All routes are unconditional — the module is only constructed when
// multiPool is available.
type WorkspaceOpsModule struct {
	workspaceSvc service.WorkspaceService
	multiPool    daemon.Pool
}

// NewWorkspaceOpsModule returns a WorkspaceOpsModule that will register routes
// using the given workspace service and connection pool.
func NewWorkspaceOpsModule(workspaceSvc service.WorkspaceService, multiPool daemon.Pool) *WorkspaceOpsModule {
	return &WorkspaceOpsModule{
		workspaceSvc: workspaceSvc,
		multiPool:    multiPool,
	}
}

// Register implements [Module] by registering 8 workspace-scoped routes.
func (m *WorkspaceOpsModule) Register(mux *http.ServeMux) {
	// Workspace rename
	mux.HandleFunc("PATCH /api/workspaces/{ws}/name", handleWorkspaceRename(m.workspaceSvc))

	// Stats, ready, blocked, graph
	mux.HandleFunc("GET /api/workspaces/{ws}/stats", handleStats(m.multiPool))
	mux.HandleFunc("GET /api/workspaces/{ws}/ready", handleReady(m.multiPool))
	mux.HandleFunc("GET /api/workspaces/{ws}/blocked", handleBlocked(m.multiPool))
	mux.HandleFunc("GET /api/workspaces/{ws}/issues/graph", handleGraph(m.multiPool))

	// Daemon status and config
	mux.HandleFunc("GET /api/workspaces/{ws}/daemon/status", handleDaemonStatus(m.multiPool))
	mux.HandleFunc("GET /api/workspaces/{ws}/config/backend", handleGetBackendConfig(m.multiPool))
	mux.HandleFunc("PATCH /api/workspaces/{ws}/config/backend", handleWorkspaceBackendPatch(m.workspaceSvc))
}

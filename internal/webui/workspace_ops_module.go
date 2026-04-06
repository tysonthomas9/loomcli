package webui

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
	githandlers "github.com/tysonthomas9/loomcli/internal/webui/handlers/git"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/issues"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/misc"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/workspace"
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
	mux.HandleFunc("PATCH /api/workspaces/{ws}/name", workspace.HandleWorkspaceRename(m.workspaceSvc))

	// Stats, ready, blocked, graph
	mux.HandleFunc("GET /api/workspaces/{ws}/stats", misc.HandleStats(m.multiPool))
	mux.HandleFunc("GET /api/workspaces/{ws}/ready", issues.HandleReady(m.multiPool))
	mux.HandleFunc("GET /api/workspaces/{ws}/blocked", githandlers.HandleBlocked(m.multiPool))
	mux.HandleFunc("GET /api/workspaces/{ws}/issues/graph", githandlers.HandleGraph(m.multiPool))

	// Daemon status and config
	mux.HandleFunc("GET /api/workspaces/{ws}/daemon/status", misc.HandleDaemonStatus(m.multiPool))
	mux.HandleFunc("GET /api/workspaces/{ws}/config/backend", misc.HandleGetBackendConfig(m.multiPool))
	mux.HandleFunc("PATCH /api/workspaces/{ws}/config/backend", workspace.HandleWorkspaceBackendPatch(m.workspaceSvc))
}

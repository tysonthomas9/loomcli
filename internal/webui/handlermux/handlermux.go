// Package handlermux consolidates handler-package imports for building
// workspace-scoped HTTP route modules and workspace route handlers.
// The webui/app package imports this single package instead of 7+ handler
// sub-packages directly.
package handlermux

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
	githandlers "github.com/tysonthomas9/loomcli/internal/webui/handlers/git"
	healthhandlers "github.com/tysonthomas9/loomcli/internal/webui/handlers/health"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/issues"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/misc"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/workspace"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// Module is the interface for an HTTP route group that registers on a mux.
type Module interface {
	Register(mux *http.ServeMux)
}

// WorkspaceOpsModule registers workspace-scoped operations routes.
type WorkspaceOpsModule struct {
	workspaceSvc service.WorkspaceService
	multiPool    daemon.Pool
	agentQueueH  http.HandlerFunc
}

// NewWorkspaceOpsModule creates a WorkspaceOpsModule.
func NewWorkspaceOpsModule(workspaceSvc service.WorkspaceService, multiPool daemon.Pool, agentQueueH http.HandlerFunc) *WorkspaceOpsModule {
	return &WorkspaceOpsModule{workspaceSvc: workspaceSvc, multiPool: multiPool, agentQueueH: agentQueueH}
}

// Register implements Module.
func (m *WorkspaceOpsModule) Register(mux *http.ServeMux) {
	mux.HandleFunc("PATCH /api/workspaces/{ws}/name", workspace.HandleWorkspaceRename(m.workspaceSvc))
	mux.HandleFunc("GET /api/workspaces/{ws}/stats", healthhandlers.HandleStats(m.multiPool))
	mux.HandleFunc("GET /api/workspaces/{ws}/ready", issues.HandleReady(m.multiPool))
	mux.HandleFunc("GET /api/workspaces/{ws}/blocked", githandlers.HandleBlocked(m.multiPool))
	mux.HandleFunc("GET /api/workspaces/{ws}/issues/graph", githandlers.HandleGraph(m.multiPool))
	mux.HandleFunc("GET /api/workspaces/{ws}/daemon/status", healthhandlers.HandleDaemonStatus(m.multiPool))
	mux.HandleFunc("GET /api/workspaces/{ws}/config/backend", misc.HandleGetBackendConfig(m.multiPool))
	mux.HandleFunc("PATCH /api/workspaces/{ws}/config/backend", workspace.HandleWorkspaceBackendPatch(m.workspaceSvc))
	if m.agentQueueH != nil {
		mux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/queue", m.agentQueueH)
	}
}

// Workspace handler re-exports for route registration.

func HandleWorkspaceCreate(svc service.WorkspaceService) http.HandlerFunc {
	return workspace.HandleWorkspaceCreate(svc)
}
func HandleListWorkspaces(svc service.WorkspaceService) http.HandlerFunc {
	return workspace.HandleListWorkspaces(svc)
}
func HandleGetWorkspace(svc service.WorkspaceService) http.HandlerFunc {
	return workspace.HandleGetWorkspace(svc)
}
func HandleGetWorkspaceJob(svc service.WorkspaceService) http.HandlerFunc {
	return workspace.HandleGetWorkspaceJob(svc)
}
func HandleWorkspaceReorder(svc service.WorkspaceService) http.HandlerFunc {
	return workspace.HandleWorkspaceReorder(svc)
}
func HandleSetDefaultWorkspace(svc service.WorkspaceService) http.HandlerFunc {
	return workspace.HandleSetDefaultWorkspace(svc)
}
func HandleClearDefaultWorkspace(svc service.WorkspaceService) http.HandlerFunc {
	return workspace.HandleClearDefaultWorkspace(svc)
}
func HandleWorkspaceDelete(svc service.WorkspaceService) http.HandlerFunc {
	return workspace.HandleWorkspaceDelete(svc)
}
func HandleActiveWorkspace(svc service.WorkspaceService) http.HandlerFunc {
	return workspace.HandleActiveWorkspace(svc)
}

// SetupWorkerAPIRoutes re-exports misc.SetupWorkerAPIRoutes.
func SetupWorkerAPIRoutes(mux *http.ServeMux, token string, resolveWorktree func(string, string) string, resolveEventsDir func(string) string, resolveLogPath func(string, string) string, validateWorkspace func(string) bool) {
	misc.SetupWorkerAPIRoutes(mux, token, resolveWorktree, resolveEventsDir, resolveLogPath, validateWorkspace)
}

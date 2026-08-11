package app

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
	workspacemodule "github.com/tysonthomas9/loomcli/internal/modules/workspace"
	githandlers "github.com/tysonthomas9/loomcli/internal/webui/handlers/git"
	healthhandlers "github.com/tysonthomas9/loomcli/internal/webui/handlers/health"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/issues"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/misc"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/workspace"
	"github.com/tysonthomas9/loomcli/internal/webui/workspacecoord"
)

// WorkspaceOpsModule registers workspace-scoped operations routes.
type WorkspaceOpsModule struct {
	workspaceSvc        workspacecoord.WorkspaceService
	workspaceCatalog    workspacemodule.API
	workspaceProjection workspace.CatalogProjection
	workItems           workitems.ReadyQueries
	workItemStats       workitems.StatsQueries
	workItemGraph       githandlers.WorkItemQueries
	agentQueueH         http.HandlerFunc
	localPathFn         healthhandlers.WorkspaceLocalPathFn
}

func (m *WorkspaceOpsModule) WithWorkItems(queries workitems.ReadyQueries) *WorkspaceOpsModule {
	m.workItems = queries
	return m
}

func (m *WorkspaceOpsModule) WithWorkItemStats(queries workitems.StatsQueries) *WorkspaceOpsModule {
	m.workItemStats = queries
	return m
}

func (m *WorkspaceOpsModule) WithWorkItemGraph(queries githandlers.WorkItemQueries) *WorkspaceOpsModule {
	m.workItemGraph = queries
	return m
}

// NewWorkspaceOpsModule creates a WorkspaceOpsModule.
func NewWorkspaceOpsModule(workspaceSvc workspacecoord.WorkspaceService, agentQueueH http.HandlerFunc) *WorkspaceOpsModule {
	return &WorkspaceOpsModule{workspaceSvc: workspaceSvc, agentQueueH: agentQueueH}
}

// WithLocalWorkspacePathFn injects the per-machine workspace path resolver used
// by runtime readiness checks. Store-backed desktop mode needs this so a
// FleetDB workspace with no local checkout path does not appear terminal-ready.
func (m *WorkspaceOpsModule) WithLocalWorkspacePathFn(fn healthhandlers.WorkspaceLocalPathFn) *WorkspaceOpsModule {
	m.localPathFn = fn
	return m
}

// WithWorkspaceCatalog routes Workspace-owned catalog reads through the
// capability API. Missing catalog composition fails closed.
func (m *WorkspaceOpsModule) WithWorkspaceCatalog(api workspacemodule.API, projection workspace.CatalogProjection) *WorkspaceOpsModule {
	m.workspaceCatalog = api
	m.workspaceProjection = projection
	return m
}

// Register implements wsModule.
func (m *WorkspaceOpsModule) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/workspaces/{ws}/repos", workspace.HandleCatalogRepositories(m.workspaceCatalog, m.workspaceProjection))
	mux.HandleFunc("POST /api/workspaces/{ws}/repos", workspace.HandleAddWorkspaceRepos(m.workspaceSvc))
	mux.HandleFunc("GET /api/workspaces/{ws}/stats",
		healthhandlers.HandleStats(m.workItemStats))
	mux.HandleFunc("GET /api/workspaces/{ws}/ready",
		issues.HandleReadyWorkItems(m.workItems))
	mux.HandleFunc("GET /api/workspaces/{ws}/blocked",
		githandlers.HandleBlocked(m.workItemGraph))
	mux.HandleFunc("GET /api/workspaces/{ws}/issues/graph",
		githandlers.HandleGraph(m.workItemGraph))
	mux.HandleFunc("GET /api/workspaces/{ws}/readyz",
		healthhandlers.HandleWorkspaceRuntimeReadyWithLocalPath(m.workItemStats, m.localPathFn))
	mux.HandleFunc("GET /api/workspaces/{ws}/config/backend", workspace.HandleWorkspaceBackendGet(m.workspaceSvc))
	if m.agentQueueH != nil {
		mux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/queue", m.agentQueueH)
	}
}

// Workspace handler re-exports for route registration.

func HandleWorkspaceCreate(svc workspacecoord.WorkspaceService) http.HandlerFunc {
	return workspace.HandleWorkspaceCreate(svc)
}
func HandleAddWorkspaceRepos(svc workspacecoord.WorkspaceService) http.HandlerFunc {
	return workspace.HandleAddWorkspaceRepos(svc)
}
func HandleGetWorkspaceJob(svc workspacecoord.WorkspaceService) http.HandlerFunc {
	return workspace.HandleGetWorkspaceJob(svc)
}
func HandleWorkspaceReorder() http.HandlerFunc {
	return workspace.HandleWorkspaceReorder()
}
func HandleSetDefaultWorkspace() http.HandlerFunc {
	return workspace.HandleSetDefaultWorkspace()
}
func HandleClearDefaultWorkspace() http.HandlerFunc {
	return workspace.HandleClearDefaultWorkspace()
}
func HandleWorkspaceDelete(svc workspacecoord.WorkspaceService) http.HandlerFunc {
	return workspace.HandleWorkspaceDelete(svc)
}
func HandleWorkspaceBackendGet(svc workspacecoord.WorkspaceService) http.HandlerFunc {
	return workspace.HandleWorkspaceBackendGet(svc)
}
func HandleWorkspaceBackendPatch(svc workspacecoord.WorkspaceService) http.HandlerFunc {
	return workspace.HandleWorkspaceBackendPatch(svc)
}
func HandleActiveWorkspace(svc workspacecoord.WorkspaceService) http.HandlerFunc {
	return workspace.HandleActiveWorkspace(svc)
}

func HandleWorkspaceCatalogList(api workspacemodule.API, projection workspace.CatalogProjection) http.HandlerFunc {
	return workspace.HandleCatalogList(api, projection)
}

func HandleWorkspaceCatalogGet(api workspacemodule.API, projection workspace.CatalogProjection) http.HandlerFunc {
	return workspace.HandleCatalogGet(api, projection)
}

func HandleWorkspaceCatalogRepositories(api workspacemodule.API, projection workspace.CatalogProjection) http.HandlerFunc {
	return workspace.HandleCatalogRepositories(api, projection)
}

func HandleWorkspaceCatalogRename(api workspacemodule.API, projection workspace.CatalogProjection) http.HandlerFunc {
	return workspace.HandleCatalogRename(api, projection)
}

func HandleWorkspaceCatalogDesignFormatPatch(api workspacemodule.API, projection workspace.CatalogProjection) http.HandlerFunc {
	return workspace.HandleCatalogDesignFormatPatch(api, projection)
}

// SetupWorkerAPIRoutes re-exports misc.SetupWorkerAPIRoutes.
func SetupWorkerAPIRoutes(mux *http.ServeMux, token string, resolveWorktree func(string, string) string, resolveEventsDir func(string) string, resolveLogPath func(string, string) string, validateWorkspace func(string) bool) {
	misc.SetupWorkerAPIRoutes(mux, token, resolveWorktree, resolveEventsDir, resolveLogPath, validateWorkspace)
}

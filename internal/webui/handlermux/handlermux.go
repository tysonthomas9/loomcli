// Package handlermux consolidates handler-package imports for building
// workspace-scoped HTTP route modules and workspace route handlers.
// The webui/app package imports this single package instead of 7+ handler
// sub-packages directly.
package handlermux

import (
	"context"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/backend"
	workspacemodule "github.com/tysonthomas9/loomcli/internal/modules/workspace"
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
	workspaceSvc        service.WorkspaceService
	workspaceCatalog    workspacemodule.API
	workspaceProjection workspace.CatalogProjection
	agentQueueH         http.HandlerFunc
	issueBackendFn      func(ctx context.Context) backend.IssueBackend
	localPathFn         healthhandlers.WorkspaceLocalPathFn
}

// NewWorkspaceOpsModule creates a WorkspaceOpsModule. Callers supply an
// IssueBackend factory so issue query handlers use the configured durable
// backend directly.
func NewWorkspaceOpsModule(workspaceSvc service.WorkspaceService, agentQueueH http.HandlerFunc) *WorkspaceOpsModule {
	return &WorkspaceOpsModule{workspaceSvc: workspaceSvc, agentQueueH: agentQueueH}
}

// WithIssueBackendFn injects the IssueBackend factory used by handlers.
// Returns the module for chaining.
func (m *WorkspaceOpsModule) WithIssueBackendFn(fn func(ctx context.Context) backend.IssueBackend) *WorkspaceOpsModule {
	m.issueBackendFn = fn
	return m
}

// WithLocalWorkspacePathFn injects the per-machine workspace path resolver used
// by runtime readiness checks. Store-backed desktop mode needs this so a
// FleetDB workspace with no local checkout path does not appear terminal-ready.
func (m *WorkspaceOpsModule) WithLocalWorkspacePathFn(fn healthhandlers.WorkspaceLocalPathFn) *WorkspaceOpsModule {
	m.localPathFn = fn
	return m
}

// WithWorkspaceCatalog routes Workspace-owned catalog reads through the
// capability API while retaining the legacy service for mutation coordinators
// that have not yet moved behind capability boundaries.
func (m *WorkspaceOpsModule) WithWorkspaceCatalog(api workspacemodule.API, projection workspace.CatalogProjection) *WorkspaceOpsModule {
	m.workspaceCatalog = api
	m.workspaceProjection = projection
	return m
}

// Register implements Module.
func (m *WorkspaceOpsModule) Register(mux *http.ServeMux) {
	if m.workspaceCatalog != nil && m.workspaceProjection != nil {
		mux.HandleFunc("GET /api/workspaces/{ws}/repos", workspace.HandleCatalogRepositories(m.workspaceCatalog, m.workspaceProjection))
	} else {
		mux.HandleFunc("GET /api/workspaces/{ws}/repos", workspace.HandleListWorkspaceRepos(m.workspaceSvc))
	}
	mux.HandleFunc("POST /api/workspaces/{ws}/repos", workspace.HandleAddWorkspaceRepos(m.workspaceSvc))
	mux.HandleFunc("GET /api/workspaces/{ws}/stats",
		healthhandlers.HandleStatsWithBackend(healthhandlers.IssueBackendFn(m.issueBackendFn)))
	mux.HandleFunc("GET /api/workspaces/{ws}/ready",
		issues.HandleReadyWithBackend(issues.IssueBackendFn(m.issueBackendFn)))
	mux.HandleFunc("GET /api/workspaces/{ws}/blocked",
		githandlers.HandleBlockedWithBackend(githandlers.IssueBackendFn(m.issueBackendFn)))
	mux.HandleFunc("GET /api/workspaces/{ws}/issues/graph",
		githandlers.HandleGraphWithBackend(githandlers.IssueBackendFn(m.issueBackendFn)))
	mux.HandleFunc("GET /api/workspaces/{ws}/readyz",
		healthhandlers.HandleWorkspaceRuntimeReadyWithLocalPath(healthhandlers.IssueBackendFn(m.issueBackendFn), m.localPathFn))
	mux.HandleFunc("GET /api/workspaces/{ws}/config/backend", workspace.HandleWorkspaceBackendGet(m.workspaceSvc))
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
func HandleListWorkspaceRepos(svc service.WorkspaceService) http.HandlerFunc {
	return workspace.HandleListWorkspaceRepos(svc)
}
func HandleAddWorkspaceRepos(svc service.WorkspaceService) http.HandlerFunc {
	return workspace.HandleAddWorkspaceRepos(svc)
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
func HandleWorkspaceRename(svc service.WorkspaceService) http.HandlerFunc {
	return workspace.HandleWorkspaceRename(svc)
}
func HandleWorkspaceBackendGet(svc service.WorkspaceService) http.HandlerFunc {
	return workspace.HandleWorkspaceBackendGet(svc)
}
func HandleWorkspaceBackendPatch(svc service.WorkspaceService) http.HandlerFunc {
	return workspace.HandleWorkspaceBackendPatch(svc)
}
func HandleWorkspaceDesignFormatPatch(svc service.WorkspaceService) http.HandlerFunc {
	return workspace.HandleWorkspaceDesignFormatPatch(svc)
}
func HandleActiveWorkspace(svc service.WorkspaceService) http.HandlerFunc {
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

// Package handlermux consolidates handler-package imports for building
// workspace-scoped HTTP route modules and workspace route handlers.
// The webui/app package imports this single package instead of 7+ handler
// sub-packages directly.
package handlermux

import (
	"context"
	"net/http"
	"reflect"

	"github.com/tysonthomas9/loomcli/internal/backend"
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
	workspaceSvc   service.WorkspaceService
	multiPool      daemon.Pool
	agentQueueH    http.HandlerFunc
	issueBackendFn func(ctx context.Context) backend.IssueBackend
	daemonExpected bool
}

// NewWorkspaceOpsModule creates a WorkspaceOpsModule. Callers that support
// pool-less backends (e.g. fleet mode) should also call WithIssueBackendFn
// so issue query handlers can use the IssueBackend when no daemon pool exists.
//
// daemonExpected defaults to true; chain WithDaemonExpected(false) for
// fleet client mode so /daemon/status returns a fleet-mode stub instead
// of 503.
func NewWorkspaceOpsModule(workspaceSvc service.WorkspaceService, multiPool daemon.Pool, agentQueueH http.HandlerFunc) *WorkspaceOpsModule {
	return &WorkspaceOpsModule{workspaceSvc: workspaceSvc, multiPool: normalizePool(multiPool), agentQueueH: agentQueueH, daemonExpected: true}
}

func normalizePool(pool daemon.Pool) daemon.Pool {
	if pool == nil {
		return nil
	}
	v := reflect.ValueOf(pool)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		if v.IsNil() {
			return nil
		}
	}
	return pool
}

// WithIssueBackendFn injects the IssueBackend factory used by pool-less
// handlers. Returns the module for chaining.
func (m *WorkspaceOpsModule) WithIssueBackendFn(fn func(ctx context.Context) backend.IssueBackend) *WorkspaceOpsModule {
	m.issueBackendFn = fn
	return m
}

// WithDaemonExpected sets whether a local issue daemon is expected to be reachable
// for this deployment. False in fleet client mode. Returns the module for
// chaining.
func (m *WorkspaceOpsModule) WithDaemonExpected(b bool) *WorkspaceOpsModule {
	m.daemonExpected = b
	return m
}

// Register implements Module.
func (m *WorkspaceOpsModule) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/workspaces/{ws}/repos", workspace.HandleListWorkspaceRepos(m.workspaceSvc))
	mux.HandleFunc("POST /api/workspaces/{ws}/repos", workspace.HandleAddWorkspaceRepos(m.workspaceSvc))
	mux.HandleFunc("GET /api/workspaces/{ws}/stats",
		healthhandlers.HandleStatsWithBackend(m.multiPool, healthhandlers.IssueBackendFn(m.issueBackendFn)))
	mux.HandleFunc("GET /api/workspaces/{ws}/ready",
		issues.HandleReadyWithBackend(m.multiPool, issues.IssueBackendFn(m.issueBackendFn)))
	mux.HandleFunc("GET /api/workspaces/{ws}/blocked",
		githandlers.HandleBlockedWithBackend(m.multiPool, githandlers.IssueBackendFn(m.issueBackendFn)))
	mux.HandleFunc("GET /api/workspaces/{ws}/issues/graph",
		githandlers.HandleGraphWithBackend(m.multiPool, githandlers.IssueBackendFn(m.issueBackendFn)))
	mux.HandleFunc("GET /api/workspaces/{ws}/daemon/status", healthhandlers.HandleDaemonStatusWithMode(m.multiPool, m.daemonExpected))
	mux.HandleFunc("GET /api/workspaces/{ws}/readyz",
		healthhandlers.HandleWorkspaceRuntimeReady(m.multiPool, m.daemonExpected, healthhandlers.IssueBackendFn(m.issueBackendFn)))
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
func HandleActiveWorkspace(svc service.WorkspaceService) http.HandlerFunc {
	return workspace.HandleActiveWorkspace(svc)
}

// SetupWorkerAPIRoutes re-exports misc.SetupWorkerAPIRoutes.
func SetupWorkerAPIRoutes(mux *http.ServeMux, token string, resolveWorktree func(string, string) string, resolveEventsDir func(string) string, resolveLogPath func(string, string) string, validateWorkspace func(string) bool) {
	misc.SetupWorkerAPIRoutes(mux, token, resolveWorktree, resolveEventsDir, resolveLogPath, validateWorkspace)
}

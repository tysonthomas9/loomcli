package webui

import (
	"net/http"
	"time"

	"golang.org/x/time/rate"

	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
	"github.com/tysonthomas9/loomcli/internal/webui/editor"
	"github.com/tysonthomas9/loomcli/internal/webui/fleet"
	"github.com/tysonthomas9/loomcli/internal/webui/issuetabs"
	"github.com/tysonthomas9/loomcli/internal/webui/sessionhistory"
	"github.com/tysonthomas9/loomcli/internal/webui/tabmeta"
)

// setupRoutes configures all HTTP routes for the server.
// allowedOrigins is the list of allowed CORS origins for WebSocket validation.
func setupRoutes(mux *http.ServeMux, pool daemon.Pool, multiPool *daemon.MultiPool, hub *SSEHub, getMutationsSince func(since int64) []rpc.MutationEvent, termManager *TerminalManager, termAuth *terminalAuth, fleetStore *fleet.Store, tokenCfg *TokenConfig, apiKey string, authEnabled bool, allowedOrigins []string, fleetRegCfg *FleetRegisterConfig, timeoutEnforcer *fleet.TimeoutEnforcer, claimMetrics *fleet.ClaimMetrics, fleetEnabled bool, devMode bool, devFrontendDir string, loomServerURL string, gitOps GitOps, fileOps FileOps, tabMetaStore *tabmeta.Store, issueTabStore *issuetabs.Store, workspaceConfigFn func() (*WorkspaceData, error), workspaceDeleteFn func(string) error, setDefaultWsFn func(string) error, clearDefaultWsFn func() error, workspaceCreateFn WorkspaceCreateFn, backendOps BackendOps, sessionHistoryStore *sessionhistory.Store) (*clientErrorLimiter, *cspReportLimiter) { //nolint:funlen // route registration function
	// Health check endpoint for load balancers and monitoring
	mux.HandleFunc("GET /health", handleHealth(pool))

	// API health endpoint that reports daemon connection status
	mux.HandleFunc("GET /api/health", handleAPIHealth(pool))

	// Client-side error reporting endpoint (has its own per-IP rate limiter, 10 req/min/IP)
	clientErrLimiter := newClientErrorLimiter(rate.Limit(10.0/60.0), 10, 5*time.Minute, 10*time.Minute)
	mux.HandleFunc("POST /api/client-errors", handleClientErrors(clientErrLimiter))

	// CSP violation reporting endpoint (has its own per-IP rate limiter, 60 req/min/IP)
	cspLimiter := newCSPReportLimiter(rate.Limit(1.0), 20, 5*time.Minute, 10*time.Minute)
	mux.HandleFunc("POST /api/csp-report", handleCSPReport(cspLimiter))

	// Auth token bootstrap endpoint (same-origin only)
	// Always register this route so that when auth is disabled, the frontend
	// gets an explicit 404 JSON response instead of a 200 HTML from the SPA catch-all.
	if authEnabled {
		mux.HandleFunc("GET /api/auth/token", handleAuthToken(apiKey))
	} else {
		mux.HandleFunc("GET /api/auth/token", handleAuthTokenDisabled())
	}

	// Stats endpoint for project statistics (workspace-aware when multiPool available)
	mux.HandleFunc("GET /api/stats", handleStats(pool)) // Keep using main pool for now

	// SSE hub metrics endpoint
	mux.HandleFunc("GET /api/metrics", handleMetrics(hub, timeoutEnforcer, claimMetrics))

	// Daemon status endpoint - exposes daemon configuration (auto-commit, auto-push, etc.)
	mux.HandleFunc("GET /api/daemon/status", handleDaemonStatus(pool))

	// Backend configuration endpoints
	mux.HandleFunc("GET /api/config/backend", handleGetBackendConfig(pool))
	mux.HandleFunc("PATCH /api/config/backend", handlePatchBackendConfig(pool))

	// Workspace topology endpoint
	mux.HandleFunc("GET /api/workspace", handleWorkspace(workspaceConfigFn))
	mux.HandleFunc("PATCH /api/workspace/rename", handleWorkspaceRename(workspaceConfigFn))
	mux.HandleFunc("DELETE /api/workspace/{name}", handleWorkspaceDelete(workspaceDeleteFn, workspaceConfigFn))
	mux.HandleFunc("PUT /api/workspace/order", handleWorkspaceReorder(workspaceConfigFn))
	mux.HandleFunc("PUT /api/workspace/default", handleSetDefaultWorkspace(setDefaultWsFn, workspaceConfigFn))
	mux.HandleFunc("DELETE /api/workspace/default", handleClearDefaultWorkspace(clearDefaultWsFn, workspaceConfigFn))
	mux.HandleFunc("PATCH /api/workspace/{name}/config/backend", handleWorkspaceBackendPatch(workspaceConfigFn))
	mux.HandleFunc("POST /api/workspace/create", handleWorkspaceCreate(workspaceCreateFn, workspaceConfigFn))

	// Backend health endpoint
	if backendOps != nil {
		mux.HandleFunc("GET /api/backends", handleGetBackendsHealth(backendOps))
	}

	// Issue + dependency endpoints with optional workspace middleware
	registerIssueRoutes(mux, pool, multiPool, workspaceConfigFn)

	// Fleet endpoints for worker registration, task acquisition, and completion
	// Only registered when fleet coordination (Redis) is configured.
	if fleetEnabled {
		mux.HandleFunc("POST /api/fleet/register", handleFleetRegister(fleetStore, tokenCfg, fleetRegCfg))
		if tokenCfg != nil && len(tokenCfg.SigningKey) > 0 {
			fleetAuth := NewFleetAuthMiddleware(tokenCfg.SigningKey)
			mux.Handle("POST /api/fleet/claim", fleetAuth(handleFleetClaim(pool, claimMetrics)))
		} else {
			mux.HandleFunc("POST /api/fleet/claim", handleFleetClaim(pool, claimMetrics))
		}
		mux.HandleFunc("POST /api/fleet/done/{id}", handleFleetDone(fleetStore))
		if tokenCfg != nil && len(tokenCfg.SigningKey) > 0 {
			fleetAuth := NewFleetAuthMiddleware(tokenCfg.SigningKey)
			mux.Handle("POST /api/fleet/heartbeat", fleetAuth(handleFleetHeartbeat(fleetStore)))
		} else {
			mux.HandleFunc("POST /api/fleet/heartbeat", handleFleetHeartbeat(fleetStore))
		}
	}

	// Ready, blocked, and graph endpoints are registered in registerIssueRoutes

	// Server-Sent Events endpoint for real-time push notifications
	if hub != nil {
		mux.HandleFunc("GET /api/events", handleSSE(hub, getMutationsSince))
	}

	// Loom proxy for agent status endpoints (same-origin to avoid CORS/CSP issues)
	if loomProxy := newLoomProxy(loomServerURL); loomProxy != nil {
		mux.Handle("/api/loom/", loomProxy)
	}

	// Terminal token and WebSocket endpoints for authenticated terminal relay
	if termManager != nil {
		mux.HandleFunc("GET /api/terminal/sessions", handleListTerminalSessions(termManager))
		if termAuth != nil {
			mux.HandleFunc("GET /api/terminal/token", handleTerminalToken(termAuth))
			mux.HandleFunc("GET /api/agents/{name}/terminal/token", handleGetAgentTerminalToken(termAuth))
		}
		mux.HandleFunc("GET /api/terminal/ws", handleTerminalWS(termManager, termAuth, allowedOrigins, loomServerURL, workspaceConfigFn, tabMetaStore, hub))
		mux.HandleFunc("GET /api/agents/{name}/terminal/ws", handleAgentTerminalWS(termManager, termAuth, allowedOrigins))
		mux.HandleFunc("GET /api/agents/{name}/terminal/info", handleGetAgentTerminalInfo(termManager))
		mux.HandleFunc("POST /api/terminal/restart", handleTerminalRestart(termManager, pool, termAuth))
		mux.HandleFunc("POST /api/terminal/kill", handleTerminalKill(termManager, termAuth))
		mux.HandleFunc("GET /api/terminal/session-status", handleTerminalSessionStatus(termManager, termAuth))
		mux.HandleFunc("POST /api/terminal/spawn", handleTerminalSpawn(termManager, sessionHistoryStore))
		mux.HandleFunc("POST /api/terminal/sessions/{name}/seed", handleSeedTerminalSession(termManager))
		mux.HandleFunc("GET /api/terminal/sessions/{session}/scrollback", handleGetScrollback(termManager))
		mux.HandleFunc("GET /api/terminal/sessions/{session}/export", handleExportSession(termManager))
		mux.HandleFunc("GET /api/terminal/sessions/{session}/scrollback-info", handleScrollbackInfo(termManager))
		mux.HandleFunc("POST /api/terminal/sessions/{session}/kill", handleScheduleSessionKill(termManager))
		mux.HandleFunc("POST /api/terminal/sessions/close-all", handleCloseAllSessions(termManager, tabMetaStore, hub))

		// Terminal tab metadata endpoints (Redis-backed persistence)
		if tabMetaStore != nil {
			mux.HandleFunc("GET /api/terminal/sessions/by-issue", handleListSessionsByIssue(tabMetaStore))
			mux.HandleFunc("GET /api/terminal/tabs", handleListTerminalTabs(tabMetaStore, termManager))
			mux.HandleFunc("GET /api/terminal/tabs/{session}", handleGetTerminalTab(tabMetaStore))
			mux.HandleFunc("PUT /api/terminal/tabs/{session}", handlePutTerminalTab(tabMetaStore, hub))
			mux.HandleFunc("PATCH /api/terminal/tabs/{session}", handlePatchTerminalTab(tabMetaStore, hub))
			mux.HandleFunc("DELETE /api/terminal/tabs/{session}", handleDeleteTerminalTab(tabMetaStore, hub))
		}

		// Terminal UI state endpoints (Redis-backed active tab persistence)
		if tabMetaStore != nil {
			rc := tabMetaStore.RedisClient()
			mux.HandleFunc("GET /api/terminal/state", handleGetTerminalState(rc))
			mux.HandleFunc("PATCH /api/terminal/state", handlePatchTerminalState(rc))
		}
	}

	// Issue tab persistence endpoints (Redis-backed)
	mux.HandleFunc("GET /api/issues/{issueId}/tabs", handleGetIssueTabs(issueTabStore, termManager))
	mux.HandleFunc("PUT /api/issues/{issueId}/tabs", handleSaveIssueTabs(issueTabStore, hub))
	mux.HandleFunc("DELETE /api/issues/{issueId}/tabs", handleDeleteIssueTabs(issueTabStore))

	// Session history endpoints (Redis-backed audit trail)
	mux.HandleFunc("GET /api/issues/{issueId}/sessions", handleListSessionHistory(sessionHistoryStore))
	mux.HandleFunc("GET /api/issues/{issueId}/sessions/{recordId}/scrollback", handleGetSessionScrollback(sessionHistoryStore))

	// Git operation endpoints for worktrees
	if gitOps != nil {
		mux.HandleFunc("POST /api/git/push-all", handleGitPushAll(gitOps))
		mux.HandleFunc("POST /api/agents/{name}/git/push", handleGitPush(gitOps))
		mux.HandleFunc("POST /api/agents/{name}/git/pull", handleGitPull(gitOps))
		mux.HandleFunc("POST /api/agents/{name}/git/sync", handleGitSync(gitOps))
		mux.HandleFunc("POST /api/agents/{name}/git/pr", handleGitPR(gitOps))
		mux.HandleFunc("POST /api/agents/{name}/git/reset", handleGitReset(gitOps))
		mux.HandleFunc("GET /api/agents/{name}/git/status", handleGitStatus(gitOps))
		mux.HandleFunc("PATCH /api/agents/{name}/git/target", handleGitTargetUpdate(gitOps))
		mux.HandleFunc("GET /api/issues/{id}/git/diff-stat", handleGetIssueDiffStat(pool, gitOps))

		// Diff endpoints for agent worktrees
		mux.HandleFunc("GET /api/agents/{name}/diff/commits", handleDiffCommits(gitOps))
		mux.HandleFunc("GET /api/agents/{name}/diff/files", handleDiffFiles(gitOps))
		mux.HandleFunc("GET /api/agents/{name}/diff/file", handleDiffFile(gitOps))
	}

	// File operation endpoints for worktrees
	if fileOps != nil {
		mux.HandleFunc("GET /api/agents/{name}/files/tree", handleFileTree(fileOps))
		mux.HandleFunc("GET /api/agents/{name}/files", handleFileRead(fileOps))
		mux.HandleFunc("PUT /api/agents/{name}/files", handleFileWrite(fileOps))
	}

	// Editor endpoints for external editor detection and launch
	editorCache := newDefaultEditorCache()
	mux.HandleFunc("GET /api/editors", handleListEditors(editorCache))
	mux.HandleFunc("POST /api/editors/open", handleOpenEditor(editorCache, editor.LaunchEditor))

	// Log streaming endpoints
	mux.HandleFunc("GET /api/agents/{name}/logs", handleGetAgentLog())
	mux.HandleFunc("GET /api/tasks/{id}/logs", handleListTaskPhases())
	mux.HandleFunc("GET /api/tasks/{id}/logs/{phase}", handleGetTaskLog())

	// Workspace management and workspace-scoped API routes
	if multiPool != nil {
		registerWorkspaceRoutes(mux, multiPool, workspaceConfigFn)
	}

	// Static file serving with SPA routing (must be last - catches all paths)
	if devMode {
		mux.Handle("/", devFrontendHandler(devFrontendDir))
	} else {
		mux.Handle("/", frontendHandler())
	}

	return clientErrLimiter, cspLimiter
}

// registerIssueRoutes sets up issue and dependency endpoints with optional workspace middleware.
func registerIssueRoutes(mux *http.ServeMux, pool daemon.Pool, multiPool *daemon.MultiPool, workspaceConfigFn func() (*WorkspaceData, error)) {
	var issuePool daemon.Pool = pool
	defaultWSID := ""
	if multiPool != nil {
		issuePool = multiPool
		if workspaceConfigFn != nil {
			if wsData, err := workspaceConfigFn(); err == nil && wsData != nil {
				defaultWSID = wsData.Name
			}
		}
		if defaultWSID == "" {
			wsIDs := multiPool.WorkspaceIDs()
			if len(wsIDs) > 0 {
				defaultWSID = wsIDs[0]
			}
		}
	}
	wrapWS := func(h http.HandlerFunc) http.Handler {
		if multiPool != nil {
			return OptionalWorkspaceMiddleware(defaultWSID, h)
		}
		return h
	}
	mux.Handle("GET /api/issues/{id}", wrapWS(handleGetIssue(issuePool)))
	mux.Handle("GET /api/issues", wrapWS(handleListIssues(issuePool)))
	mux.Handle("POST /api/issues", wrapWS(handleCreateIssue(issuePool)))
	mux.Handle("PATCH /api/issues/{id}", wrapWS(handlePatchIssue(issuePool)))
	mux.Handle("POST /api/issues/{id}/close", wrapWS(handleCloseIssue(issuePool)))
	mux.Handle("POST /api/issues/{id}/move", wrapWS(handleMoveIssue(issuePool, workspaceConfigFn)))
	mux.Handle("DELETE /api/issues/{id}", wrapWS(handleDeleteIssue(issuePool)))
	mux.Handle("POST /api/issues/{id}/comments", wrapWS(handleAddComment(issuePool)))
	mux.Handle("GET /api/issues/{id}/events", wrapWS(handleGetIssueEvents(issuePool)))
	mux.Handle("POST /api/issues/{id}/dependencies", wrapWS(handleAddDependency(issuePool)))
	mux.Handle("DELETE /api/issues/{id}/dependencies/{depId}", wrapWS(handleRemoveDependency(issuePool)))
	mux.Handle("GET /api/ready", wrapWS(handleReady(issuePool)))
	mux.Handle("GET /api/blocked", wrapWS(handleBlocked(issuePool)))
	mux.Handle("GET /api/issues/graph", wrapWS(handleGraph(issuePool)))
}

// registerWorkspaceRoutes sets up workspace listing and workspace-scoped API routes.
func registerWorkspaceRoutes(mux *http.ServeMux, multiPool *daemon.MultiPool, workspaceConfigFn func() (*WorkspaceData, error)) {
	// Workspace listing (not workspace-scoped themselves)
	mux.HandleFunc("GET /api/workspaces", handleListWorkspaces(multiPool, workspaceConfigFn))
	mux.HandleFunc("GET /api/workspaces/{ws}", handleGetWorkspace(multiPool, workspaceConfigFn))

	// Workspace-scoped API routes via a sub-mux with WorkspaceMiddleware.
	// The middleware injects the workspace ID into the context so that
	// multiPool.Get(ctx) routes to the correct per-workspace pool.
	wsMux := http.NewServeMux()

	// Issue endpoints
	wsMux.HandleFunc("GET /api/workspaces/{ws}/issues/{id}", handleGetIssue(multiPool))
	wsMux.HandleFunc("GET /api/workspaces/{ws}/issues", handleListIssues(multiPool))
	wsMux.HandleFunc("POST /api/workspaces/{ws}/issues", handleCreateIssue(multiPool))
	wsMux.HandleFunc("PATCH /api/workspaces/{ws}/issues/{id}", handlePatchIssue(multiPool))
	wsMux.HandleFunc("POST /api/workspaces/{ws}/issues/{id}/close", handleCloseIssue(multiPool))
	wsMux.HandleFunc("POST /api/workspaces/{ws}/issues/{id}/move", handleMoveIssue(multiPool, workspaceConfigFn))
	wsMux.HandleFunc("DELETE /api/workspaces/{ws}/issues/{id}", handleDeleteIssue(multiPool))
	wsMux.HandleFunc("POST /api/workspaces/{ws}/issues/{id}/comments", handleAddComment(multiPool))
	wsMux.HandleFunc("GET /api/workspaces/{ws}/issues/{id}/events", handleGetIssueEvents(multiPool))

	// Dependency management
	wsMux.HandleFunc("POST /api/workspaces/{ws}/issues/{id}/dependencies", handleAddDependency(multiPool))
	wsMux.HandleFunc("DELETE /api/workspaces/{ws}/issues/{id}/dependencies/{depId}", handleRemoveDependency(multiPool))

	// Stats, ready, blocked, graph
	wsMux.HandleFunc("GET /api/workspaces/{ws}/stats", handleStats(multiPool))
	wsMux.HandleFunc("GET /api/workspaces/{ws}/ready", handleReady(multiPool))
	wsMux.HandleFunc("GET /api/workspaces/{ws}/blocked", handleBlocked(multiPool))
	wsMux.HandleFunc("GET /api/workspaces/{ws}/issues/graph", handleGraph(multiPool))

	// Daemon status and config
	wsMux.HandleFunc("GET /api/workspaces/{ws}/daemon/status", handleDaemonStatus(multiPool))
	wsMux.HandleFunc("GET /api/workspaces/{ws}/config/backend", handleGetBackendConfig(multiPool))
	wsMux.HandleFunc("PATCH /api/workspaces/{ws}/config/backend", handlePatchBackendConfig(multiPool))

	// Apply WorkspaceMiddleware to all workspace-scoped routes
	mux.Handle("/api/workspaces/{ws}/", WorkspaceMiddleware(wsMux))
}

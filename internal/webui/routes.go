package webui

import (
	"net/http"
	"time"

	"golang.org/x/time/rate"

	"github.com/tysonthomas9/loomcli/internal/webui/editor"
	"github.com/tysonthomas9/loomcli/internal/webui/fleet"
)

// setupRoutes configures all HTTP routes for the server.
// Dependencies are read from serverApp fields.
func (app *serverApp) setupRoutes(mux *http.ServeMux) (*clientErrorLimiter, *cspReportLimiter, *authConfigLimiter) {
	// Health check endpoint for load balancers and monitoring
	mux.HandleFunc("GET /health", handleHealth(app.pool))

	// API health endpoint that reports daemon connection status
	mux.HandleFunc("GET /api/health", handleAPIHealth(app.pool))

	// Client-side error reporting endpoint (has its own per-IP rate limiter, 10 req/min/IP)
	clientErrLimiter := newClientErrorLimiter(rate.Limit(10.0/60.0), 10, 5*time.Minute, 10*time.Minute)
	mux.HandleFunc("POST /api/client-errors", handleClientErrors(clientErrLimiter))

	// CSP violation reporting endpoint (has its own per-IP rate limiter, 60 req/min/IP)
	cspLimiter := newCSPReportLimiter(rate.Limit(1.0), 20, 5*time.Minute, 10*time.Minute)
	mux.HandleFunc("POST /api/csp-report", handleCSPReport(cspLimiter))

	// Auth mode discovery endpoint (public, rate-limited — called once per page load).
	// extAuthURL is the Better Auth service base URL for OAuth redirects, not JWKS.
	authCfgLimiter := newAuthConfigLimiter(rate.Limit(5), 10, 5*time.Minute, 10*time.Minute)
	mux.HandleFunc("GET /api/config", handleAuthConfig(app.config.ExtAuthURL, authCfgLimiter))

	// Stats endpoint for project statistics (workspace-aware when multiPool available)
	mux.HandleFunc("GET /api/stats", handleStats(app.pool)) // Keep using main pool for now

	// SSE hub metrics endpoint
	var getFleetTimeouts func() int64
	if app.fleetRegistry != nil {
		getFleetTimeouts = app.fleetRegistry.GetTotalTimeoutCount
	}
	mux.HandleFunc("GET /api/metrics", handleMetrics(app.hub, getFleetTimeouts, app.claimMetrics))

	// Daemon status endpoint - exposes daemon configuration (auto-commit, auto-push, etc.)
	mux.HandleFunc("GET /api/daemon/status", handleDaemonStatus(app.pool))

	// Backend configuration endpoints
	mux.HandleFunc("GET /api/config/backend", handleGetBackendConfig(app.pool))
	mux.HandleFunc("PATCH /api/config/backend", handlePatchBackendConfig(app.pool))

	// Workspace CRUD endpoints are registered in registerWorkspaceRoutes below.

	// Backend health endpoint
	if app.config.BackendOps != nil {
		mux.HandleFunc("GET /api/backends", handleGetBackendsHealth(app.config.BackendOps))
	}

	// Fleet endpoints: workspace-scoped routes only (flat routes removed).
	// Workspace-scoped fleet routes are registered in registerWorkspaceRoutes below.

	// Legacy SSE endpoint removed — SSE is now workspace-scoped at /api/workspaces/{ws}/events

	// Loom proxy for agent status endpoints (same-origin to avoid CORS/CSP issues)
	if loomProxy := newLoomProxy(app.config.LoomServerURL); loomProxy != nil {
		mux.Handle("/api/loom/", loomProxy)
	}

	// Terminal endpoints: workspace-scoped routes only (flat routes removed).
	// All terminal routes are registered in registerWorkspaceRoutes below.

	// Editor endpoints for external editor detection and launch
	editorCache := newDefaultEditorCache()
	mux.HandleFunc("GET /api/editors", handleListEditors(editorCache))
	mux.HandleFunc("POST /api/editors/open", handleOpenEditor(editorCache, editor.LaunchEditor))

	// Session change notification endpoint for local agents to push SSE events
	if app.hub != nil {
		mux.HandleFunc("POST /api/sessions/notify", handleNotifySessionChange(app.hub, app.notifyToken))
	}

	// Workspace management and workspace-scoped API routes
	if app.multiPool != nil {
		app.registerWorkspaceRoutes(mux)
	}

	// Static file serving with SPA routing (must be last - catches all paths)
	if app.config.DevMode {
		mux.Handle("/", devFrontendHandler(app.config.DevFrontendDir))
	} else {
		mux.Handle("/", frontendHandler())
	}

	return clientErrLimiter, cspLimiter, authCfgLimiter
}

// registerWorkspaceRoutes sets up workspace listing, CRUD, and workspace-scoped API routes.
func (app *serverApp) registerWorkspaceRoutes(mux *http.ServeMux) { //nolint:funlen // route registration function
	workspaceConfigFn := app.config.WorkspaceConfigFn
	workspaceConfigByIDFn := app.config.WorkspaceConfigByIDFn

	// Active workspace endpoint — returns full topology for the default workspace
	mux.HandleFunc("GET /api/workspaces/active", handleActiveWorkspace(workspaceConfigFn))

	// Workspace listing (not workspace-scoped themselves)
	mux.HandleFunc("GET /api/workspaces", handleListWorkspaces(app.multiPool, workspaceConfigFn))
	mux.HandleFunc("GET /api/workspaces/{ws}", handleGetWorkspace(app.wsExistsFn, workspaceConfigByIDFn))

	// Global workspace CRUD operations (no WorkspaceMiddleware)
	mux.HandleFunc("POST /api/workspaces", handleWorkspaceCreate(app.wrappedCreateFn, workspaceConfigFn, app.jobStore))

	// Workspace job polling endpoint (literal "jobs" segment wins over {ws} wildcard)
	mux.HandleFunc("GET /api/workspaces/jobs/{id}", handleGetWorkspaceJob(app.jobStore))
	mux.HandleFunc("PUT /api/workspaces/order", handleWorkspaceReorder(workspaceConfigFn))
	mux.HandleFunc("PUT /api/workspaces/default", handleSetDefaultWorkspace(app.config.SetDefaultWorkspaceFn, workspaceConfigFn))
	mux.HandleFunc("DELETE /api/workspaces/default", handleClearDefaultWorkspace(app.config.ClearDefaultWorkspaceFn, workspaceConfigFn))

	// Per-workspace DELETE — registered on main mux with manual middleware wrapping
	// because DELETE /api/workspaces/{ws} (no trailing slash) won't match the
	// wsMux prefix handler at /api/workspaces/{ws}/.
	mux.Handle("DELETE /api/workspaces/{ws}", WorkspaceMiddleware(app.wsExistsFn, handleWorkspaceDelete(app.wrappedDeleteFn, workspaceConfigFn)))

	// Workspace-scoped API routes via a sub-mux with WorkspaceMiddleware.
	// The middleware injects the workspace ID into the context so that
	// multiPool.Get(ctx) routes to the correct per-workspace pool.
	wsMux := http.NewServeMux()

	// Per-workspace CRUD (through WorkspaceMiddleware via wsMux)
	wsMux.HandleFunc("PATCH /api/workspaces/{ws}/name", handleWorkspaceRename(workspaceConfigFn))

	// Issue endpoints
	wsMux.HandleFunc("GET /api/workspaces/{ws}/issues/{id}", handleGetIssue(app.multiPool))
	wsMux.HandleFunc("GET /api/workspaces/{ws}/issues", handleListIssues(app.multiPool))
	wsMux.HandleFunc("POST /api/workspaces/{ws}/issues", handleCreateIssue(app.multiPool))
	wsMux.HandleFunc("PATCH /api/workspaces/{ws}/issues/{id}", handlePatchIssue(app.multiPool))
	wsMux.HandleFunc("POST /api/workspaces/{ws}/issues/{id}/close", handleCloseIssue(app.multiPool))
	wsMux.HandleFunc("POST /api/workspaces/{ws}/issues/{id}/move", handleMoveIssue(app.multiPool, app.multiPool, workspaceConfigFn))
	wsMux.HandleFunc("DELETE /api/workspaces/{ws}/issues/{id}", handleDeleteIssue(app.multiPool))
	wsMux.HandleFunc("POST /api/workspaces/{ws}/issues/{id}/comments", handleAddComment(app.multiPool))
	wsMux.HandleFunc("GET /api/workspaces/{ws}/issues/{id}/events", handleGetIssueEvents(app.multiPool))

	// Dependency management
	wsMux.HandleFunc("POST /api/workspaces/{ws}/issues/{id}/dependencies", handleAddDependency(app.multiPool))
	wsMux.HandleFunc("DELETE /api/workspaces/{ws}/issues/{id}/dependencies/{depId}", handleRemoveDependency(app.multiPool))

	// Stats, ready, blocked, graph
	wsMux.HandleFunc("GET /api/workspaces/{ws}/stats", handleStats(app.multiPool))
	wsMux.HandleFunc("GET /api/workspaces/{ws}/ready", handleReady(app.multiPool))
	wsMux.HandleFunc("GET /api/workspaces/{ws}/blocked", handleBlocked(app.multiPool))
	wsMux.HandleFunc("GET /api/workspaces/{ws}/issues/graph", handleGraph(app.multiPool))

	// Daemon status and config
	wsMux.HandleFunc("GET /api/workspaces/{ws}/daemon/status", handleDaemonStatus(app.multiPool))
	wsMux.HandleFunc("GET /api/workspaces/{ws}/config/backend", handleGetBackendConfig(app.multiPool))
	wsMux.HandleFunc("PATCH /api/workspaces/{ws}/config/backend", handleWorkspaceBackendPatch(workspaceConfigFn))

	// Log streaming endpoints (workspace-scoped)
	wsMux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/logs", handleGetAgentLog())
	wsMux.HandleFunc("GET /api/workspaces/{ws}/tasks/{id}/logs", handleListTaskPhases())
	wsMux.HandleFunc("GET /api/workspaces/{ws}/tasks/{id}/logs/{phase}", handleGetTaskLog())

	// Server-Sent Events endpoint (workspace-scoped)
	if app.hub != nil {
		var sseHandler http.Handler
		if app.sseTokens != nil {
			sseHandler = NewSSEHandlerWithAuth(app.hub, app.getMutationsSince, app.sseTokens)
		} else {
			sseHandler = NewSSEHandler(app.hub, app.getMutationsSince)
		}
		wsMux.Handle("GET /api/workspaces/{ws}/events", sseHandler)
		// SSE token exchange: exchanges JWT for a short-lived opaque token.
		// Protected by ExtAuth middleware (JWT required in external mode).
		if app.sseTokens != nil {
			wsMux.HandleFunc("GET /api/workspaces/{ws}/events/token", handleSSEToken(app.sseTokens))
		}
	}

	// Terminal tab metadata endpoints (workspace-scoped, Redis-backed)
	if app.tabMetaStore != nil {
		wsMux.HandleFunc("GET /api/workspaces/{ws}/terminal/tabs", handleListTerminalTabs(app.tabMetaStore, app.termMgr))
		wsMux.HandleFunc("GET /api/workspaces/{ws}/terminal/tabs/{session}", handleGetTerminalTab(app.tabMetaStore))
		wsMux.HandleFunc("PUT /api/workspaces/{ws}/terminal/tabs/{session}", handlePutTerminalTab(app.tabMetaStore, app.hub))
		wsMux.HandleFunc("PATCH /api/workspaces/{ws}/terminal/tabs/{session}", handlePatchTerminalTab(app.tabMetaStore, app.hub))
		wsMux.HandleFunc("DELETE /api/workspaces/{ws}/terminal/tabs/{session}", handleDeleteTerminalTab(app.tabMetaStore, app.hub))
		// Cross-workspace endpoint: the workspace in the URL is for auth context,
		// but ListByIssue searches across all workspaces intentionally.
		wsMux.HandleFunc("GET /api/workspaces/{ws}/terminal/sessions/by-issue", handleListSessionsByIssue(app.tabMetaStore))

		// Terminal UI state endpoints (Redis-backed active tab persistence, workspace-scoped)
		rc := app.tabMetaStore.RedisClient()
		wsMux.HandleFunc("GET /api/workspaces/{ws}/terminal/state", handleGetTerminalState(rc))
		wsMux.HandleFunc("PATCH /api/workspaces/{ws}/terminal/state", handlePatchTerminalState(rc))
	}

	// Issue tab persistence endpoints (Redis-backed, workspace-scoped)
	if app.issueTabStore != nil {
		wsMux.HandleFunc("GET /api/workspaces/{ws}/issues/{issueId}/tabs", handleGetIssueTabs(app.issueTabStore, app.termMgr))
		wsMux.HandleFunc("PUT /api/workspaces/{ws}/issues/{issueId}/tabs", handleSaveIssueTabs(app.issueTabStore, app.hub))
		wsMux.HandleFunc("DELETE /api/workspaces/{ws}/issues/{issueId}/tabs", handleDeleteIssueTabs(app.issueTabStore))
	}

	// Session history endpoints (Redis-backed audit trail, workspace-scoped)
	if app.sessionHistoryStore != nil {
		wsMux.HandleFunc("GET /api/workspaces/{ws}/issues/{issueId}/sessions", handleListSessionHistory(app.sessionHistoryStore))
		wsMux.HandleFunc("GET /api/workspaces/{ws}/issues/{issueId}/sessions/{recordId}/scrollback", handleGetSessionScrollback(app.sessionHistoryStore))
	}

	// Session audit trail endpoints (workspace-scoped)
	wsMux.HandleFunc("GET /api/workspaces/{ws}/tasks/{taskId}/sessions", handleListTaskSessions(app.config.SessionsStore))
	wsMux.HandleFunc("GET /api/workspaces/{ws}/tasks/{taskId}/sessions/{sessionId}", handleGetSession(app.config.SessionsStore))
	wsMux.HandleFunc("GET /api/workspaces/{ws}/tasks/{taskId}/sessions/{sessionId}/transcript", handleGetSessionTranscript(app.config.SessionsStore))
	wsMux.HandleFunc("GET /api/workspaces/{ws}/tasks/{taskId}/sessions/{sessionId}/diff", handleGetSessionDiff(app.config.SessionsStore))

	// Terminal endpoints (workspace-scoped) — agent, core session, and scrollback/export
	if app.termMgr != nil {
		// Agent terminal endpoints
		wsMux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/terminal/info", handleGetAgentTerminalInfo(app.termMgr))
		if app.termAuth != nil {
			wsMux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/terminal/token", handleGetAgentTerminalToken(app.termAuth))
		}
		wsMux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/terminal/ws", handleAgentTerminalWS(app.termMgr, app.termAuth, app.corsConfig.AllowedOrigins))

		// Core terminal session management endpoints
		wsMux.HandleFunc("GET /api/workspaces/{ws}/terminal/sessions", handleListTerminalSessions(app.termMgr))
		if app.termAuth != nil {
			wsMux.HandleFunc("GET /api/workspaces/{ws}/terminal/token", handleTerminalToken(app.termAuth))
		}
		wsMux.HandleFunc("GET /api/workspaces/{ws}/terminal/ws", handleTerminalWS(app.termMgr, app.termAuth, app.corsConfig.AllowedOrigins, app.config.LoomServerURL, workspaceConfigByIDFn, app.tabMetaStore, app.hub))
		wsMux.HandleFunc("POST /api/workspaces/{ws}/terminal/restart", handleTerminalRestart(app.termMgr, app.multiPool, app.termAuth))
		wsMux.HandleFunc("POST /api/workspaces/{ws}/terminal/kill", handleTerminalKill(app.termMgr, app.termAuth))
		wsMux.HandleFunc("GET /api/workspaces/{ws}/terminal/session-status", handleTerminalSessionStatus(app.termMgr, app.termAuth))
		wsMux.HandleFunc("POST /api/workspaces/{ws}/terminal/spawn", handleTerminalSpawn(app.termMgr, app.sessionHistoryStore))
		wsMux.HandleFunc("POST /api/workspaces/{ws}/terminal/sessions/{name}/seed", handleSeedTerminalSession(app.termMgr))
		wsMux.HandleFunc("POST /api/workspaces/{ws}/terminal/sessions/{session}/kill", handleScheduleSessionKill(app.termMgr))
		// Note: closeAllSessions operates globally (kills all tmux sessions) regardless of
		// the workspace in the URL. The workspace provides auth context only.
		wsMux.HandleFunc("POST /api/workspaces/{ws}/terminal/sessions/close-all", handleCloseAllSessions(app.termMgr, app.tabMetaStore, app.hub))

		// Terminal scrollback, export, and scrollback-info endpoints
		wsMux.HandleFunc("GET /api/workspaces/{ws}/terminal/sessions/{session}/scrollback", handleGetScrollback(app.termMgr))
		wsMux.HandleFunc("GET /api/workspaces/{ws}/terminal/sessions/{session}/export", handleExportSession(app.termMgr))
		wsMux.HandleFunc("GET /api/workspaces/{ws}/terminal/sessions/{session}/scrollback-info", handleScrollbackInfo(app.termMgr))
	}

	// Workspace-scoped fleet routes. Claim uses multiPool (routes to correct
	// workspace daemon); register/done/heartbeat resolve the Store per-request.
	if app.fleetRegistry != nil {
		wsMux.HandleFunc("POST /api/workspaces/{ws}/fleet/register",
			fleetWSHandler(app.fleetRegistry, func(s *fleet.Store) http.HandlerFunc {
				return handleFleetRegister(s, app.tokenCfg, app.fleetRegCfg)
			}))
		if app.tokenCfg != nil && len(app.tokenCfg.SigningKey) > 0 {
			fleetAuth := NewFleetAuthMiddleware(app.tokenCfg.SigningKey)
			wsMux.Handle("POST /api/workspaces/{ws}/fleet/claim",
				fleetAuth(handleFleetClaim(app.multiPool, app.claimMetrics)))
		} else {
			wsMux.HandleFunc("POST /api/workspaces/{ws}/fleet/claim",
				handleFleetClaim(app.multiPool, app.claimMetrics))
		}
		if app.tokenCfg != nil && len(app.tokenCfg.SigningKey) > 0 {
			fleetAuthDone := NewFleetAuthMiddleware(app.tokenCfg.SigningKey)
			wsMux.Handle("POST /api/workspaces/{ws}/fleet/done/{id}",
				fleetAuthDone(fleetWSHandler(app.fleetRegistry, handleFleetDone)))
		} else {
			wsMux.HandleFunc("POST /api/workspaces/{ws}/fleet/done/{id}",
				fleetWSHandler(app.fleetRegistry, handleFleetDone))
		}
		if app.tokenCfg != nil && len(app.tokenCfg.SigningKey) > 0 {
			fleetAuth := NewFleetAuthMiddleware(app.tokenCfg.SigningKey)
			wsMux.Handle("POST /api/workspaces/{ws}/fleet/heartbeat",
				fleetAuth(fleetWSHandler(app.fleetRegistry, handleFleetHeartbeat)))
		} else {
			wsMux.HandleFunc("POST /api/workspaces/{ws}/fleet/heartbeat",
				fleetWSHandler(app.fleetRegistry, handleFleetHeartbeat))
		}
	}

	// Git operations (workspace-scoped)
	if app.config.GitOps != nil {
		wsMux.HandleFunc("POST /api/workspaces/{ws}/git/push-all", handleGitPushAll(app.config.GitOps))
		wsMux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/git/push", handleGitPush(app.config.GitOps))
		wsMux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/git/pull", handleGitPull(app.config.GitOps))
		wsMux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/git/sync", handleGitSync(app.config.GitOps))
		wsMux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/git/pr", handleGitPR(app.config.GitOps))
		wsMux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/git/reset", handleGitReset(app.config.GitOps))
		wsMux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/git/status", handleGitStatus(app.config.GitOps))
		wsMux.HandleFunc("PATCH /api/workspaces/{ws}/agents/{name}/git/target", handleGitTargetUpdate(app.config.GitOps))
		wsMux.HandleFunc("GET /api/workspaces/{ws}/issues/{id}/git/diff-stat", handleGetIssueDiffStat(app.multiPool, app.config.GitOps))
		wsMux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/git/diff-stat", handleAgentDiffStat(app.config.GitOps))

		// Diff endpoints (workspace-scoped)
		wsMux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/diff/commits", handleDiffCommits(app.config.GitOps))
		wsMux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/diff/files", handleDiffFiles(app.config.GitOps))
		wsMux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/diff/file", handleDiffFile(app.config.GitOps))
	}

	// File operations (workspace-scoped)
	if app.config.FileOps != nil {
		wsMux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/files/tree", handleFileTree(app.config.FileOps))
		wsMux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/files", handleFileRead(app.config.FileOps))
		wsMux.HandleFunc("PUT /api/workspaces/{ws}/agents/{name}/files", handleFileWrite(app.config.FileOps))
	}

	// Apply WorkspaceMiddleware to all workspace-scoped routes
	mux.Handle("/api/workspaces/{ws}/", WorkspaceMiddleware(app.wsExistsFn, wsMux))
}

// fleetWSHandler resolves a per-workspace fleet Store from the registry and
// delegates to the given handler factory. Returns 404 if the workspace is not
// found in the fleet registry.
func fleetWSHandler(registry *fleet.StoreRegistry, makeHandler func(*fleet.Store) http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := WorkspaceFromContext(r.Context())
		store, ok := registry.Get(wsID)
		if !ok {
			respondJSON(w, http.StatusServiceUnavailable, map[string]any{
				"success": false,
				"error":   "fleet not configured for workspace",
			})
			return
		}
		makeHandler(store).ServeHTTP(w, r)
	}
}

package webui

import (
	"net/http"
	"time"

	"golang.org/x/time/rate"

	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
	"github.com/tysonthomas9/loomcli/internal/webui/editor"
	"github.com/tysonthomas9/loomcli/internal/webui/fleet"
	"github.com/tysonthomas9/loomcli/internal/webui/issuetabs"
	"github.com/tysonthomas9/loomcli/internal/webui/sessionhistory"
	"github.com/tysonthomas9/loomcli/internal/webui/tabmeta"
)

// setupRoutes configures all HTTP routes for the server.
// allowedOrigins is the list of allowed CORS origins for WebSocket validation.
func setupRoutes(mux *http.ServeMux, pool daemon.Pool, multiPool *daemon.MultiPool, hub *SSEHub, getMutationsSince func(wsID string, since int64) []rpc.MutationEvent, termManager *TerminalManager, termAuth *terminalAuth, fleetRegistry *fleet.StoreRegistry, tokenCfg *TokenConfig, apiKey string, authEnabled bool, allowedOrigins []string, fleetRegCfg *FleetRegisterConfig, claimMetrics *fleet.ClaimMetrics, fleetEnabled bool, devMode bool, devFrontendDir string, loomServerURL string, gitOps GitOps, fileOps FileOps, tabMetaStore *tabmeta.Store, issueTabStore *issuetabs.Store, workspaceConfigFn func() (*WorkspaceData, error), workspaceDeleteFn func(string) error, setDefaultWsFn func(string) error, clearDefaultWsFn func() error, workspaceCreateFn WorkspaceCreateFn, backendOps BackendOps, sessionHistoryStore *sessionhistory.Store, sessStore *sessions.Store, wsExistsFn func(string) bool, initialWorkspaceID string, workspaceConfigByIDFn func(string) (*WorkspaceData, error), extAuthURL string) (*clientErrorLimiter, *cspReportLimiter, *authConfigLimiter) { //nolint:funlen // route registration function
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

	// Auth mode discovery endpoint (public, rate-limited — called once per page load).
	// extAuthURL is the Better Auth service base URL for OAuth redirects, not JWKS.
	authCfgLimiter := newAuthConfigLimiter(rate.Limit(5), 10, 5*time.Minute, 10*time.Minute)
	mux.HandleFunc("GET /api/config", handleAuthConfig(extAuthURL, authCfgLimiter))

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
	var getFleetTimeouts func() int64
	if fleetRegistry != nil {
		getFleetTimeouts = fleetRegistry.GetTotalTimeoutCount
	}
	mux.HandleFunc("GET /api/metrics", handleMetrics(hub, getFleetTimeouts, claimMetrics))

	// Daemon status endpoint - exposes daemon configuration (auto-commit, auto-push, etc.)
	mux.HandleFunc("GET /api/daemon/status", handleDaemonStatus(pool))

	// Backend configuration endpoints
	mux.HandleFunc("GET /api/config/backend", handleGetBackendConfig(pool))
	mux.HandleFunc("PATCH /api/config/backend", handlePatchBackendConfig(pool))

	// Workspace CRUD endpoints are registered in registerWorkspaceRoutes below.

	// Backend health endpoint
	if backendOps != nil {
		mux.HandleFunc("GET /api/backends", handleGetBackendsHealth(backendOps))
	}

	// Fleet endpoints for worker registration, task acquisition, and completion.
	// Legacy routes fall back to the initial workspace's Store. Workspace-scoped
	// routes are registered in registerWorkspaceRoutes below.
	if fleetEnabled && fleetRegistry != nil {
		initialStore, _ := fleetRegistry.Get(initialWorkspaceID)
		mux.HandleFunc("POST /api/fleet/register", handleFleetRegister(initialStore, tokenCfg, fleetRegCfg))
		// Legacy claim injects initialWorkspaceID so multiPool.Get(ctx) resolves correctly.
		legacyClaim := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := WithWorkspace(r.Context(), initialWorkspaceID)
			handleFleetClaim(multiPool, claimMetrics).ServeHTTP(w, r.WithContext(ctx))
		})
		if tokenCfg != nil && len(tokenCfg.SigningKey) > 0 {
			fleetAuth := NewFleetAuthMiddleware(tokenCfg.SigningKey)
			mux.Handle("POST /api/fleet/claim", fleetAuth(legacyClaim))
		} else {
			mux.Handle("POST /api/fleet/claim", legacyClaim)
		}
		mux.HandleFunc("POST /api/fleet/done/{id}", handleFleetDone(initialStore))
		if tokenCfg != nil && len(tokenCfg.SigningKey) > 0 {
			fleetAuth := NewFleetAuthMiddleware(tokenCfg.SigningKey)
			mux.Handle("POST /api/fleet/heartbeat", fleetAuth(handleFleetHeartbeat(initialStore)))
		} else {
			mux.HandleFunc("POST /api/fleet/heartbeat", handleFleetHeartbeat(initialStore))
		}
	}

	// Legacy SSE endpoint removed — SSE is now workspace-scoped at /api/workspaces/{ws}/events

	// Loom proxy for agent status endpoints (same-origin to avoid CORS/CSP issues)
	if loomProxy := newLoomProxy(loomServerURL); loomProxy != nil {
		mux.Handle("/api/loom/", loomProxy)
	}

	// Terminal token and WebSocket endpoints for authenticated terminal relay
	if termManager != nil {
		mux.HandleFunc("GET /api/terminal/sessions", handleListTerminalSessions(termManager))
		if termAuth != nil {
			mux.HandleFunc("GET /api/terminal/token", handleTerminalToken(termAuth))
		}
		mux.HandleFunc("GET /api/terminal/ws", handleTerminalWS(termManager, termAuth, allowedOrigins, loomServerURL, workspaceConfigFn, tabMetaStore, hub))
		mux.HandleFunc("POST /api/terminal/restart", handleTerminalRestart(termManager, pool, termAuth))
		mux.HandleFunc("POST /api/terminal/kill", handleTerminalKill(termManager, termAuth))
		mux.HandleFunc("GET /api/terminal/session-status", handleTerminalSessionStatus(termManager, termAuth))
		mux.HandleFunc("POST /api/terminal/spawn", handleTerminalSpawn(termManager, sessionHistoryStore, initialWorkspaceID))
		mux.HandleFunc("POST /api/terminal/sessions/{name}/seed", handleSeedTerminalSession(termManager))
		mux.HandleFunc("GET /api/terminal/sessions/{session}/scrollback", handleGetScrollback(termManager))
		mux.HandleFunc("GET /api/terminal/sessions/{session}/export", handleExportSession(termManager))
		mux.HandleFunc("GET /api/terminal/sessions/{session}/scrollback-info", handleScrollbackInfo(termManager))
		mux.HandleFunc("POST /api/terminal/sessions/{session}/kill", handleScheduleSessionKill(termManager))
		mux.HandleFunc("POST /api/terminal/sessions/close-all", handleCloseAllSessions(termManager, tabMetaStore, hub))

		// Note: Terminal tab metadata endpoints have moved to workspace-scoped routes
		// in registerWorkspaceRoutes. The by-issue endpoint is also workspace-scoped.

		// Terminal UI state endpoints (Redis-backed active tab persistence)
		if tabMetaStore != nil {
			rc := tabMetaStore.RedisClient()
			mux.HandleFunc("GET /api/terminal/state", handleGetTerminalState(rc))
			mux.HandleFunc("PATCH /api/terminal/state", handlePatchTerminalState(rc))
		}
	}

	// Issue tab persistence and session history endpoints have moved to
	// workspace-scoped routes in registerWorkspaceRoutes (T46).

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

	// Session audit trail endpoints — registered unconditionally; handlers return 503 when sessStore is nil
	mux.HandleFunc("GET /api/tasks/{taskId}/sessions", handleListTaskSessions(sessStore))
	mux.HandleFunc("GET /api/tasks/{taskId}/sessions/{sessionId}", handleGetSession(sessStore))
	mux.HandleFunc("GET /api/tasks/{taskId}/sessions/{sessionId}/transcript", handleGetSessionTranscript(sessStore))
	mux.HandleFunc("GET /api/tasks/{taskId}/sessions/{sessionId}/diff", handleGetSessionDiff(sessStore))

	// Session change notification endpoint for local agents to push SSE events
	if hub != nil {
		mux.HandleFunc("POST /api/sessions/notify", handleNotifySessionChange(hub))
	}

	// Workspace management and workspace-scoped API routes
	if multiPool != nil {
		registerWorkspaceRoutes(mux, multiPool, workspaceConfigFn, wsExistsFn, workspaceConfigByIDFn, tabMetaStore, issueTabStore, sessionHistoryStore, termManager, termAuth, allowedOrigins, hub, getMutationsSince, fleetRegistry, tokenCfg, fleetRegCfg, claimMetrics, gitOps, fileOps, workspaceDeleteFn, setDefaultWsFn, clearDefaultWsFn, workspaceCreateFn)
	}

	// Static file serving with SPA routing (must be last - catches all paths)
	if devMode {
		mux.Handle("/", devFrontendHandler(devFrontendDir))
	} else {
		mux.Handle("/", frontendHandler())
	}

	return clientErrLimiter, cspLimiter, authCfgLimiter
}

// registerWorkspaceRoutes sets up workspace listing, CRUD, and workspace-scoped API routes.
func registerWorkspaceRoutes(mux *http.ServeMux, multiPool *daemon.MultiPool, workspaceConfigFn func() (*WorkspaceData, error), wsExistsFn func(string) bool, workspaceConfigByIDFn func(string) (*WorkspaceData, error), tabMetaStore *tabmeta.Store, issueTabStore *issuetabs.Store, sessionHistoryStore *sessionhistory.Store, termManager *TerminalManager, termAuth *terminalAuth, allowedOrigins []string, hub *SSEHub, getMutationsSince func(wsID string, since int64) []rpc.MutationEvent, fleetRegistry *fleet.StoreRegistry, tokenCfg *TokenConfig, fleetRegCfg *FleetRegisterConfig, claimMetrics *fleet.ClaimMetrics, gitOps GitOps, fileOps FileOps, workspaceDeleteFn func(string) error, setDefaultWsFn func(string) error, clearDefaultWsFn func() error, workspaceCreateFn WorkspaceCreateFn) { //nolint:funlen // route registration function
	// Active workspace endpoint — returns full topology for the default workspace
	mux.HandleFunc("GET /api/workspaces/active", handleActiveWorkspace(workspaceConfigFn))

	// Workspace listing (not workspace-scoped themselves)
	mux.HandleFunc("GET /api/workspaces", handleListWorkspaces(multiPool, workspaceConfigFn))
	mux.HandleFunc("GET /api/workspaces/{ws}", handleGetWorkspace(wsExistsFn, workspaceConfigByIDFn))

	// Global workspace CRUD operations (no WorkspaceMiddleware)
	mux.HandleFunc("POST /api/workspaces", handleWorkspaceCreate(workspaceCreateFn, workspaceConfigFn))
	mux.HandleFunc("PUT /api/workspaces/order", handleWorkspaceReorder(workspaceConfigFn))
	mux.HandleFunc("PUT /api/workspaces/default", handleSetDefaultWorkspace(setDefaultWsFn, workspaceConfigFn))
	mux.HandleFunc("DELETE /api/workspaces/default", handleClearDefaultWorkspace(clearDefaultWsFn, workspaceConfigFn))

	// Per-workspace DELETE — registered on main mux with manual middleware wrapping
	// because DELETE /api/workspaces/{ws} (no trailing slash) won't match the
	// wsMux prefix handler at /api/workspaces/{ws}/.
	mux.Handle("DELETE /api/workspaces/{ws}", WorkspaceMiddleware(wsExistsFn, handleWorkspaceDelete(workspaceDeleteFn, workspaceConfigFn)))

	// Workspace-scoped API routes via a sub-mux with WorkspaceMiddleware.
	// The middleware injects the workspace ID into the context so that
	// multiPool.Get(ctx) routes to the correct per-workspace pool.
	wsMux := http.NewServeMux()

	// Per-workspace CRUD (through WorkspaceMiddleware via wsMux)
	wsMux.HandleFunc("PATCH /api/workspaces/{ws}/name", handleWorkspaceRename(workspaceConfigFn))

	// Issue endpoints
	wsMux.HandleFunc("GET /api/workspaces/{ws}/issues/{id}", handleGetIssue(multiPool))
	wsMux.HandleFunc("GET /api/workspaces/{ws}/issues", handleListIssues(multiPool))
	wsMux.HandleFunc("POST /api/workspaces/{ws}/issues", handleCreateIssue(multiPool))
	wsMux.HandleFunc("PATCH /api/workspaces/{ws}/issues/{id}", handlePatchIssue(multiPool))
	wsMux.HandleFunc("POST /api/workspaces/{ws}/issues/{id}/close", handleCloseIssue(multiPool))
	wsMux.HandleFunc("POST /api/workspaces/{ws}/issues/{id}/move", handleMoveIssue(multiPool, multiPool, workspaceConfigFn))
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

	// Log streaming endpoints (workspace-scoped)
	wsMux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/logs", handleGetAgentLog())
	wsMux.HandleFunc("GET /api/workspaces/{ws}/tasks/{id}/logs", handleListTaskPhases())
	wsMux.HandleFunc("GET /api/workspaces/{ws}/tasks/{id}/logs/{phase}", handleGetTaskLog())

	// Server-Sent Events endpoint (workspace-scoped)
	if hub != nil {
		wsMux.HandleFunc("GET /api/workspaces/{ws}/events", handleSSE(hub, getMutationsSince))
	}

	// Terminal tab metadata endpoints (workspace-scoped, Redis-backed)
	if tabMetaStore != nil {
		wsMux.HandleFunc("GET /api/workspaces/{ws}/terminal/tabs", handleListTerminalTabs(tabMetaStore, termManager))
		wsMux.HandleFunc("GET /api/workspaces/{ws}/terminal/tabs/{session}", handleGetTerminalTab(tabMetaStore))
		wsMux.HandleFunc("PUT /api/workspaces/{ws}/terminal/tabs/{session}", handlePutTerminalTab(tabMetaStore, hub))
		wsMux.HandleFunc("PATCH /api/workspaces/{ws}/terminal/tabs/{session}", handlePatchTerminalTab(tabMetaStore, hub))
		wsMux.HandleFunc("DELETE /api/workspaces/{ws}/terminal/tabs/{session}", handleDeleteTerminalTab(tabMetaStore, hub))
		// Cross-workspace endpoint: the workspace in the URL is for auth context,
		// but ListByIssue searches across all workspaces intentionally.
		wsMux.HandleFunc("GET /api/workspaces/{ws}/terminal/sessions/by-issue", handleListSessionsByIssue(tabMetaStore))
	}

	// Issue tab persistence endpoints (Redis-backed, workspace-scoped)
	if issueTabStore != nil {
		wsMux.HandleFunc("GET /api/workspaces/{ws}/issues/{issueId}/tabs", handleGetIssueTabs(issueTabStore, termManager))
		wsMux.HandleFunc("PUT /api/workspaces/{ws}/issues/{issueId}/tabs", handleSaveIssueTabs(issueTabStore, hub))
		wsMux.HandleFunc("DELETE /api/workspaces/{ws}/issues/{issueId}/tabs", handleDeleteIssueTabs(issueTabStore))
	}

	// Session history endpoints (Redis-backed audit trail, workspace-scoped)
	if sessionHistoryStore != nil {
		wsMux.HandleFunc("GET /api/workspaces/{ws}/issues/{issueId}/sessions", handleListSessionHistory(sessionHistoryStore))
		wsMux.HandleFunc("GET /api/workspaces/{ws}/issues/{issueId}/sessions/{recordId}/scrollback", handleGetSessionScrollback(sessionHistoryStore))
	}

	// Agent terminal endpoints (workspace-scoped)
	if termManager != nil {
		wsMux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/terminal/info", handleGetAgentTerminalInfo(termManager))
		if termAuth != nil {
			wsMux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/terminal/token", handleGetAgentTerminalToken(termAuth))
		}
		wsMux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/terminal/ws", handleAgentTerminalWS(termManager, termAuth, allowedOrigins))
	}

	// Workspace-scoped fleet routes. Claim uses multiPool (routes to correct
	// workspace daemon); register/done/heartbeat resolve the Store per-request.
	if fleetRegistry != nil {
		wsMux.HandleFunc("POST /api/workspaces/{ws}/fleet/register",
			fleetWSHandler(fleetRegistry, func(s *fleet.Store) http.HandlerFunc {
				return handleFleetRegister(s, tokenCfg, fleetRegCfg)
			}))
		if tokenCfg != nil && len(tokenCfg.SigningKey) > 0 {
			fleetAuth := NewFleetAuthMiddleware(tokenCfg.SigningKey)
			wsMux.Handle("POST /api/workspaces/{ws}/fleet/claim",
				fleetAuth(handleFleetClaim(multiPool, claimMetrics)))
		} else {
			wsMux.HandleFunc("POST /api/workspaces/{ws}/fleet/claim",
				handleFleetClaim(multiPool, claimMetrics))
		}
		wsMux.HandleFunc("POST /api/workspaces/{ws}/fleet/done/{id}",
			fleetWSHandler(fleetRegistry, handleFleetDone))
		if tokenCfg != nil && len(tokenCfg.SigningKey) > 0 {
			fleetAuth := NewFleetAuthMiddleware(tokenCfg.SigningKey)
			wsMux.Handle("POST /api/workspaces/{ws}/fleet/heartbeat",
				fleetAuth(fleetWSHandler(fleetRegistry, handleFleetHeartbeat)))
		} else {
			wsMux.HandleFunc("POST /api/workspaces/{ws}/fleet/heartbeat",
				fleetWSHandler(fleetRegistry, handleFleetHeartbeat))
		}
	}

	// Git operations (workspace-scoped)
	if gitOps != nil {
		wsMux.HandleFunc("POST /api/workspaces/{ws}/git/push-all", handleGitPushAll(gitOps))
		wsMux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/git/push", handleGitPush(gitOps))
		wsMux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/git/pull", handleGitPull(gitOps))
		wsMux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/git/sync", handleGitSync(gitOps))
		wsMux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/git/pr", handleGitPR(gitOps))
		wsMux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/git/reset", handleGitReset(gitOps))
		wsMux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/git/status", handleGitStatus(gitOps))
		wsMux.HandleFunc("PATCH /api/workspaces/{ws}/agents/{name}/git/target", handleGitTargetUpdate(gitOps))
		wsMux.HandleFunc("GET /api/workspaces/{ws}/issues/{id}/git/diff-stat", handleGetIssueDiffStat(multiPool, gitOps))

		// Diff endpoints (workspace-scoped)
		wsMux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/diff/commits", handleDiffCommits(gitOps))
		wsMux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/diff/files", handleDiffFiles(gitOps))
		wsMux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/diff/file", handleDiffFile(gitOps))
	}

	// File operations (workspace-scoped)
	if fileOps != nil {
		wsMux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/files/tree", handleFileTree(fileOps))
		wsMux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/files", handleFileRead(fileOps))
		wsMux.HandleFunc("PUT /api/workspaces/{ws}/agents/{name}/files", handleFileWrite(fileOps))
	}

	// Apply WorkspaceMiddleware to all workspace-scoped routes
	mux.Handle("/api/workspaces/{ws}/", WorkspaceMiddleware(wsExistsFn, wsMux))
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

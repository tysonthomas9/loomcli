package webui

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
	"github.com/tysonthomas9/loomcli/internal/webui/editor"
	"github.com/tysonthomas9/loomcli/internal/webui/fleet"
	"github.com/tysonthomas9/loomcli/internal/webui/tabmeta"
)

// setupRoutes configures all HTTP routes for the server.
// allowedOrigins is the list of allowed CORS origins for WebSocket validation.
func setupRoutes(mux *http.ServeMux, pool daemon.Pool, hub *SSEHub, getMutationsSince func(since int64) []rpc.MutationEvent, termManager *TerminalManager, termAuth *terminalAuth, fleetStore *fleet.Store, tokenCfg *TokenConfig, apiKey string, authEnabled bool, allowedOrigins []string, fleetRegCfg *FleetRegisterConfig, timeoutEnforcer *fleet.TimeoutEnforcer, claimMetrics *fleet.ClaimMetrics, fleetEnabled bool, devMode bool, devFrontendDir string, loomServerURL string, gitOps GitOps, fileOps FileOps, tabMetaStore *tabmeta.Store, workspaceConfigFn func() (*WorkspaceData, error), backendListFn func() ([]BackendInfo, error)) { //nolint:funlen // route registration function
	// Health check endpoint for load balancers and monitoring
	mux.HandleFunc("GET /health", handleHealth(pool))

	// API health endpoint that reports daemon connection status
	mux.HandleFunc("GET /api/health", handleAPIHealth(pool))

	// Auth token bootstrap endpoint (same-origin only)
	if authEnabled {
		mux.HandleFunc("GET /api/auth/token", handleAuthToken(apiKey))
	}

	// Stats endpoint for project statistics
	mux.HandleFunc("GET /api/stats", handleStats(pool))

	// SSE hub metrics endpoint
	mux.HandleFunc("GET /api/metrics", handleMetrics(hub, timeoutEnforcer, claimMetrics))

	// Daemon status endpoint - exposes daemon configuration (auto-commit, auto-push, etc.)
	mux.HandleFunc("GET /api/daemon/status", handleDaemonStatus(pool))

	// Backend configuration endpoints
	mux.HandleFunc("GET /api/config/backend", handleGetBackendConfig(pool))
	mux.HandleFunc("PATCH /api/config/backend", handlePatchBackendConfig(pool))

	// Workspace topology endpoint
	mux.HandleFunc("GET /api/workspace", handleWorkspace(workspaceConfigFn))

	// Backend registry endpoint
	mux.HandleFunc("GET /api/backends", handleGetBackends(backendListFn))

	// Issue endpoints
	mux.HandleFunc("GET /api/issues/{id}", handleGetIssue(pool))
	mux.HandleFunc("GET /api/issues", handleListIssues(pool))
	mux.HandleFunc("POST /api/issues", handleCreateIssue(pool))
	mux.HandleFunc("PATCH /api/issues/{id}", handlePatchIssue(pool))
	mux.HandleFunc("POST /api/issues/{id}/close", handleCloseIssue(pool))
	mux.HandleFunc("DELETE /api/issues/{id}", handleDeleteIssue(pool))
	mux.HandleFunc("POST /api/issues/{id}/comments", handleAddComment(pool))

	// Dependency management endpoints
	mux.HandleFunc("POST /api/issues/{id}/dependencies", handleAddDependency(pool))
	mux.HandleFunc("DELETE /api/issues/{id}/dependencies/{depId}", handleRemoveDependency(pool))

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

	// Ready endpoint for issues ready to work on
	mux.HandleFunc("GET /api/ready", handleReady(pool))

	// Blocked endpoint for issues with blocking dependencies
	mux.HandleFunc("GET /api/blocked", handleBlocked(pool))

	// Graph endpoint for dependency visualization
	mux.HandleFunc("GET /api/issues/graph", handleGraph(pool))

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
		mux.HandleFunc("GET /api/terminal/ws", handleTerminalWS(termManager, termAuth, allowedOrigins, loomServerURL, workspaceConfigFn))
		mux.HandleFunc("GET /api/agents/{name}/terminal/ws", handleAgentTerminalWS(termManager, termAuth, allowedOrigins))
		mux.HandleFunc("GET /api/agents/{name}/terminal/info", handleGetAgentTerminalInfo(termManager))
		mux.HandleFunc("POST /api/terminal/restart", handleTerminalRestart(termManager, pool, termAuth))
		mux.HandleFunc("POST /api/terminal/spawn", handleTerminalSpawn(termManager))
		mux.HandleFunc("POST /api/terminal/sessions/{name}/seed", handleSeedTerminalSession(termManager))
		mux.HandleFunc("POST /api/terminal/sessions/{session}/kill", handleScheduleSessionKill(termManager))

		// Terminal tab metadata endpoints (Redis-backed persistence)
		mux.HandleFunc("GET /api/terminal/tabs", handleListTerminalTabs(tabMetaStore, termManager))
		mux.HandleFunc("GET /api/terminal/tabs/{session}", handleGetTerminalTab(tabMetaStore))
		mux.HandleFunc("PUT /api/terminal/tabs/{session}", handlePutTerminalTab(tabMetaStore, hub))
		mux.HandleFunc("PATCH /api/terminal/tabs/{session}", handlePatchTerminalTab(tabMetaStore, hub))
		mux.HandleFunc("DELETE /api/terminal/tabs/{session}", handleDeleteTerminalTab(tabMetaStore, hub))
	}

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

	// Static file serving with SPA routing (must be last - catches all paths)
	if devMode {
		mux.Handle("/", devFrontendHandler(devFrontendDir))
	} else {
		mux.Handle("/", frontendHandler())
	}
}

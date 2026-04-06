package webui

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/webui/fleet"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// registerRoutes maps URL patterns to pre-built handler fields on the Server.
// Called from NewServer after buildHandlers().
func (app *Server) registerRoutes() {
	// Health check endpoint for load balancers and monitoring
	app.mux.HandleFunc("GET /health", app.healthHandler)

	// API health endpoint that reports daemon connection status
	app.mux.HandleFunc("GET /api/health", app.apiHealthHandler)

	// Client-side error reporting endpoint (has its own per-IP rate limiter, 10 req/min/IP)
	app.mux.HandleFunc("POST /api/client-errors", app.clientErrorsHandler)

	// CSP violation reporting endpoint (has its own per-IP rate limiter, 60 req/min/IP)
	app.mux.HandleFunc("POST /api/csp-report", app.cspReportHandler)

	// Auth mode discovery endpoint (public, rate-limited — called once per page load).
	app.mux.HandleFunc("GET /api/config", app.authConfigHandler)

	// Stats endpoint for project statistics (workspace-aware when multiPool available)
	app.mux.HandleFunc("GET /api/stats", app.statsHandler)

	// SSE hub metrics endpoint
	app.mux.HandleFunc("GET /api/metrics", app.metricsHandler)

	// Daemon status endpoint - exposes daemon configuration (auto-commit, auto-push, etc.)
	app.mux.HandleFunc("GET /api/daemon/status", app.daemonStatusHandler)

	// Backend configuration endpoints
	app.mux.HandleFunc("GET /api/config/backend", app.getBackendConfigHandler)
	app.mux.HandleFunc("PATCH /api/config/backend", app.patchBackendConfigHandler)

	// Workspace CRUD endpoints are registered in registerWorkspaceRoutes below.

	// Backend health endpoint
	if app.getBackendsHealthHandler != nil {
		app.mux.HandleFunc("GET /api/backends", app.getBackendsHealthHandler)
	}

	// Fleet endpoints: workspace-scoped routes only (flat routes removed).
	// Workspace-scoped fleet routes are registered in registerWorkspaceRoutes below.

	// Legacy SSE endpoint removed — SSE is now workspace-scoped at /api/workspaces/{ws}/events

	// Loom proxy for agent status endpoints (same-origin to avoid CORS/CSP issues)
	if app.loomProxy != nil {
		app.mux.Handle("/api/loom/", app.loomProxy)
	}

	// Terminal endpoints: workspace-scoped routes only (flat routes removed).
	// All terminal routes are registered in registerWorkspaceRoutes below.

	// Editor endpoints for external editor detection and launch
	app.mux.HandleFunc("GET /api/editors", app.listEditorsHandler)
	app.mux.HandleFunc("POST /api/editors/open", app.openEditorHandler)

	// Session change notification endpoint for local agents to push SSE events
	if app.notifySessionChangeHandler != nil {
		app.mux.HandleFunc("POST "+sessions.NotifyPath, app.notifySessionChangeHandler)
	}

	// Workspace management and workspace-scoped API routes
	if app.multiPool != nil {
		app.registerWorkspaceRoutes()
	}

	// Static file serving with SPA routing (must be last - catches all paths)
	app.mux.Handle("/", app.frontendH)
}

// registerWorkspaceRoutes sets up workspace listing, CRUD, and workspace-scoped API routes.
func (app *Server) registerWorkspaceRoutes() {
	// Active workspace endpoint — returns full topology for the default workspace
	app.mux.HandleFunc("GET /api/workspaces/active", handleActiveWorkspace(app.workspaceSvc))

	// Workspace listing (not workspace-scoped themselves)
	app.mux.HandleFunc("GET /api/workspaces", handleListWorkspaces(app.workspaceSvc))
	app.mux.HandleFunc("GET /api/workspaces/{ws}", handleGetWorkspace(app.workspaceSvc))

	// Global workspace CRUD operations (no WorkspaceMiddleware)
	app.mux.HandleFunc("POST /api/workspaces", handleWorkspaceCreate(app.workspaceSvc))

	// Workspace job polling endpoint (literal "jobs" segment wins over {ws} wildcard)
	app.mux.HandleFunc("GET /api/workspaces/jobs/{id}", handleGetWorkspaceJob(app.workspaceSvc))
	app.mux.HandleFunc("PUT /api/workspaces/order", handleWorkspaceReorder(app.workspaceSvc))
	app.mux.HandleFunc("PUT /api/workspaces/default", handleSetDefaultWorkspace(app.workspaceSvc))
	app.mux.HandleFunc("DELETE /api/workspaces/default", handleClearDefaultWorkspace(app.workspaceSvc))

	// Per-workspace DELETE — registered on main mux with manual middleware wrapping
	// because DELETE /api/workspaces/{ws} (no trailing slash) won't match the
	// wsMux prefix handler at /api/workspaces/{ws}/.
	app.mux.Handle("DELETE /api/workspaces/{ws}", middleware.Workspace(app.wsExistsFn)(handleWorkspaceDelete(app.workspaceSvc)))

	// Workspace-scoped API routes via a sub-mux with WorkspaceMiddleware.
	// The middleware injects the workspace ID into the context so that
	// multiPool.Get(ctx) routes to the correct per-workspace pool.
	wsMux := http.NewServeMux()

	for _, mod := range app.wsModules {
		mod.Register(wsMux)
	}

	// Apply WorkspaceMiddleware to all workspace-scoped routes
	app.mux.Handle("/api/workspaces/{ws}/", middleware.Workspace(app.wsExistsFn)(wsMux))
}

// fleetWSHandler resolves a per-workspace fleet Store via the provided lookup
// function and delegates to the given handler factory. Returns 503 if the
// workspace is not found in the fleet registry.
func fleetWSHandler(getStore func(string) (*fleet.Store, bool), makeHandler func(*fleet.Store) http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		store, ok := getStore(wsID)
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

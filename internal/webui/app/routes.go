package app

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/handlermux"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// registerRoutes maps URL patterns to pre-built handler fields on the Server.
// Called from NewServer after buildHandlers().
func (app *Server) registerRoutes() {
	h := app.handlers
	app.registerCoreAPIRoutes(h)
	app.registerDaemonRoutes(h)
	app.registerMonitorHandlers()
	app.registerEditorAndNotifyRoutes(h)

	// Workspace management and workspace-scoped API routes
	if app.multiPool != nil {
		app.registerWorkspaceRoutes()
	}

	// Static file serving with SPA routing (must be last - catches all paths)
	if !app.config.SkipFrontend {
		app.mux.Handle("/", app.frontendH)
	}
}

// registerCoreAPIRoutes registers health, config, stats, and error reporting endpoints.
func (app *Server) registerCoreAPIRoutes(h *handlermux.Handlers) {
	app.mux.HandleFunc("GET /health", h.Health)
	app.mux.HandleFunc("GET /api/health", h.APIHealth)
	app.mux.HandleFunc("POST /api/client-errors", h.ClientErrors)
	app.mux.HandleFunc("POST /api/csp-report", h.CSPReport)
	app.mux.HandleFunc("GET /api/config", h.AuthConfig)
	app.mux.HandleFunc("GET /api/stats", h.Stats)
	app.mux.HandleFunc("GET /api/metrics", h.Metrics)
	app.mux.HandleFunc("GET /api/config/backend", h.GetBackendConfig)
	app.mux.HandleFunc("PATCH /api/config/backend", h.PatchBackendConfig)
	if h.GetBackendsHealth != nil {
		app.mux.HandleFunc("GET /api/backends", h.GetBackendsHealth)
	}
}

// registerDaemonRoutes registers daemon status, supervisor, and config endpoints.
func (app *Server) registerDaemonRoutes(h *handlermux.Handlers) {
	app.mux.HandleFunc("GET /api/daemon/status", h.DaemonStatus)
	if h.DaemonSupervisor != nil {
		app.mux.HandleFunc("GET /api/daemon/supervisor", h.DaemonSupervisor)
	}
	if h.DaemonConfig != nil {
		app.mux.HandleFunc("GET /api/daemon/config", h.DaemonConfig)
	}
}

// registerEditorAndNotifyRoutes registers editor and session notification endpoints.
func (app *Server) registerEditorAndNotifyRoutes(h *handlermux.Handlers) {
	app.mux.HandleFunc("GET /api/editors", h.ListEditors)
	app.mux.HandleFunc("POST /api/editors/open", h.OpenEditor)
	if h.NotifySessionChange != nil {
		app.mux.HandleFunc("POST /api/sessions/notify", h.NotifySessionChange)
	}
}

// registerMonitorHandlers registers monitor/metrics/observability handlers
// injected from the cli package via ServerConfig.MonitorHandlers.
func (app *Server) registerMonitorHandlers() {
	mh := app.config.MonitorHandlers
	if mh.Status != nil {
		app.mux.HandleFunc("GET /api/monitor/status", mh.Status)
	}
	if mh.Agents != nil {
		app.mux.HandleFunc("GET /api/monitor/agents", mh.Agents)
	}
	if mh.Tasks != nil {
		app.mux.HandleFunc("GET /api/monitor/tasks", mh.Tasks)
	}
	if mh.Stats != nil {
		app.mux.HandleFunc("GET /api/monitor/stats", mh.Stats)
	}
	if mh.Sync != nil {
		app.mux.HandleFunc("GET /api/monitor/sync", mh.Sync)
	}
	if mh.Workspaces != nil {
		app.mux.HandleFunc("GET /api/monitor/workspaces", mh.Workspaces)
	}
	if mh.StaleDetector != nil {
		app.mux.HandleFunc("GET /api/monitor/stale-detector", mh.StaleDetector)
	}
	if mh.Usage != nil {
		app.mux.HandleFunc("GET /api/monitor/usage", mh.Usage)
	}
	if mh.Metrics != nil {
		app.mux.HandleFunc("GET /metrics", mh.Metrics)
	}
	if mh.ObservabilityMetrics != nil {
		app.mux.HandleFunc("GET /api/observability/metrics", mh.ObservabilityMetrics)
	}
	if mh.ObservabilityEvents != nil {
		app.mux.HandleFunc("GET /api/observability/events", mh.ObservabilityEvents)
	}
}

// registerWorkspaceRoutes sets up workspace listing, CRUD, and workspace-scoped API routes.
func (app *Server) registerWorkspaceRoutes() {
	app.mux.HandleFunc("GET /api/workspaces/active", handlermux.HandleActiveWorkspace(app.workspaceSvc))
	app.mux.HandleFunc("GET /api/workspaces", handlermux.HandleListWorkspaces(app.workspaceSvc))
	app.mux.HandleFunc("GET /api/workspaces/{ws}", handlermux.HandleGetWorkspace(app.workspaceSvc))
	app.mux.HandleFunc("POST /api/workspaces", handlermux.HandleWorkspaceCreate(app.workspaceSvc))
	app.mux.HandleFunc("GET /api/workspaces/jobs/{id}", handlermux.HandleGetWorkspaceJob(app.workspaceSvc))
	app.mux.HandleFunc("PUT /api/workspaces/order", handlermux.HandleWorkspaceReorder(app.workspaceSvc))
	app.mux.HandleFunc("PUT /api/workspaces/default", handlermux.HandleSetDefaultWorkspace(app.workspaceSvc))
	app.mux.HandleFunc("DELETE /api/workspaces/default", handlermux.HandleClearDefaultWorkspace(app.workspaceSvc))
	app.mux.Handle("DELETE /api/workspaces/{ws}", middleware.Workspace(app.wsExistsFn)(handlermux.HandleWorkspaceDelete(app.workspaceSvc)))

	wsMux := http.NewServeMux()
	for _, mod := range app.wsModules {
		mod.Register(wsMux)
	}
	app.mux.Handle("/api/workspaces/{ws}/", middleware.Workspace(app.wsExistsFn)(wsMux))
}

package app

import (
	"net/http"

	beadsbackend "github.com/tysonthomas9/loomcli/internal/backend/beads"
	"github.com/tysonthomas9/loomcli/internal/webui"
	"github.com/tysonthomas9/loomcli/internal/webui/handlermux"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// registerRoutes maps URL patterns to pre-built handler fields on the Server.
// Called from NewServer after buildHandlers().
func (app *Server) registerRoutes() {
	h := app.handlers
	app.registerCoreAPIRoutes(h)
	app.registerDaemonRoutes(h)
	app.registerMonitorHandlers()
	app.registerEditorAndNotifyRoutes(h)
	app.registerAuthProxy()

	// Workspace management and workspace-scoped API routes
	if app.multiPool != nil {
		app.registerWorkspaceRoutes()
	}

	// Unregistered /api/* paths return JSON 404. Must run after all specific
	// /api/... routes are registered so Go 1.22+ longest-match prefers real
	// handlers. When the embedded frontend is enabled, non-/api paths fall
	// through to the frontend handler below (which serves static assets and
	// SPA fallback). When --api-only or --frontend-url is set, frontendH is
	// nil and non-/api paths fall through to Go's default text 404.
	app.mux.Handle("/api/", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"not found"}`))
	}))

	if app.frontendH != nil {
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
	app.mux.HandleFunc("GET /api/metrics", h.Metrics)
	app.mux.HandleFunc("GET /api/config/backend", h.GetBackendConfig)
	app.mux.HandleFunc("PATCH /api/config/backend", h.PatchBackendConfig)
	app.mux.HandleFunc("GET /api/config/terminal", h.GetTerminalConfig)
	if h.GetBackendsHealth != nil {
		app.mux.HandleFunc("GET /api/backends", h.GetBackendsHealth)
	}
}

// registerDaemonRoutes is retained as a no-op hook for future server-wide
// daemon endpoints. The previous /api/daemon/{status,supervisor,config}
// routes were workspace-specific data served from the launch directory — they
// now live under /api/workspaces/{ws}/daemon/* and resolve the right
// per-workspace state file.
func (app *Server) registerDaemonRoutes(_ *handlermux.Handlers) {}

// registerAuthProxy forwards /api/auth/* to the external BetterAuth service.
// Makes auth cookies same-origin with the frontend, avoiding cross-site cookie
// restrictions that block SameSite cookies over HTTP.
func (app *Server) registerAuthProxy() {
	if proxy := webui.NewAuthProxy(app.config.ExtAuthURL, logger); proxy != nil {
		// Mount under the prefix pattern. Metrics will bucket all auth proxy
		// requests under "/api/auth/" — we deliberately do NOT use
		// PromRouteCaptureByPath here because BetterAuth paths may embed
		// opaque tokens or IDs (verify-email, callbacks), which would explode
		// metric label cardinality.
		app.mux.Handle("/api/auth/", proxy)
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
//
// Per-workspace data (status/tasks/stats/sync/usage/agents) lives under
// /api/workspaces/{ws}/monitor/* — the old un-scoped routes leaked the
// launch workspace's data into every per-workspace view. The only /api/
// monitor/* routes that survive here are genuinely server-wide:
// workspaces (topology), stale-detector (fleet health), and Prometheus.
func (app *Server) registerMonitorHandlers() {
	mh := app.config.MonitorHandlers
	if mh.Workspaces != nil {
		app.mux.HandleFunc("GET /api/monitor/workspaces", mh.Workspaces)
	}
	if mh.StaleDetector != nil {
		app.mux.HandleFunc("GET /api/monitor/stale-detector", mh.StaleDetector)
	}
	// /metrics serves both loom-specific monitor metrics and the auto-registered
	// Prometheus metrics (loom_http_requests_total, loom_http_request_duration_seconds).
	// Both write text/plain Prometheus format and can be concatenated for scraping.
	promHandler := webui.PromHandler()
	if mh.Metrics != nil {
		app.mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, r *http.Request) {
			mh.Metrics(w, r)
			promHandler.ServeHTTP(w, r)
		})
	} else {
		app.mux.Handle("GET /metrics", promHandler)
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
	// PATCH handlers are registered on the outer mux (not the nested wsMux)
	// because Go 1.22+ http.ServeMux has a bug where r.Body.Read() hangs for
	// PATCH requests routed through a nested mux via wildcard subtree pattern.
	app.mux.Handle("PATCH /api/workspaces/{ws}/name", middleware.Workspace(app.wsExistsFn)(handlermux.HandleWorkspaceRename(app.workspaceSvc)))
	app.mux.Handle("PATCH /api/workspaces/{ws}/config/backend", middleware.Workspace(app.wsExistsFn)(handlermux.HandleWorkspaceBackendPatch(app.workspaceSvc)))
	app.mux.Handle("PATCH /api/workspaces/{ws}/repos/{repo}/default-branch", middleware.Workspace(app.wsExistsFn)(handlermux.HandleRepoDefaultBranchPatch(app.workspaceSvc)))

	wsMux := http.NewServeMux()
	for _, mod := range app.wsModules {
		mod.Register(wsMux)
	}
	app.registerScopedMonitorAndDaemonRoutes(wsMux)
	// Mount workspace sub-mux with route pattern capture for metrics.
	// Go's ServeMux sets r.Pattern on an internal request copy, invisible to
	// the outer metrics middleware. We pre-resolve the pattern via Handler()
	// (a cheap trie lookup) and write it into the shared promRouteStore so
	// metrics show granular routes (e.g., /api/workspaces/{ws}/issues) instead
	// of the lumped prefix bucket.
	wsHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, pattern := wsMux.Handler(r)
		webui.SetPromRoutePattern(r.Context(), pattern)
		wsMux.ServeHTTP(w, r)
	})
	app.mux.Handle("/api/workspaces/{ws}/", middleware.Workspace(app.wsExistsFn)(wsHandler))
}

// registerScopedMonitorAndDaemonRoutes installs the workspace-scoped
// counterparts of /api/monitor/* routes onto wsMux so they inherit the
// middleware.Workspace wrapping that validates the {ws} path param and
// populates the request context. Daemon inspection routes
// (/api/workspaces/{ws}/daemon/*) live in DaemonModule and are wired through
// buildInfraModules() instead.
func (app *Server) registerScopedMonitorAndDaemonRoutes(wsMux *http.ServeMux) {
	// /monitor/agents is handled by ScopedMonitorHandlersFn below; it now
	// uses the same per-workspace CollectMonitorDataScoped path as the other
	// scoped routes. The global-collector + name-filter handler
	// (MonitorHandlers.AgentsScoped) returned empty agents for any workspace
	// that wasn't the one the global collector was initialized against.

	if app.config.ScopedMonitorHandlersFn == nil || app.multiPool == nil {
		return
	}
	pathFn := func(wsID string) string {
		return service.ResolveWorkspacePath(app.config.WorkspaceConfigFn, wsID)
	}
	poolFn := func(wsID string) beadsbackend.Pool {
		// daemon.Pool structurally satisfies beadsbackend.Pool; nil map
		// lookup returns a nil interface, which callers check before
		// invoking CollectMonitorDataScoped.
		p := app.multiPool.PoolForWorkspace(wsID)
		if p == nil {
			return nil
		}
		return p
	}
	for pattern, handler := range app.config.ScopedMonitorHandlersFn(pathFn, poolFn) {
		wsMux.Handle(pattern, handler)
	}
}

package app

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui"
	"github.com/tysonthomas9/loomcli/internal/webui/handlermux"
	locsettings "github.com/tysonthomas9/loomcli/internal/webui/handlers/localsettings"
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
	app.registerAuthProxy()

	// Workspace management and workspace-scoped API routes
	if app.multiPool != nil {
		app.registerWorkspaceRoutes()
	}

	// Unregistered /api/* paths return JSON 404. Must run after all specific
	// /api/... routes are registered so Go 1.22+ longest-match prefers real
	// handlers. Non-/api paths fall through to Go's default text 404 — the
	// frontend is served externally (reverse proxy / Vite preview), not by
	// this server.
	app.mux.Handle("/api/", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"not found"}`))
	}))
	app.registerFrontendRoutes()
}

// registerCoreAPIRoutes registers health, config, and error reporting endpoints.
func (app *Server) registerCoreAPIRoutes(h *handlermux.Handlers) {
	app.mux.HandleFunc("GET /health", h.Health)
	app.mux.HandleFunc("GET /api/health", h.APIHealth)
	app.mux.HandleFunc("POST /api/client-errors", h.ClientErrors)
	app.mux.HandleFunc("GET /api/config", h.AuthConfig)
	app.mux.HandleFunc("GET /api/metrics", h.Metrics)
	app.mux.HandleFunc("GET /api/config/terminal", h.GetTerminalConfig)
	if app.config.LocalSettingsDir != "" {
		app.mux.HandleFunc("GET /api/local/settings", locsettings.HandleGet(app.config.LocalSettingsDir))
		app.mux.HandleFunc("PATCH /api/local/settings", locsettings.HandlePatch(app.config.LocalSettingsDir))
	}
	if h.GetBackendsHealth != nil {
		app.mux.HandleFunc("GET /api/backends", h.GetBackendsHealth)
	}
}

// registerDaemonRoutes registers daemon supervisor and config endpoints.
func (app *Server) registerDaemonRoutes(h *handlermux.Handlers) {
	if h.DaemonSupervisor != nil {
		app.mux.HandleFunc("GET /api/daemon/supervisor", h.DaemonSupervisor)
	}
	if h.DaemonConfig != nil {
		app.mux.HandleFunc("GET /api/daemon/config", h.DaemonConfig)
	}
}

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
	workspaceMW := app.workspaceMiddleware()
	app.mux.HandleFunc("GET /api/workspaces/active", handlermux.HandleActiveWorkspace(app.workspaceSvc))
	app.mux.HandleFunc("GET /api/workspaces", handlermux.HandleListWorkspaces(app.workspaceSvc))
	app.mux.Handle("GET /api/workspaces/{ws}", workspaceMW(handlermux.HandleGetWorkspace(app.workspaceSvc)))
	app.mux.HandleFunc("POST /api/workspaces", handlermux.HandleWorkspaceCreate(app.workspaceSvc))
	app.mux.HandleFunc("GET /api/workspaces/jobs/{id}", handlermux.HandleGetWorkspaceJob(app.workspaceSvc))
	app.mux.HandleFunc("PUT /api/workspaces/order", handlermux.HandleWorkspaceReorder(app.workspaceSvc))
	app.mux.Handle("DELETE /api/workspaces/{ws}", workspaceMW(handlermux.HandleWorkspaceDelete(app.workspaceSvc)))
	// PATCH handlers are registered on the outer mux (not the nested wsMux)
	// because Go 1.22+ http.ServeMux has a bug where r.Body.Read() hangs for
	// PATCH requests routed through a nested mux via wildcard subtree pattern.
	app.mux.Handle("PATCH /api/workspaces/{ws}/name", workspaceMW(handlermux.HandleWorkspaceRename(app.workspaceSvc)))
	app.mux.Handle("GET /api/workspaces/{ws}/config/backend", workspaceMW(handlermux.HandleWorkspaceBackendGet(app.workspaceSvc)))
	app.mux.Handle("PATCH /api/workspaces/{ws}/config/backend", workspaceMW(handlermux.HandleWorkspaceBackendPatch(app.workspaceSvc)))
	if statusHandler := app.config.MonitorHandlers.Status; statusHandler != nil {
		app.mux.Handle("GET /api/workspaces/{ws}/monitor/status", workspaceMW(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			q := r.URL.Query()
			if q.Get("workspace") == "" {
				q.Set("workspace", middleware.WorkspaceFromContext(r.Context()))
			}
			r2 := r.Clone(r.Context())
			u := *r.URL
			u.RawQuery = q.Encode()
			r2.URL = &u
			statusHandler(w, r2)
		})))
	}

	wsMux := http.NewServeMux()
	for _, mod := range app.wsModules {
		mod.Register(wsMux)
	}
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
	app.mux.Handle("/api/workspaces/{ws}/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// /readyz is a runtime readiness probe consumed by ensure-runtime.
		// It still goes through global auth when auth is enabled, but bypasses
		// workspace middleware so the handler can return readiness diagnostics
		// for unregistered or starting workspaces. Exact-path match (not
		// HasSuffix) so future nested routes ending in `/readyz` do not
		// silently skip workspace middleware.
		if r.Method == http.MethodGet && r.URL.Path == "/api/workspaces/"+r.PathValue("ws")+"/readyz" {
			wsHandler.ServeHTTP(w, r)
			return
		}
		workspaceMW(wsHandler).ServeHTTP(w, r)
	}))
}

func (app *Server) workspaceMiddleware() middleware.Middleware {
	if app.wsResolveFn != nil {
		return middleware.WorkspaceResolved(app.wsResolveFn)
	}
	return middleware.Workspace(func(id string) bool {
		return app.wsExistsFn != nil && app.wsExistsFn(id)
	})
}

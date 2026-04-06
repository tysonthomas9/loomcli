// Package webui provides the web UI server for loomcli, embedding the React frontend and serving API endpoints.
package webui

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"golang.org/x/time/rate"

	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/webui/coordinator"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
	"github.com/tysonthomas9/loomcli/internal/webui/editor"
	"github.com/tysonthomas9/loomcli/internal/webui/fleet"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/misc"
	"github.com/tysonthomas9/loomcli/internal/webui/issuetabs"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
	"github.com/tysonthomas9/loomcli/internal/webui/sessionhistory"
	"github.com/tysonthomas9/loomcli/internal/webui/subscription"
	"github.com/tysonthomas9/loomcli/internal/webui/tabmeta"
	"github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

// Server holds all initialized server dependencies as struct fields.
type Server struct {
	config ServerConfig

	// Network
	listener   net.Listener
	actualPort int
	corsConfig middleware.CORSConfig

	// HTTP routing
	mux       *http.ServeMux
	wsModules []Module // workspace-scoped route modules (registered on wsMux)

	// Connection pools
	pool      daemon.Pool // may be nil if daemon unavailable at startup
	multiPool *daemon.MultiPool

	// Service layer
	issueSvc     service.IssueService
	agentSvc     service.AgentService
	workspaceSvc service.WorkspaceService
	termSvc      service.TerminalService // nil if termMgr is nil
	diffSvc      service.DiffService     // nil if ops.GitOps is nil
	fileSvc      service.FileService     // nil if ops.FileOps is nil
	sessSvc      service.SessionService  // always constructed (stores may be nil internally)

	// Real-time
	hub               *realtime.Hub
	multiSub          *subscription.MultiWorkspaceSubscriber
	getMutationsSince func(wsID string, since int64) []rpc.MutationEvent

	// Workspace lifecycle
	registry           *coordinator.WorkspaceRegistry
	initialWorkspaceID string

	// Terminal
	termMgr  *terminal.TerminalManager // nil if tmux unavailable
	termAuth *realtime.TerminalAuth    // nil if termMgr is nil

	// SSE token exchange (external auth mode only)
	sseTokens *realtime.TokenStore // nil if ExtAuthURL is empty

	// Fleet
	fleetRegistry *fleet.StoreRegistry  // nil if Redis unconfigured
	tokenCfg      *fleet.TokenConfig    // nil if fleetRegistry is nil
	claimMetrics  *fleet.ClaimMetrics   // nil if fleetRegistry is nil
	fleetRegCfg   *fleet.RegisterConfig // nil if no fleet API key

	// Redis-backed stores
	tabMetaStore        *tabmeta.Store        // nil if Redis unconfigured
	issueTabStore       *issuetabs.Store      // nil if Redis unconfigured
	sessionHistoryStore *sessionhistory.Store // nil if Redis unconfigured

	// External auth
	extAuthMiddleware middleware.Middleware // nil = open mode
	jwksCleanup       func()                // nil if no JWKS cache

	// Wrapped workspace lifecycle functions
	wrappedCreateFn service.WorkspaceCreateFn
	wrappedDeleteFn func(string) error

	// Async workspace creation jobs
	jobStore *WorkspaceJobStore

	// Workspace existence checker
	wsExistsFn func(string) bool

	// Notify token for session change endpoint auth
	notifyToken     string
	notifyTokenFile string

	// Rate limiters (created in buildHandlers, stopped in Close)
	clientErrLimiter *misc.ClientErrorLimiter
	cspLimiter       *misc.CSPReportLimiter
	authCfgLimiter   *misc.AuthConfigLimiter

	// Shared infrastructure
	editorCache *misc.EditorCache
	frontendH   http.Handler // embedded FS handler or dev-mode handler

	// Pre-built top-level handlers
	healthHandler              http.HandlerFunc
	apiHealthHandler           http.HandlerFunc
	clientErrorsHandler        http.HandlerFunc
	cspReportHandler           http.HandlerFunc
	authConfigHandler          http.HandlerFunc
	statsHandler               http.HandlerFunc
	metricsHandler             http.HandlerFunc
	daemonStatusHandler        http.HandlerFunc
	getBackendConfigHandler    http.HandlerFunc
	patchBackendConfigHandler  http.HandlerFunc
	getBackendsHealthHandler   http.HandlerFunc // nil if ops.BackendOps is nil
	listEditorsHandler         http.HandlerFunc
	openEditorHandler          http.HandlerFunc
	notifySessionChangeHandler http.HandlerFunc // nil if hub is nil

	// Daemon supervisor/config handlers (nil when callback not provided)
	daemonSupervisorHandler http.HandlerFunc
	daemonConfigHandler     http.HandlerFunc
}

// buildHandlers constructs all top-level HTTP handlers from the current dependency
// fields. Called by NewServer after all dependencies are initialized. Tests that
// bypass NewServer must assign app.mux before calling registerRoutes, then call
// this method explicitly.
func (app *Server) buildHandlers() {
	// Rate limiters
	app.clientErrLimiter = misc.NewClientErrorLimiter(rate.Limit(10.0/60.0), 10, 5*time.Minute, 10*time.Minute)
	app.cspLimiter = misc.NewCSPReportLimiter(rate.Limit(1.0), 20, 5*time.Minute, 10*time.Minute)
	app.authCfgLimiter = misc.NewAuthConfigLimiter(rate.Limit(5), 10, 5*time.Minute, 10*time.Minute)

	// Editor infrastructure
	app.editorCache = misc.NewDefaultEditorCache()

	// Handlers
	app.healthHandler = misc.HandleHealth(app.pool)
	app.apiHealthHandler = misc.HandleAPIHealth(app.pool)
	app.clientErrorsHandler = misc.HandleClientErrors(app.clientErrLimiter)
	app.cspReportHandler = misc.HandleCSPReport(app.cspLimiter)
	app.authConfigHandler = misc.HandleAuthConfig(app.config.ExtAuthURL, app.authCfgLimiter)
	app.statsHandler = misc.HandleStats(app.pool)

	var getFleetTimeouts func() int64
	if app.fleetRegistry != nil {
		getFleetTimeouts = app.fleetRegistry.GetTotalTimeoutCount
	}
	app.metricsHandler = misc.HandleMetrics(app.hub, getFleetTimeouts, app.claimMetrics)

	app.daemonStatusHandler = misc.HandleDaemonStatus(app.pool)
	app.getBackendConfigHandler = misc.HandleGetBackendConfig(app.pool)
	app.patchBackendConfigHandler = misc.HandlePatchBackendConfig(app.pool)

	if app.config.BackendOps != nil {
		app.getBackendsHealthHandler = misc.HandleGetBackendsHealth(app.config.BackendOps)
	}

	app.listEditorsHandler = misc.HandleListEditors(app.editorCache)
	app.openEditorHandler = misc.HandleOpenEditor(app.editorCache, editor.LaunchEditor)

	if app.hub != nil {
		app.notifySessionChangeHandler = misc.HandleNotifySessionChange(app.hub, app.notifyToken)
	}

	if app.config.DaemonSupervisorFn != nil {
		app.daemonSupervisorHandler = handleDaemonSupervisor(app.config.DaemonSupervisorFn)
	}
	if app.config.DaemonConfigFn != nil {
		app.daemonConfigHandler = handleDaemonConfig(app.config.DaemonConfigFn)
	}

	if app.config.DevMode {
		app.frontendH = devFrontendHandler(app.config.DevFrontendDir)
	} else {
		app.frontendH = frontendHandler()
	}
}

// StartServer starts the web UI server with the given configuration.
// It blocks until the context is canceled, then performs graceful shutdown.
// Returns the actual port used (which may differ from config.Port if it was in use).
func StartServer(ctx context.Context, config ServerConfig) error {
	app, err := NewServer(ctx, config)
	if err != nil {
		return err
	}
	defer app.Close()
	return app.run(ctx)
}

// Close releases deferred resources that outlive the HTTP server lifetime.
// Called from StartServer via defer after run() returns.
func (app *Server) Close() {
	// Stop rate limiter background goroutines.
	if app.clientErrLimiter != nil {
		app.clientErrLimiter.Stop()
	}
	if app.cspLimiter != nil {
		app.cspLimiter.Stop()
	}
	if app.authCfgLimiter != nil {
		app.authCfgLimiter.Stop()
	}

	if app.notifyTokenFile != "" {
		_ = os.Remove(app.notifyTokenFile)
	}
	if app.jobStore != nil {
		app.jobStore.Stop()
	}
	if app.jwksCleanup != nil {
		app.jwksCleanup()
	}
	if app.sessionHistoryStore != nil {
		_ = app.sessionHistoryStore.Close()
	}
	if app.issueTabStore != nil {
		_ = app.issueTabStore.Close()
	}
	if app.tabMetaStore != nil {
		_ = app.tabMetaStore.Close()
	}
	if app.fleetRegCfg != nil && app.fleetRegCfg.RateLimiter != nil {
		_ = app.fleetRegCfg.RateLimiter.Close()
	}
	if app.fleetRegistry != nil {
		_ = app.fleetRegistry.Close()
	}
}

// run starts the HTTP server and blocks until the context is canceled or the
// server encounters a fatal error. It performs graceful shutdown and stops
// components in reverse-initialization order.
func (app *Server) run(ctx context.Context) error { //nolint:funlen // server lifecycle method
	// Middleware chain: recover -> log -> ratelimit -> security -> auth -> CORS -> mux
	authMW := app.extAuthMiddleware
	if authMW == nil {
		authMW = func(next http.Handler) http.Handler { return next }
	}
	rl, rateLimitMW := middleware.RateLimit(middleware.DefaultRateLimitConfig())
	chain := middleware.Chain(
		middleware.Recover(app.config.Logger),
		middleware.RequestLog(app.config.Logger),
		rateLimitMW,
		middleware.SecurityHeaders(middleware.SecurityConfig{
			HSTSEnabled:   app.config.HSTSEnabled,
			ExtAuthOrigin: middleware.ExtractOrigin(app.config.ExtAuthURL),
		}),
		authMW,
		middleware.CORS(app.corsConfig),
	)
	handler := h2c.NewHandler(chain(app.mux), &http2.Server{})

	// Shutdown context: when canceled, in-flight handlers abort quickly.
	shutdownCtx, shutdownCancel := context.WithCancel(context.Background())
	defer shutdownCancel()

	server := &http.Server{
		Addr:              net.JoinHostPort(app.config.BindAddress, strconv.Itoa(app.actualPort)),
		Handler:           handler,
		ReadTimeout:       15 * time.Second,
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		BaseContext: func(_ net.Listener) context.Context {
			return shutdownCtx
		},
	}

	// Start server in a goroutine using the pre-acquired listener
	serverErr := make(chan error, 1)
	go func() {
		logger.Info("server listening", "address", app.config.BindAddress, "port", app.actualPort)
		if err := server.Serve(app.listener); err != nil && err != http.ErrServerClosed {
			serverErr <- fmt.Errorf("server error: %w", err)
		}
		close(serverErr)
	}()

	// Wait for context cancellation or server error
	select {
	case <-ctx.Done():
	case err := <-serverErr:
		if err != nil {
			return err
		}
	}

	logger.Info("shutting down server")

	// Cancel server-wide context so in-flight handlers abort quickly.
	shutdownCancel()

	// Drain in-flight requests (most abort quickly due to canceled context).
	drainCtx, drainCancel := context.WithTimeout(context.Background(), app.config.ShutdownTimeout)
	defer drainCancel()

	if err := server.Shutdown(drainCtx); err != nil {
		return fmt.Errorf("server forced to shutdown: %w", err)
	}
	logger.Info("server stopped")

	// Stop components in reverse-initialization order.

	// Stop rate limiter cleanup goroutine
	rl.Stop()

	// Stop terminal auth cleanup goroutine
	if app.termAuth != nil {
		app.termAuth.Stop()
	}

	// Stop SSE token store cleanup goroutine
	if app.sseTokens != nil {
		app.sseTokens.Stop()
	}

	// Stop terminal manager (kill tmux sessions and close PTYs)
	if app.termMgr != nil {
		if err := app.termMgr.Shutdown(); err != nil {
			logger.Warn("error shutting down terminal manager", "component", "terminal", "err", err)
		} else {
			logger.Info("terminal manager stopped", "component", "terminal")
		}
	}

	_ = app.registry.Close()

	// Stop multi-workspace subscriber (no more handlers need it)
	if app.multiSub != nil {
		app.multiSub.Stop()
		logger.Info("multi-workspace subscriber stopped")
	}

	// Stop SSE hub (all SSE handlers have exited)
	if app.hub != nil {
		app.hub.Stop()
		logger.Info("SSE hub stopped")
	}

	// Close MultiPool (closes all per-workspace pools including the initial one)
	if app.multiPool != nil {
		if err := app.multiPool.Close(); err != nil {
			logger.Warn("error closing multi-pool", "err", err)
		} else {
			logger.Info("multi-pool closed")
		}
	}

	return nil
}

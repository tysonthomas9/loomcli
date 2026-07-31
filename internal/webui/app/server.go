// Package app provides the web UI server assembly for loomcli.
// It wires together all subpackages and serves the API + embedded React frontend.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"github.com/tysonthomas9/loomcli/internal/connector"
	"github.com/tysonthomas9/loomcli/internal/webui"
	"github.com/tysonthomas9/loomcli/internal/webui/appinfra"
	"github.com/tysonthomas9/loomcli/internal/webui/appstores"

	"github.com/tysonthomas9/loomcli/internal/webui/handlermux"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
	"github.com/tysonthomas9/loomcli/internal/webui/svcimpl"
	"github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

// logger is the package-level structured logger.
var logger = slog.Default()

// initLogger sets the package-level logger. If l is nil, slog.Default() is kept.
func initLogger(l *slog.Logger) {
	if l != nil {
		logger = l
	}
}

// Server holds all initialized server dependencies as struct fields.
type Server struct {
	config webui.ServerConfig

	// Network
	listener   net.Listener
	actualPort int
	corsConfig middleware.CORSConfig

	// HTTP routing
	mux                 *http.ServeMux
	wsModules           []wsModule // workspace-scoped route modules (registered on wsMux)
	connectorDispatcher *connector.Dispatcher

	// Connection pools
	pool      appinfra.Pool // may be nil if daemon unavailable at startup
	multiPool *appinfra.MultiPool

	// Service layer
	issueSvc     service.IssueService
	agentSvc     service.AgentService
	workspaceSvc service.WorkspaceService
	termSvc      service.TerminalService // nil if termMgr is nil
	diffSvc      service.DiffService     // nil if ops.GitOps is nil
	fileSvc      service.FileService     // nil if ops.FileOps is nil
	sessSvc      service.SessionService  // always constructed (stores may be nil internally)

	// Real-time
	hub               *appstores.Hub
	multiSub          *appstores.MultiWorkspaceSubscriber
	getMutationsSince appstores.MutationsSinceFn

	// Workspace lifecycle
	registry           *appinfra.WorkspaceRegistry
	initialWorkspaceID string

	// Terminal
	ptyMgr       *terminal.MultiPTYManager  // main web terminal (per-workspace dispatch)
	agentTmuxMgr *terminal.AgentTmuxManager // agent-view only; nil if tmux unavailable
	termAuth     *appstores.TerminalAuth    // one-time token issuer (nil disables auth)

	// SSE token exchange (external auth mode only)
	sseTokens *appstores.TokenStore // nil if ExtAuthURL is empty

	// Fleet
	fleetRegistry *appinfra.FleetStoreRegistry  // nil if Redis unconfigured
	tokenCfg      *appinfra.FleetTokenConfig    // nil if fleetRegistry is nil
	claimMetrics  *appinfra.FleetClaimMetrics   // nil if fleetRegistry is nil
	fleetRegCfg   *appinfra.FleetRegisterConfig // nil if no fleet API key

	// Redis-backed stores
	tabMetaStore        *appstores.TabMetaStore        // nil if Redis unconfigured
	issueTabStore       *appstores.IssueTabStore       // nil if Redis unconfigured
	sessionHistoryStore *appstores.SessionHistoryStore // nil if Redis unconfigured

	// External auth
	extAuthMiddleware middleware.Middleware // nil = open mode
	jwksCleanup       func()                // nil if no JWKS cache

	// Wrapped workspace lifecycle functions
	wrappedCreateFn service.WorkspaceCreateFn
	wrappedDeleteFn func(string) error

	// Async workspace creation jobs
	jobStore *svcimpl.WorkspaceJobStore

	// Workspace resolver
	wsExistsFn  func(string) bool // legacy identity resolver used by tests
	wsResolveFn middleware.WorkspaceResolveFn

	// Notify token for session change endpoint auth
	notifyToken     string
	notifyTokenFile string

	// Pre-built top-level handlers (built by handlermux.BuildHandlers)
	handlers *handlermux.Handlers

	prReviewCredentialSeeds credentialSeedInvalidator

	// startedAt captures server-process boot time. Used to distinguish
	// "tab metadata from a prior server process" (PTY is gone) from
	// "tab metadata just created in this process" (PTY about to spawn).
	startedAt time.Time
}

type credentialSeedInvalidator interface {
	InvalidateCredentialSeeds()
}

// buildHandlers constructs all top-level HTTP handlers from the current dependency
// fields. Called by NewServer after all dependencies are initialized.
func (app *Server) buildHandlers() {
	var getFleetTimeouts func() int64
	if app.fleetRegistry != nil {
		getFleetTimeouts = app.fleetRegistry.GetTotalTimeoutCount
	}

	var daemonSupervisorH, daemonConfigH http.HandlerFunc
	if app.config.DaemonSupervisorFn != nil {
		daemonSupervisorH = webui.HandleDaemonSupervisor(app.config.DaemonSupervisorFn)
	}
	if app.config.DaemonConfigFn != nil {
		daemonConfigH = webui.HandleDaemonConfig(app.config.DaemonConfigFn)
	}

	var backendsHealthH http.HandlerFunc
	if app.config.BackendOps != nil {
		backendsHealthH = webui.HandleBackendsHealth(app.config.BackendOps)
	}

	var graceMS, idleMS int64
	var maxSess int
	if app.ptyMgr != nil {
		graceMS = app.ptyMgr.GracePeriod().Milliseconds()
		idleMS = app.ptyMgr.IdleTimeout().Milliseconds()
		maxSess = app.ptyMgr.MaxSessions()
	}
	app.handlers = handlermux.BuildHandlers(handlermux.HandlerDeps{
		Pool:               app.pool,
		Hub:                app.hub,
		ExtAuthURL:         app.config.ExtAuthURL,
		BackendsHealthH:    backendsHealthH,
		NotifyToken:        app.notifyToken,
		DaemonSupervisor:   daemonSupervisorH,
		DaemonConfig:       daemonConfigH,
		FleetTimeoutsFn:    getFleetTimeouts,
		ClaimMetrics:       app.claimMetrics,
		TerminalGraceMS:    graceMS,
		TerminalIdleMS:     idleMS,
		TerminalMaxSession: maxSess,
		IssueBackendFn:     app.config.IssueBackendFn,
		// Fleet client mode is the only deployment that runs without a local
		// issue daemon. FleetEnabled (--fleet-mode) is a separate signal: it
		// controls whether this server registers fleet API routes for other
		// clients to consume.
		DaemonExpected: !app.config.FleetClient,
	})
}

// StartServer starts the web UI server with the given configuration.
// It blocks until the context is canceled, then performs graceful shutdown.
// Returns the actual port used (which may differ from config.Port if it was in use).
func StartServer(ctx context.Context, config webui.ServerConfig) error {
	if config.SentryDSN != "" {
		base := config.Logger
		if base == nil {
			base = slog.Default()
		}
		config.Logger = webui.InitSentry(base, config.SentryDSN, "")
		slog.SetDefault(config.Logger)
		defer webui.FlushSentry(2 * time.Second)
	}
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
	if app.handlers != nil {
		if app.handlers.ClientErrLimiter != nil {
			app.handlers.ClientErrLimiter.Stop()
		}
		if app.handlers.AuthCfgLimiter != nil {
			app.handlers.AuthCfgLimiter.Stop()
		}
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
	// Prometheus HTTP metrics: outer wraps the chain to record duration+status;
	// routeCapture must run innermost (after mux routes) to read r.Pattern.
	metricsOuter, routeCapture := webui.PromMetricsMiddleware()
	// Tracing pair mirrors the same outer/inner pattern. The outer (otelhttp)
	// extracts traceparent and starts a span before any other middleware
	// runs; the inner promotes the captured route template onto the span
	// name + http.route attribute.
	tracingOuter, tracingInner := webui.TracingWithRouteName()
	chain := middleware.Chain(
		middleware.Middleware(tracingOuter),
		middleware.Recover(app.config.Logger),
		middleware.RequestLog(app.config.Logger),
		middleware.Middleware(metricsOuter),
		rateLimitMW,
		middleware.SecurityHeaders(middleware.SecurityConfig{
			HSTSEnabled:   app.config.HSTSEnabled,
			ExtAuthOrigin: middleware.ExtractOrigin(app.config.ExtAuthURL),
		}),
		authMW,
		middleware.CORS(app.corsConfig),
		middleware.Middleware(routeCapture),
		middleware.Middleware(tracingInner),
	)
	var handler http.Handler
	if os.Getenv("LOOM_DISABLE_H2C") == "1" {
		handler = chain(app.mux)
	} else {
		handler = h2c.NewHandler(chain(app.mux), &http2.Server{})
	}

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
	//
	// Workspace jobs intentionally outlive the HTTP request that accepted them.
	// Drain them before closing the registry, workspace pools, or any other
	// dependency captured by their callbacks. Close calls Stop again, so this
	// remains safe on both the graceful and construction-cleanup paths.
	if app.jobStore != nil {
		app.jobStore.Stop()
	}

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

	// Stop terminal managers (close PTYs; detach agent-view tmux attaches)
	if app.ptyMgr != nil {
		if err := app.ptyMgr.Close(); err != nil {
			logger.Warn("error closing pty manager", "component", "terminal", "err", err)
		} else {
			logger.Info("pty manager stopped", "component", "terminal")
		}
	}
	if app.agentTmuxMgr != nil {
		if err := app.agentTmuxMgr.Shutdown(); err != nil {
			logger.Warn("error shutting down agent tmux manager", "component", "terminal", "err", err)
		} else {
			logger.Info("agent tmux manager stopped", "component", "terminal")
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

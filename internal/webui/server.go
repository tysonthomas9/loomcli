// Package webui provides the web UI server for loomcli, embedding the React frontend and serving API endpoints.
package webui

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
	"golang.org/x/time/rate"

	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
	"github.com/tysonthomas9/loomcli/internal/webui/editor"
	"github.com/tysonthomas9/loomcli/internal/webui/fleet"
	"github.com/tysonthomas9/loomcli/internal/webui/issuetabs"
	"github.com/tysonthomas9/loomcli/internal/webui/sessionhistory"
	"github.com/tysonthomas9/loomcli/internal/webui/tabmeta"
)

// Server holds all initialized server dependencies as struct fields.
type Server struct {
	config ServerConfig

	// Network
	listener   net.Listener
	actualPort int
	corsConfig CORSConfig

	// HTTP routing
	mux *http.ServeMux

	// Connection pools
	pool      daemon.Pool // may be nil if daemon unavailable at startup
	multiPool *daemon.MultiPool

	// Real-time
	hub               *SSEHub
	multiSub          *MultiWorkspaceSubscriber
	getMutationsSince func(wsID string, since int64) []rpc.MutationEvent

	// Workspace lifecycle
	registry           *WorkspaceRegistry
	initialWorkspaceID string

	// Terminal
	termMgr  *TerminalManager // nil if tmux unavailable
	termAuth *terminalAuth    // nil if termMgr is nil

	// SSE token exchange (external auth mode only)
	sseTokens *sseTokenStore // nil if ExtAuthURL is empty

	// Fleet
	fleetRegistry *fleet.StoreRegistry // nil if Redis unconfigured
	tokenCfg      *TokenConfig         // nil if fleetRegistry is nil
	claimMetrics  *fleet.ClaimMetrics  // nil if fleetRegistry is nil
	fleetRegCfg   *FleetRegisterConfig // nil if no fleet API key

	// Redis-backed stores
	tabMetaStore        *tabmeta.Store        // nil if Redis unconfigured
	issueTabStore       *issuetabs.Store      // nil if Redis unconfigured
	sessionHistoryStore *sessionhistory.Store // nil if Redis unconfigured

	// External auth
	extAuthMiddleware func(http.Handler) http.Handler // nil = open mode
	jwksCleanup       func()                          // nil if no JWKS cache

	// Wrapped workspace lifecycle functions
	wrappedCreateFn WorkspaceCreateFn
	wrappedDeleteFn func(string) error

	// Async workspace creation jobs
	jobStore *WorkspaceJobStore

	// Workspace existence checker
	wsExistsFn func(string) bool

	// Notify token for session change endpoint auth
	notifyToken     string
	notifyTokenFile string

	// Rate limiters (created in buildHandlers, stopped in Close)
	clientErrLimiter *clientErrorLimiter
	cspLimiter       *cspReportLimiter
	authCfgLimiter   *authConfigLimiter

	// Shared infrastructure
	editorCache *editorCache
	loomProxy   http.Handler // nil if LoomServerURL is empty
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
	getBackendsHealthHandler   http.HandlerFunc // nil if BackendOps is nil
	listEditorsHandler         http.HandlerFunc
	openEditorHandler          http.HandlerFunc
	notifySessionChangeHandler http.HandlerFunc // nil if hub is nil
}

// buildHandlers constructs all top-level HTTP handlers from the current dependency
// fields. Called by NewServer after all dependencies are initialized. Tests that
// bypass NewServer must assign app.mux before calling registerRoutes, then call
// this method explicitly.
func (app *Server) buildHandlers() {
	// Rate limiters
	app.clientErrLimiter = newClientErrorLimiter(rate.Limit(10.0/60.0), 10, 5*time.Minute, 10*time.Minute)
	app.cspLimiter = newCSPReportLimiter(rate.Limit(1.0), 20, 5*time.Minute, 10*time.Minute)
	app.authCfgLimiter = newAuthConfigLimiter(rate.Limit(5), 10, 5*time.Minute, 10*time.Minute)

	// Editor infrastructure
	app.editorCache = newDefaultEditorCache()

	// Handlers
	app.healthHandler = handleHealth(app.pool)
	app.apiHealthHandler = handleAPIHealth(app.pool)
	app.clientErrorsHandler = handleClientErrors(app.clientErrLimiter)
	app.cspReportHandler = handleCSPReport(app.cspLimiter)
	app.authConfigHandler = handleAuthConfig(app.config.ExtAuthURL, app.authCfgLimiter)
	app.statsHandler = handleStats(app.pool)

	var getFleetTimeouts func() int64
	if app.fleetRegistry != nil {
		getFleetTimeouts = app.registry.FleetTimeoutCount
	}
	app.metricsHandler = handleMetrics(app.hub, getFleetTimeouts, app.claimMetrics)

	app.daemonStatusHandler = handleDaemonStatus(app.pool)
	app.getBackendConfigHandler = handleGetBackendConfig(app.pool)
	app.patchBackendConfigHandler = handlePatchBackendConfig(app.pool)

	if app.config.BackendOps != nil {
		app.getBackendsHealthHandler = handleGetBackendsHealth(app.config.BackendOps)
	}

	app.loomProxy = newLoomProxy(app.config.LoomServerURL)

	app.listEditorsHandler = handleListEditors(app.editorCache)
	app.openEditorHandler = handleOpenEditor(app.editorCache, editor.LaunchEditor)

	if app.hub != nil {
		app.notifySessionChangeHandler = handleNotifySessionChange(app.hub, app.notifyToken)
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
		app.clientErrLimiter.stop()
	}
	if app.cspLimiter != nil {
		app.cspLimiter.stop()
	}
	if app.authCfgLimiter != nil {
		app.authCfgLimiter.stop()
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
	// fleetRegistry is closed by registry.Close() — no separate close needed.
}

// run starts the HTTP server and blocks until the context is canceled or the
// server encounters a fatal error. It performs graceful shutdown and stops
// components in reverse-initialization order.
func (app *Server) run(ctx context.Context) error { //nolint:funlen // server lifecycle method
	// Middleware chain: rate-limit -> security -> auth -> CORS -> mux
	corsMiddleware := NewCORSMiddleware(app.corsConfig)
	authMW := app.extAuthMiddleware
	if authMW == nil {
		authMW = func(next http.Handler) http.Handler { return next }
	}
	securityMiddleware := NewSecurityHeadersMiddleware(SecurityConfig{
		HSTSEnabled:   app.config.HSTSEnabled,
		ExtAuthOrigin: extractOrigin(app.config.ExtAuthURL),
	})
	rl, rateLimitMiddleware := NewRateLimitMiddleware(DefaultRateLimitConfig())
	handler := h2c.NewHandler(NewRequestLogMiddleware(app.config.Logger)(rateLimitMiddleware(securityMiddleware(authMW(corsMiddleware(app.mux))))), &http2.Server{})

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
		slog.Info("server listening", "address", app.config.BindAddress, "port", app.actualPort)
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

	slog.Info("shutting down server")

	// Cancel server-wide context so in-flight handlers abort quickly.
	shutdownCancel()

	// Drain in-flight requests (most abort quickly due to canceled context).
	drainCtx, drainCancel := context.WithTimeout(context.Background(), app.config.ShutdownTimeout)
	defer drainCancel()

	if err := server.Shutdown(drainCtx); err != nil {
		return fmt.Errorf("server forced to shutdown: %w", err)
	}
	slog.Info("server stopped")

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
			slog.Warn("error shutting down terminal manager", "component", "terminal", "err", err)
		} else {
			slog.Info("terminal manager stopped", "component", "terminal")
		}
	}

	_ = app.registry.Close()

	// Stop multi-workspace subscriber (no more handlers need it)
	if app.multiSub != nil {
		app.multiSub.Stop()
		slog.Info("multi-workspace subscriber stopped")
	}

	// Stop SSE hub (all SSE handlers have exited)
	if app.hub != nil {
		app.hub.Stop()
		slog.Info("SSE hub stopped")
	}

	// Close MultiPool (closes all per-workspace pools including the initial one)
	if app.multiPool != nil {
		if err := app.multiPool.Close(); err != nil {
			slog.Warn("error closing multi-pool", "err", err)
		} else {
			slog.Info("multi-pool closed")
		}
	}

	return nil
}

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
)

// StartServer starts the web UI server with the given configuration.
// It blocks until the context is canceled, then performs graceful shutdown.
// Returns the actual port used (which may differ from config.Port if it was in use).
func StartServer(ctx context.Context, config ServerConfig) error {
	app, err := newServerApp(ctx, config)
	if err != nil {
		return err
	}
	defer app.Close()
	return app.run(ctx)
}

// Close releases deferred resources that outlive the HTTP server lifetime.
// Called from StartServer via defer after run() returns.
func (app *serverApp) Close() {
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
func (app *serverApp) run(ctx context.Context) error { //nolint:funlen // server lifecycle method
	// Create HTTP server and register routes
	mux := http.NewServeMux()
	clientErrLimiter, cspLimiter, authCfgLimiter := app.setupRoutes(mux)
	defer clientErrLimiter.stop()
	defer cspLimiter.stop()
	defer authCfgLimiter.stop()

	app.registerWorkerAPIRoutes(mux)

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
	handler := h2c.NewHandler(NewRequestLogMiddleware(app.config.Logger)(rateLimitMiddleware(securityMiddleware(authMW(corsMiddleware(mux))))), &http2.Server{})

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

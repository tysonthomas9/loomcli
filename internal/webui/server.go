// Package webui provides the web UI server for loomcli, embedding the React frontend and serving API endpoints.
package webui

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"github.com/tysonthomas9/loomcli/internal/circuitbreaker"
	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
	"github.com/tysonthomas9/loomcli/internal/webui/fleet"
	"github.com/tysonthomas9/loomcli/internal/webui/issuetabs"
	"github.com/tysonthomas9/loomcli/internal/webui/sessionhistory"
	"github.com/tysonthomas9/loomcli/internal/webui/tabmeta"
	"github.com/tysonthomas9/loomcli/internal/workspace"
)

// StartServer starts the web UI server with the given configuration.
// It blocks until the context is canceled, then performs graceful shutdown.
// Returns the actual port used (which may differ from config.Port if it was in use).
func StartServer(ctx context.Context, config ServerConfig) error {
	// Apply defaults for zero values
	if config.Port == 0 {
		config.Port = defaultPort
	}
	if config.PoolSize == 0 {
		config.PoolSize = defaultPoolSize
	}
	if config.ShutdownTimeout == 0 {
		config.ShutdownTimeout = defaultShutdownTimeout
	}
	if config.MaxPortAttempts == 0 {
		config.MaxPortAttempts = defaultMaxPortAttempts
	}
	if config.BindAddress == "" {
		config.BindAddress = "127.0.0.1"
	}

	// Build CORS configuration
	corsConfig := CORSConfig{
		Enabled: config.CORSEnabled,
	}
	if config.CORSEnabled {
		if len(config.CORSOrigins) > 0 {
			corsConfig.AllowedOrigins = config.CORSOrigins
		} else {
			// Default to Vite dev server
			corsConfig.AllowedOrigins = []string{"http://localhost:3000"}
		}
	}

	// Auto-add auth service origin to CORS when external auth is configured
	if authOrigin := extractOrigin(config.ExtAuthURL); authOrigin != "" {
		corsConfig.Enabled = true
		corsConfig.AllowedOrigins = append(corsConfig.AllowedOrigins, authOrigin)
	}

	// Log configuration
	slog.Info("starting web UI server", "port", config.Port, "pool_size", config.PoolSize, "bind_address", config.BindAddress)
	if config.SocketPath != "" {
		slog.Info("daemon socket configured", "socket", config.SocketPath)
	} else {
		slog.Info("daemon socket: auto-detect")
	}
	if corsConfig.Enabled {
		slog.Info("CORS enabled", "origins", corsConfig.AllowedOrigins)
	}
	if config.HSTSEnabled {
		slog.Info("HSTS enabled: ensure this server is behind a TLS-terminating proxy")
	}
	if config.ExtAuthURL == "" && config.BindAddress != "127.0.0.1" && config.BindAddress != "::1" {
		slog.Warn("no authentication configured and server is exposed to network", "bind_address", config.BindAddress)
	}
	if config.DevMode {
		dir := config.DevFrontendDir
		if dir == "" {
			dir = "internal/webui/frontend/dist"
		}
		slog.Info("dev mode enabled", "frontend_dir", dir)
	} else {
		logFrontendBuildMeta()
	}
	if config.FleetEnabled {
		slog.Info("fleet routes enabled")
	}

	// Find an available port (auto-fallback if requested port is in use)
	listener, actualPort, err := findAvailablePort(config.BindAddress, config.Port, config.MaxPortAttempts)
	if err != nil {
		return fmt.Errorf("could not find available port: %w", err)
	}
	if actualPort != config.Port {
		slog.Info("configured port in use, using fallback", "requested_port", config.Port, "actual_port", actualPort)
	}

	// MultiPool for workspace-aware connection routing.
	multiPool := daemon.NewMultiPool(WorkspaceFromContext, config.PoolSize)

	// Initialize the initial workspace connection pool (stable UUID or CWD basename).
	initialWorkspaceID := config.InitialWorkspaceID
	if initialWorkspaceID == "" {
		initialWorkspaceID = "default"
		if cwd, err := os.Getwd(); err == nil {
			initialWorkspaceID = filepath.Base(cwd)
		}
	}
	var rawPool *daemon.ConnectionPool
	var pool daemon.Pool // may be ProtectedPool or raw ConnectionPool
	var poolErr error

	if config.SocketPath != "" {
		// Use explicit socket path
		rawPool, poolErr = daemon.NewConnectionPool(config.SocketPath, config.PoolSize)
	} else {
		// Auto-discover daemon from current directory
		cwd, err := getCwd()
		if err != nil {
			slog.Warn("failed to get current directory", "err", err)
		} else {
			rawPool, poolErr = daemon.NewConnectionPoolAutoDiscover(cwd, config.PoolSize)
		}
	}

	if poolErr != nil {
		slog.Warn("failed to initialize daemon connection pool", "err", poolErr)
		slog.Info("web UI will start but API endpoints may not work until daemon is available")
	} else {
		// Wrap pool with circuit breaker for resilience
		breaker := circuitbreaker.NewBreaker("daemon", circuitbreaker.Config{
			FailureThreshold:  5,
			OpenTimeout:       30 * time.Second,
			HalfOpenMaxProbes: 1,
			ShouldTrip:        daemon.DaemonShouldTrip,
			OnStateChange: func(from, to circuitbreaker.State) {
				slog.Info("circuit breaker state change", "component", "circuit_breaker", "from", from, "to", to)
			},
		})
		pool = daemon.NewProtectedPool(rawPool, breaker)
		slog.Info("daemon connection pool initialized with circuit breaker")

		// Test the connection
		func() {
			testCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			client, err := pool.Get(testCtx)
			if err != nil {
				slog.Warn("daemon not available at startup", "err", err)
				slog.Info("API endpoints will attempt to connect when called")
			} else {
				pool.Put(client)
				slog.Info("daemon connection verified")
			}
		}()
	}

	// Create SSE hub for real-time push notifications
	hub := NewSSEHub()
	go hub.Run()

	// Bridge per-workspace daemon mutations to SSE clients.
	multiSub := NewMultiWorkspaceSubscriber(hub, multiPool, config.Logger)
	var getMutationsSince func(wsID string, since int64) []rpc.MutationEvent

	// Create central WorkspaceRegistry to coordinate all workspace lifecycle.
	registry := NewWorkspaceRegistry(multiPool, multiSub, config.PoolSize, config.Logger)
	if pool != nil {
		if err := registry.RegisterPool(initialWorkspaceID, pool); err != nil {
			slog.Warn("failed to register initial workspace", "err", err)
		}
		getMutationsSince = multiSub.GetMutationsSinceForWorkspace
	}

	// NOTE: reconcileConfigWorkspaces called below after fleet registry init.

	// Terminal manager for WebSocket sessions (workspace-scoped prefix prevents collisions).
	termSessionPrefix := fmt.Sprintf("%d-%s", actualPort, workspace.ShortWorkspaceID(initialWorkspaceID))
	var termMgr *TerminalManager
	if termMgr, err = NewTerminalManager(config.TerminalCmd, termSessionPrefix, config.MaxTerminalSessions); err != nil {
		if errors.Is(err, ErrTmuxNotFound) {
			slog.Warn("tmux not found, terminal feature disabled")
		} else {
			slog.Warn("failed to initialize terminal manager", "err", err)
		}
	} else {
		if config.ScrollbackMaxLines > 0 {
			termMgr.SetScrollbackMaxLines(config.ScrollbackMaxLines)
		}
		slog.Info("terminal manager initialized", "component", "terminal", "default_command", config.TerminalCmd)
	}

	// Wire session history callback (closure captures pointer, set after store init below).
	var sessionHistoryStoreRef *sessionhistory.Store
	if termMgr != nil {
		termMgr.SetOnSessionKilled(func(sessionName string) {
			store := sessionHistoryStoreRef
			if store == nil {
				return
			}
			issueID := extractIssueID(sessionName)
			if issueID == "" {
				return
			}
			homeDir, err := os.UserHomeDir()
			if err != nil {
				return
			}
			scrollbackPath := homeDir + "/.loom/session-scrollback/" + sessionName + ".log"
			// Check if file exists before recording path.
			if _, err := os.Stat(scrollbackPath); err != nil {
				scrollbackPath = ""
			}
			if err := store.Complete(context.Background(), initialWorkspaceID, issueID, sessionName, scrollbackPath); err != nil { // TODO(T41): derive workspace from session metadata
				slog.Warn("failed to complete session history", "session", sessionName, "err", err)
			}
		})
	}

	// Initialize terminal auth for one-time WebSocket tokens
	var termAuth *terminalAuth
	if termMgr != nil {
		var authErr error
		termAuth, authErr = newTerminalAuth()
		if authErr != nil {
			slog.Warn("failed to initialize terminal auth, terminal feature disabled", "err", authErr)
			termMgr = nil
		}
	}

	// Initialize SSE token exchange store (external auth mode only).
	// When ExtAuthURL is set, SSE connections require opaque tokens instead of JWTs.
	var sseTokens *sseTokenStore
	if config.ExtAuthURL != "" {
		var sseErr error
		sseTokens, sseErr = newSSETokenStore()
		if sseErr != nil {
			slog.Warn("failed to initialize SSE token store", "err", sseErr)
		}
	}

	// Fleet store registry and JWT config for worker registration.
	var fleetRegistry *fleet.StoreRegistry
	var tokenCfg *TokenConfig
	if config.FleetRedis != nil {
		var err error
		fleetRegistry, err = fleet.NewStoreRegistry(*config.FleetRedis, fleet.DefaultTimeoutConfig(), nil)
		if err != nil {
			slog.Warn("failed to initialize fleet store registry", "component", "fleet", "err", err)
		} else {
			if regErr := fleetRegistry.Register(initialWorkspaceID); regErr != nil {
				slog.Warn("failed to register initial workspace in fleet registry",
					"workspace", initialWorkspaceID, "err", regErr)
			}

			// Use pre-provisioned key if available, otherwise generate ephemeral key
			var jwtKey []byte
			if len(config.FleetJWTKey) > 0 {
				jwtKey = config.FleetJWTKey
				slog.Info("using pre-provisioned JWT signing key", "component", "fleet")
			} else {
				jwtKey = make([]byte, 32)
				if _, err := rand.Read(jwtKey); err != nil {
					slog.Warn("failed to generate JWT signing key", "component", "fleet", "err", err)
					_ = fleetRegistry.Close()
					fleetRegistry = nil
				}
			}
			if fleetRegistry != nil {
				tokenCfg = &TokenConfig{
					SigningKey: jwtKey,
					Expiry:     time.Hour,
				}
				slog.Info("fleet store registry initialized", "component", "fleet", "redis_address", config.FleetRedis.Address)
			}
		}
	}
	if fleetRegistry != nil {
		defer func() { _ = fleetRegistry.Close() }()
	}

	// Initialize fleet claim metrics
	var claimMetrics *fleet.ClaimMetrics
	if fleetRegistry != nil {
		claimMetrics = fleet.NewClaimMetrics()
	}

	// Reconcile all config workspaces (deferred until after fleet registry init
	// so that other workspaces are also registered in the fleet).
	reconcileConfigWorkspaces(config.WorkspaceListFn, initialWorkspaceID, pool != nil, registry, fleetRegistry)

	// Build fleet registration config (API key + rate limiter)
	var fleetRegCfg *FleetRegisterConfig
	if config.FleetAPIKey != "" && fleetRegistry != nil {
		fleetRegCfg = &FleetRegisterConfig{
			APIKey: config.FleetAPIKey,
		}
		// Create rate limiter using a separate Redis client
		if config.FleetRedis != nil {
			rlClient := fleet.NewRedisClient(config.FleetRedis.Address, config.FleetRedis.Password, 0)
			fleetRegCfg.RateLimiter = NewFleetRateLimiter(rlClient, 10, time.Minute)
			defer func() { _ = fleetRegCfg.RateLimiter.Close() }()
		}
		slog.Info("fleet API key authentication enabled", "component", "fleet")
	} else if fleetRegistry != nil && config.FleetAPIKey == "" {
		slog.Warn("fleet store configured but no fleet API key set", "component", "fleet", "env_var", "LOOM_FLEET_API_KEY")
		slog.Warn("fleet registration endpoint will return 503 until fleet API key is configured", "component", "fleet")
	}

	var tabMetaStore *tabmeta.Store
	if config.FleetRedis != nil {
		tmClient := fleet.NewRedisClient(config.FleetRedis.Address, config.FleetRedis.Password, 0)
		tabMetaStore = tabmeta.NewStore(tmClient, nil)
		defer func() { _ = tabMetaStore.Close() }()
		slog.Info("tab metadata store initialized", "redis_address", config.FleetRedis.Address)
		_ = tabMetaStore.MigrateLegacyKeys(ctx, "default")
		if config.WorkspaceConfigFn != nil { // best-effort: migrate name-keyed → UUID-keyed entries
			if wsData, err := config.WorkspaceConfigFn(); err == nil && wsData != nil {
				nameToID := make(map[string]string, len(wsData.Workspaces))
				for _, ws := range wsData.Workspaces {
					if ws.Name != "" && ws.ID != "" {
						nameToID[ws.Name] = ws.ID
					}
				}
				_ = tabMetaStore.MigrateNamedKeys(ctx, nameToID)
			}
		}
	}

	var issueTabStore *issuetabs.Store
	if config.FleetRedis != nil {
		itClient := fleet.NewRedisClient(config.FleetRedis.Address, config.FleetRedis.Password, 0)
		issueTabStore = issuetabs.NewStore(itClient, nil)
		defer func() { _ = issueTabStore.Close() }()
		slog.Info("issue tab store initialized", "redis_address", config.FleetRedis.Address)
		_, _ = issueTabStore.MigrateLegacyKeys(ctx, initialWorkspaceID)
	}
	var sessionHistoryStore *sessionhistory.Store
	if config.FleetRedis != nil {
		shClient := fleet.NewRedisClient(config.FleetRedis.Address, config.FleetRedis.Password, 0)
		sessionHistoryStore = sessionhistory.NewStore(shClient, nil)
		defer func() { _ = sessionHistoryStore.Close() }()
		sessionHistoryStoreRef = sessionHistoryStore
		slog.Info("session history store initialized", "redis_address", config.FleetRedis.Address)
		if n, err := sessionHistoryStore.MigrateLegacyKeys(ctx, initialWorkspaceID); err == nil && n > 0 {
			slog.Info("session history legacy keys migrated", "count", n)
		}
	}

	// Initialize external auth (JWKS cache + middleware)
	extAuthMiddleware, jwksCleanup := initExtAuth(config)
	if jwksCleanup != nil {
		defer jwksCleanup()
	}

	wrappedCreateFn := wrapWorkspaceCreateFn(config.WorkspaceCreateFn, registry, config.WorkspaceIDResolverFn, fleetRegistry)
	wrappedDeleteFn := wrapWorkspaceDeleteFn(config.WorkspaceDeleteFn, registry, fleetRegistry, config.WorkspaceIDResolverFn)
	// Workspace-existence checker for WorkspaceMiddleware (MultiPool is authoritative registry).
	wsExistsFn := func(id string) bool {
		return multiPool.PoolForWorkspace(id) != nil
	}

	// Create HTTP server and register routes (allowedOrigins: nil = same-origin only)
	mux := http.NewServeMux()
	clientErrLimiter, cspLimiter, authCfgLimiter := setupRoutes(mux, pool, multiPool, hub, getMutationsSince, termMgr, termAuth, fleetRegistry, tokenCfg, corsConfig.AllowedOrigins, fleetRegCfg, claimMetrics, config.FleetEnabled, config.DevMode, config.DevFrontendDir, config.LoomServerURL, config.GitOps, config.FileOps, tabMetaStore, issueTabStore, config.WorkspaceConfigFn, wrappedDeleteFn, config.SetDefaultWorkspaceFn, config.ClearDefaultWorkspaceFn, wrappedCreateFn, config.BackendOps, sessionHistoryStore, config.SessionsStore, wsExistsFn, initialWorkspaceID, config.WorkspaceConfigByIDFn, config.ExtAuthURL, sseTokens)
	defer clientErrLimiter.stop()
	defer cspLimiter.stop()
	defer authCfgLimiter.stop()

	registerWorkerAPIRoutes(mux, config.WorkspaceConfigFn)

	// Middleware chain: rate-limit -> security -> auth -> CORS -> mux
	corsMiddleware := NewCORSMiddleware(corsConfig)
	// When no external auth is configured (open mode), use a passthrough middleware.
	authMW := extAuthMiddleware
	if authMW == nil {
		authMW = func(next http.Handler) http.Handler { return next }
	}
	securityMiddleware := NewSecurityHeadersMiddleware(SecurityConfig{
		HSTSEnabled:   config.HSTSEnabled,
		ExtAuthOrigin: extractOrigin(config.ExtAuthURL),
	})
	rl, rateLimitMiddleware := NewRateLimitMiddleware(DefaultRateLimitConfig())
	handler := h2c.NewHandler(NewRequestLogMiddleware(config.Logger)(rateLimitMiddleware(securityMiddleware(authMW(corsMiddleware(mux))))), &http2.Server{})

	// Shutdown context: when canceled, in-flight handlers abort quickly.
	shutdownCtx, shutdownCancel := context.WithCancel(context.Background())
	defer shutdownCancel()

	server := &http.Server{
		Addr:              net.JoinHostPort(config.BindAddress, strconv.Itoa(actualPort)),
		Handler:           handler,
		ReadTimeout:       15 * time.Second,
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      30 * time.Second, // 30s default; streaming handlers (SSE, WebSocket) disable per-connection via ResponseController
		IdleTimeout:       60 * time.Second,
		BaseContext: func(_ net.Listener) context.Context {
			return shutdownCtx
		},
	}

	// Start server in a goroutine using the pre-acquired listener
	serverErr := make(chan error, 1)
	go func() {
		slog.Info("server listening", "address", config.BindAddress, "port", actualPort)
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
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
	drainCtx, drainCancel := context.WithTimeout(context.Background(), config.ShutdownTimeout)
	defer drainCancel()

	if err := server.Shutdown(drainCtx); err != nil {
		return fmt.Errorf("server forced to shutdown: %w", err)
	}
	slog.Info("server stopped")

	// Stop components in reverse-initialization order.

	// Stop rate limiter cleanup goroutine
	rl.Stop()

	// Stop terminal auth cleanup goroutine
	if termAuth != nil {
		termAuth.Stop()
	}

	// Stop SSE token store cleanup goroutine
	if sseTokens != nil {
		sseTokens.Stop()
	}

	// Stop terminal manager (kill tmux sessions and close PTYs)
	if termMgr != nil {
		if err := termMgr.Shutdown(); err != nil {
			slog.Warn("error shutting down terminal manager", "component", "terminal", "err", err)
		} else {
			slog.Info("terminal manager stopped", "component", "terminal")
		}
	}

	_ = registry.Close() // prevent new registrations during shutdown

	// Stop multi-workspace subscriber (no more handlers need it)
	if multiSub != nil {
		multiSub.Stop()
		slog.Info("multi-workspace subscriber stopped")
	}

	// Stop SSE hub (all SSE handlers have exited)
	if hub != nil {
		hub.Stop()
		slog.Info("SSE hub stopped")
	}

	// Close MultiPool (closes all per-workspace pools including the initial one)
	if multiPool != nil {
		if err := multiPool.Close(); err != nil {
			slog.Warn("error closing multi-pool", "err", err)
		} else {
			slog.Info("multi-pool closed")
		}
	}

	return nil
}

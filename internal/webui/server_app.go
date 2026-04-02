package webui

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/tysonthomas9/loomcli/internal/circuitbreaker"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
	"github.com/tysonthomas9/loomcli/internal/webui/fleet"
	"github.com/tysonthomas9/loomcli/internal/webui/issuetabs"
	"github.com/tysonthomas9/loomcli/internal/webui/sessionhistory"
	"github.com/tysonthomas9/loomcli/internal/webui/tabmeta"
	"github.com/tysonthomas9/loomcli/internal/workspace"
)

// NewServer initializes all server dependencies. On failure, it cleans up
// resources allocated before the error point via a cleanup stack.
func NewServer(ctx context.Context, config ServerConfig) (_ *Server, retErr error) { //nolint:gocognit,cyclop,funlen // server initialization requires sequential resource setup
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

	app := &Server{config: config}

	var cleanups []func()
	defer func() {
		if retErr != nil {
			for i := len(cleanups) - 1; i >= 0; i-- {
				cleanups[i]()
			}
		}
	}()

	// Build CORS configuration
	app.corsConfig = CORSConfig{
		Enabled: config.CORSEnabled,
	}
	if config.CORSEnabled {
		if len(config.CORSOrigins) > 0 {
			app.corsConfig.AllowedOrigins = config.CORSOrigins
		} else {
			// Default to Vite dev server
			app.corsConfig.AllowedOrigins = []string{"http://localhost:3000"}
		}
	}

	// Auto-add auth service origin to CORS when external auth is configured
	if authOrigin := extractOrigin(config.ExtAuthURL); authOrigin != "" {
		app.corsConfig.Enabled = true
		app.corsConfig.AllowedOrigins = append(app.corsConfig.AllowedOrigins, authOrigin)
	}

	// Log configuration
	slog.Info("starting web UI server", "port", config.Port, "pool_size", config.PoolSize, "bind_address", config.BindAddress)
	if config.SocketPath != "" {
		slog.Info("daemon socket configured", "socket", config.SocketPath)
	} else {
		slog.Info("daemon socket: auto-detect")
	}
	if app.corsConfig.Enabled {
		slog.Info("CORS enabled", "origins", app.corsConfig.AllowedOrigins)
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
	var err error
	app.listener, app.actualPort, err = findAvailablePort(config.BindAddress, config.Port, config.MaxPortAttempts)
	if err != nil {
		return nil, fmt.Errorf("could not find available port: %w", err)
	}
	cleanups = append(cleanups, func() { _ = app.listener.Close() })
	if app.actualPort != config.Port {
		slog.Info("configured port in use, using fallback", "requested_port", config.Port, "actual_port", app.actualPort)
	}

	// MultiPool for workspace-aware connection routing.
	app.multiPool = daemon.NewMultiPool(WorkspaceFromContext, config.PoolSize)
	cleanups = append(cleanups, func() { _ = app.multiPool.Close() })

	// Initialize the initial workspace connection pool (stable UUID or CWD basename).
	app.initialWorkspaceID = config.InitialWorkspaceID
	if app.initialWorkspaceID == "" {
		app.initialWorkspaceID = "default"
		if cwd, err := os.Getwd(); err == nil {
			app.initialWorkspaceID = filepath.Base(cwd)
		}
	}
	var rawPool *daemon.ConnectionPool
	var poolErr error

	if config.SocketPath != "" {
		rawPool, poolErr = daemon.NewConnectionPool(config.SocketPath, config.PoolSize)
	} else {
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
		breaker := circuitbreaker.NewBreaker("daemon", circuitbreaker.Config{
			FailureThreshold:  5,
			OpenTimeout:       30 * time.Second,
			HalfOpenMaxProbes: 1,
			ShouldTrip:        daemon.DaemonShouldTrip,
			OnStateChange: func(from, to circuitbreaker.State) {
				slog.Info("circuit breaker state change", "component", "circuit_breaker", "from", from, "to", to)
			},
		})
		app.pool = daemon.NewProtectedPool(rawPool, breaker)
		slog.Info("daemon connection pool initialized with circuit breaker")

		func() {
			testCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			client, err := app.pool.Get(testCtx)
			if err != nil {
				slog.Warn("daemon not available at startup", "err", err)
				slog.Info("API endpoints will attempt to connect when called")
			} else {
				app.pool.Put(client)
				slog.Info("daemon connection verified")
			}
		}()
	}

	// Create SSE hub for real-time push notifications
	app.hub = NewSSEHub()
	go app.hub.Run()
	cleanups = append(cleanups, func() { app.hub.Stop() })

	// Bridge per-workspace daemon mutations to SSE clients.
	app.multiSub = NewMultiWorkspaceSubscriber(app.hub, app.multiPool, config.Logger)
	cleanups = append(cleanups, func() { app.multiSub.Stop() })

	// Create central WorkspaceRegistry to coordinate all workspace lifecycle.
	app.registry = NewWorkspaceRegistry(app.multiPool, app.multiSub, config.PoolSize, config.Logger)
	cleanups = append(cleanups, func() { _ = app.registry.Close() })
	if app.pool != nil {
		if err := app.registry.RegisterPool(app.initialWorkspaceID, app.pool); err != nil {
			slog.Warn("failed to register initial workspace", "err", err)
		}
		app.getMutationsSince = app.multiSub.GetMutationsSinceForWorkspace
	}

	// NOTE: reconcileConfigWorkspaces called below after fleet registry init.

	// Terminal manager for WebSocket sessions (workspace-scoped prefix prevents collisions).
	termSessionPrefix := fmt.Sprintf("%d-%s", app.actualPort, workspace.ShortWorkspaceID(app.initialWorkspaceID))
	if app.termMgr, err = NewTerminalManager(config.TerminalCmd, termSessionPrefix, config.MaxTerminalSessions); err != nil {
		if errors.Is(err, ErrTmuxNotFound) {
			slog.Warn("tmux not found, terminal feature disabled")
		} else {
			slog.Warn("failed to initialize terminal manager", "err", err)
		}
	} else {
		if config.ScrollbackMaxLines > 0 {
			app.termMgr.SetScrollbackMaxLines(config.ScrollbackMaxLines)
		}
		cleanups = append(cleanups, func() { _ = app.termMgr.Shutdown() })
		slog.Info("terminal manager initialized", "component", "terminal", "default_command", config.TerminalCmd)
	}

	// Wire session history callback. The closure captures app.sessionHistoryStore,
	// which is set later during store initialization. Safe because the closure
	// is only invoked at runtime (session kill events), never during init.
	if app.termMgr != nil {
		app.termMgr.SetOnSessionKilled(func(sessionName string) {
			store := app.sessionHistoryStore
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
			if _, err := os.Stat(scrollbackPath); err != nil {
				scrollbackPath = ""
			}
			if err := store.Complete(context.Background(), app.initialWorkspaceID, issueID, sessionName, scrollbackPath); err != nil {
				slog.Warn("failed to complete session history", "session", sessionName, "err", err)
			}
		})
	}

	// Initialize terminal auth for one-time WebSocket tokens
	if app.termMgr != nil {
		var authErr error
		app.termAuth, authErr = newTerminalAuth()
		if authErr != nil {
			slog.Warn("failed to initialize terminal auth, terminal feature disabled", "err", authErr)
			app.termMgr = nil
		} else {
			cleanups = append(cleanups, func() { app.termAuth.Stop() })
		}
	}

	// Initialize SSE token exchange store (external auth mode only).
	if config.ExtAuthURL != "" {
		var sseErr error
		app.sseTokens, sseErr = newSSETokenStore()
		if sseErr != nil {
			slog.Warn("failed to initialize SSE token store", "err", sseErr)
		} else {
			cleanups = append(cleanups, func() { app.sseTokens.Stop() })
		}
	}

	// Fleet store registry and JWT config for worker registration.
	if config.FleetRedis != nil {
		app.fleetRegistry, err = fleet.NewStoreRegistry(*config.FleetRedis, fleet.DefaultTimeoutConfig(), nil)
		if err != nil {
			slog.Warn("failed to initialize fleet store registry", "component", "fleet", "err", err)
		} else {
			if regErr := app.fleetRegistry.Register(app.initialWorkspaceID); regErr != nil {
				slog.Warn("failed to register initial workspace in fleet registry",
					"workspace", app.initialWorkspaceID, "err", regErr)
			}

			var jwtKey []byte
			if len(config.FleetJWTKey) > 0 {
				jwtKey = config.FleetJWTKey
				slog.Info("using pre-provisioned JWT signing key", "component", "fleet")
			} else {
				jwtKey = make([]byte, 32)
				if _, err := rand.Read(jwtKey); err != nil {
					slog.Warn("failed to generate JWT signing key", "component", "fleet", "err", err)
					_ = app.fleetRegistry.Close()
					app.fleetRegistry = nil
				}
			}
			if app.fleetRegistry != nil {
				app.tokenCfg = &TokenConfig{
					SigningKey: jwtKey,
					Expiry:     time.Hour,
				}
				// fleetRegistry cleanup is handled by registry.Close() via SetFleetRegistry.
				slog.Info("fleet store registry initialized", "component", "fleet", "redis_address", config.FleetRedis.Address)
			}
		}
	}

	// Initialize fleet claim metrics
	if app.fleetRegistry != nil {
		app.claimMetrics = fleet.NewClaimMetrics()
	}

	// Wire fleet registry into WorkspaceRegistry so Register/Deregister
	// atomically manage all three subsystems (pool, subscriber, fleet).
	if app.fleetRegistry != nil {
		app.registry.SetFleetRegistry(app.fleetRegistry)
	}

	// Reconcile all config workspaces (deferred until after fleet registry init
	// so that other workspaces are also registered in the fleet).
	reconcileConfigWorkspaces(config.WorkspaceListFn, app.initialWorkspaceID, app.pool != nil, app.registry)

	// Build fleet registration config (API key + rate limiter)
	if config.FleetAPIKey != "" && app.fleetRegistry != nil {
		app.fleetRegCfg = &FleetRegisterConfig{
			APIKey: config.FleetAPIKey,
		}
		if config.FleetRedis != nil {
			rlClient := fleet.NewRedisClient(config.FleetRedis.Address, config.FleetRedis.Password, 0)
			app.fleetRegCfg.RateLimiter = NewFleetRateLimiter(rlClient, 10, time.Minute)
			cleanups = append(cleanups, func() { _ = app.fleetRegCfg.RateLimiter.Close() })
		}
		slog.Info("fleet API key authentication enabled", "component", "fleet")
	} else if app.fleetRegistry != nil && config.FleetAPIKey == "" {
		slog.Warn("fleet store configured but no fleet API key set", "component", "fleet", "env_var", "LOOM_FLEET_API_KEY")
		slog.Warn("fleet registration endpoint will return 503 until fleet API key is configured", "component", "fleet")
	}

	if config.FleetRedis != nil {
		tmClient := fleet.NewRedisClient(config.FleetRedis.Address, config.FleetRedis.Password, 0)
		app.tabMetaStore = tabmeta.NewStore(tmClient, nil)
		cleanups = append(cleanups, func() { _ = app.tabMetaStore.Close() })
		slog.Info("tab metadata store initialized", "redis_address", config.FleetRedis.Address)
		_ = app.tabMetaStore.MigrateLegacyKeys(ctx, "default")
		if config.WorkspaceConfigFn != nil {
			if wsData, err := config.WorkspaceConfigFn(); err == nil && wsData != nil {
				nameToID := make(map[string]string, len(wsData.Workspaces))
				for _, ws := range wsData.Workspaces {
					if ws.Name != "" && ws.ID != "" {
						nameToID[ws.Name] = ws.ID
					}
				}
				_ = app.tabMetaStore.MigrateNamedKeys(ctx, nameToID)
			}
		}
	}

	if config.FleetRedis != nil {
		itClient := fleet.NewRedisClient(config.FleetRedis.Address, config.FleetRedis.Password, 0)
		app.issueTabStore = issuetabs.NewStore(itClient, nil)
		cleanups = append(cleanups, func() { _ = app.issueTabStore.Close() })
		slog.Info("issue tab store initialized", "redis_address", config.FleetRedis.Address)
		_, _ = app.issueTabStore.MigrateLegacyKeys(ctx, app.initialWorkspaceID)
	}

	if config.FleetRedis != nil {
		shClient := fleet.NewRedisClient(config.FleetRedis.Address, config.FleetRedis.Password, 0)
		app.sessionHistoryStore = sessionhistory.NewStore(shClient, nil)
		cleanups = append(cleanups, func() { _ = app.sessionHistoryStore.Close() })
		slog.Info("session history store initialized", "redis_address", config.FleetRedis.Address)
		if n, err := app.sessionHistoryStore.MigrateLegacyKeys(ctx, app.initialWorkspaceID); err == nil && n > 0 {
			slog.Info("session history legacy keys migrated", "count", n)
		}
	}

	// Initialize external auth (JWKS cache + middleware)
	app.extAuthMiddleware, app.jwksCleanup = initExtAuth(config)
	if app.jwksCleanup != nil {
		cleanups = append(cleanups, app.jwksCleanup)
	}

	app.wrappedCreateFn = wrapWorkspaceCreateFn(config.WorkspaceCreateFn, app.registry)
	app.wrappedDeleteFn = wrapWorkspaceDeleteFn(config.WorkspaceDeleteFn, app.registry, config.WorkspaceIDResolverFn)

	// Async job store for clone workspace creation (202 + polling).
	app.jobStore = NewWorkspaceJobStore()
	cleanups = append(cleanups, func() { app.jobStore.Stop() })

	// Workspace-existence checker for WorkspaceMiddleware (MultiPool is authoritative registry).
	app.wsExistsFn = func(id string) bool {
		return app.multiPool.PoolForWorkspace(id) != nil
	}

	// Generate and persist notify token for session change endpoint auth.
	app.notifyToken, app.notifyTokenFile = generateNotifyToken(config.NotifyTokenDir)

	app.buildHandlers()

	// Create mux and register all routes.
	app.mux = http.NewServeMux()
	app.registerRoutes()
	app.registerWorkerAPIRoutes()

	return app, nil
}

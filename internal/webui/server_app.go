package webui

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/tysonthomas9/loomcli/internal/circuitbreaker"
	"github.com/tysonthomas9/loomcli/internal/webui/coordinator"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
	"github.com/tysonthomas9/loomcli/internal/webui/fleet"
	"github.com/tysonthomas9/loomcli/internal/webui/issuetabs"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
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

	initLogger(config.Logger)

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
	app.corsConfig = middleware.CORSConfig{
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
	if authOrigin := middleware.ExtractOrigin(config.ExtAuthURL); authOrigin != "" {
		app.corsConfig.Enabled = true
		app.corsConfig.AllowedOrigins = append(app.corsConfig.AllowedOrigins, authOrigin)
	}

	// Log configuration
	logger.Info("starting web UI server", "port", config.Port, "pool_size", config.PoolSize, "bind_address", config.BindAddress)
	if config.SocketPath != "" {
		logger.Info("daemon socket configured", "socket", config.SocketPath)
	} else {
		logger.Info("daemon socket: auto-detect")
	}
	if app.corsConfig.Enabled {
		logger.Info("CORS enabled", "origins", app.corsConfig.AllowedOrigins)
	}
	if config.HSTSEnabled {
		logger.Info("HSTS enabled: ensure this server is behind a TLS-terminating proxy")
	}
	if config.ExtAuthURL == "" && config.BindAddress != "127.0.0.1" && config.BindAddress != "::1" {
		logger.Warn("no authentication configured and server is exposed to network", "bind_address", config.BindAddress)
	}
	if config.DevMode {
		dir := config.DevFrontendDir
		if dir == "" {
			dir = "internal/webui/frontend/dist"
		}
		logger.Info("dev mode enabled", "frontend_dir", dir)
	} else {
		logFrontendBuildMeta()
	}
	if config.FleetEnabled {
		logger.Info("fleet routes enabled")
	}

	// Find an available port (auto-fallback if requested port is in use)
	var err error
	app.listener, app.actualPort, err = findAvailablePort(config.BindAddress, config.Port, config.MaxPortAttempts)
	if err != nil {
		return nil, fmt.Errorf("could not find available port: %w", err)
	}
	cleanups = append(cleanups, func() { _ = app.listener.Close() })
	if app.actualPort != config.Port {
		logger.Info("configured port in use, using fallback", "requested_port", config.Port, "actual_port", app.actualPort)
	}

	// MultiPool for workspace-aware connection routing.
	app.multiPool = daemon.NewMultiPool(middleware.WorkspaceFromContext, config.PoolSize)
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
			logger.Warn("failed to get current directory", "err", err)
		} else {
			rawPool, poolErr = daemon.NewConnectionPoolAutoDiscover(cwd, config.PoolSize)
		}
	}

	if poolErr != nil {
		logger.Warn("failed to initialize daemon connection pool", "err", poolErr)
		logger.Info("web UI will start but API endpoints may not work until daemon is available")
	} else {
		breaker := circuitbreaker.NewBreaker("daemon", circuitbreaker.Config{
			FailureThreshold:  5,
			OpenTimeout:       30 * time.Second,
			HalfOpenMaxProbes: 1,
			ShouldTrip:        daemon.DaemonShouldTrip,
			OnStateChange: func(from, to circuitbreaker.State) {
				logger.Info("circuit breaker state change", "component", "circuit_breaker", "from", from, "to", to)
			},
		})
		app.pool = daemon.NewProtectedPool(rawPool, breaker)
		logger.Info("daemon connection pool initialized with circuit breaker")

		func() {
			testCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			client, err := app.pool.Get(testCtx)
			if err != nil {
				logger.Warn("daemon not available at startup", "err", err)
				logger.Info("API endpoints will attempt to connect when called")
			} else {
				app.pool.Put(client)
				logger.Info("daemon connection verified")
			}
		}()
	}

	// Initialize issue service layer
	app.issueSvc = service.NewIssueService(app.pool, app.multiPool, middleware.WithWorkspace)

	// Create SSE hub for real-time push notifications
	app.hub = realtime.NewHub()
	go app.hub.Run()
	cleanups = append(cleanups, func() { app.hub.Stop() })

	// Bridge per-workspace daemon mutations to SSE clients.
	app.multiSub = NewMultiWorkspaceSubscriber(app.hub, app.multiPool, config.Logger)
	cleanups = append(cleanups, func() { app.multiSub.Stop() })

	// NOTE: coordinator.WorkspaceRegistry construction is deferred until after
	// TerminalManager and FleetStoreRegistry init, because all hooks must exist
	// before the first Register call.

	// Terminal manager for WebSocket sessions (workspace-scoped prefix prevents collisions).
	termSessionPrefix := fmt.Sprintf("%d-%s", app.actualPort, workspace.ShortWorkspaceID(app.initialWorkspaceID))
	if app.termMgr, err = NewTerminalManager(config.TerminalCmd, termSessionPrefix, config.MaxTerminalSessions); err != nil {
		if errors.Is(err, ErrTmuxNotFound) {
			logger.Warn("tmux not found, terminal feature disabled")
		} else {
			logger.Warn("failed to initialize terminal manager", "err", err)
		}
	} else {
		if config.ScrollbackMaxLines > 0 {
			app.termMgr.SetScrollbackMaxLines(config.ScrollbackMaxLines)
		}
		cleanups = append(cleanups, func() { _ = app.termMgr.Shutdown() })
		logger.Info("terminal manager initialized", "component", "terminal", "default_command", config.TerminalCmd)
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
				logger.Warn("failed to complete session history", "session", sessionName, "err", err)
			}
		})
	}

	// Initialize terminal auth for one-time WebSocket tokens
	if app.termMgr != nil {
		var authErr error
		app.termAuth, authErr = realtime.NewTerminalAuth()
		if authErr != nil {
			logger.Warn("failed to initialize terminal auth, terminal feature disabled", "err", authErr)
			app.termMgr = nil
		} else {
			cleanups = append(cleanups, func() { app.termAuth.Stop() })
		}
	}

	// Initialize agent service layer (requires GitOps; termMgr/termAuth may be nil)
	if config.GitOps != nil {
		app.agentSvc = NewAgentService(config.GitOps, app.termMgr, app.termAuth)
	}

	// Initialize SSE token exchange store (external auth mode only).
	if config.ExtAuthURL != "" {
		var sseErr error
		app.sseTokens, sseErr = realtime.NewTokenStore()
		if sseErr != nil {
			logger.Warn("failed to initialize SSE token store", "err", sseErr)
		} else {
			cleanups = append(cleanups, func() { app.sseTokens.Stop() })
		}
	}

	// Fleet store registry and JWT config for worker registration.
	if config.FleetRedis != nil {
		app.fleetRegistry, err = fleet.NewStoreRegistry(*config.FleetRedis, fleet.DefaultTimeoutConfig(), nil)
		if err != nil {
			logger.Warn("failed to initialize fleet store registry", "component", "fleet", "err", err)
		} else {
			var jwtKey []byte
			if len(config.FleetJWTKey) > 0 {
				jwtKey = config.FleetJWTKey
				logger.Info("using pre-provisioned JWT signing key", "component", "fleet")
			} else {
				jwtKey = make([]byte, 32)
				if _, err := rand.Read(jwtKey); err != nil {
					logger.Warn("failed to generate JWT signing key", "component", "fleet", "err", err)
					_ = app.fleetRegistry.Close()
					app.fleetRegistry = nil
				}
			}
			if app.fleetRegistry != nil {
				app.tokenCfg = &TokenConfig{
					SigningKey: jwtKey,
					Expiry:     time.Hour,
				}
				logger.Info("fleet store registry initialized", "component", "fleet", "redis_address", config.FleetRedis.Address)
			}
		}
	}

	// Initialize fleet claim metrics
	if app.fleetRegistry != nil {
		app.claimMetrics = fleet.NewClaimMetrics()
	}

	// Construct lifecycle hooks in canonical order. In fleet mode, beads-pool and
	// notification-subscriber are suppressed (fleet server manages agents).
	var beadsPoolHook *BeadsPoolHook
	app.registry = coordinator.NewWorkspaceRegistry(config.Logger)
	cleanups = append(cleanups, func() { _ = app.registry.Close() })
	if config.FleetMode {
		logger.Info("beads pool and notification subscriber suppressed (fleet mode)", "component", "fleet_mode")
	} else {
		beadsPoolHook = NewBeadsPoolHook(app.multiPool, config.PoolSize, config.Logger)
		notifHook := NewNotificationSubscriberHook(app.multiSub, config.Logger)
		_ = app.registry.AddHook(beadsPoolHook)
		_ = app.registry.AddHook(notifHook)
	}

	if app.termMgr != nil {
		_ = app.registry.AddHook(NewTerminalHook(app.termMgr, config.Logger))
	}
	if app.fleetRegistry != nil {
		_ = app.registry.AddHook(NewFleetStoreHook(app.fleetRegistry, config.Logger))
	}
	if config.FleetClientURL != "" {
		_ = app.registry.AddHook(NewFleetBackendHook(config.FleetClientURL, config.FleetClientWorkspace, config.FleetClientAPIKey, config.Logger))
	}

	// Register the initial workspace (replaces old RegisterPool pattern).
	if app.pool != nil {
		if beadsPoolHook != nil {
			beadsPoolHook.SetPrebuiltPool(app.initialWorkspaceID, app.pool)
		}
		var initialWSPath string
		if config.WorkspaceListFn != nil {
			if wsMap, listErr := config.WorkspaceListFn(); listErr == nil {
				initialWSPath = wsMap[app.initialWorkspaceID]
			}
		}
		if initialWSPath == "" {
			if cwd, cwdErr := getCwd(); cwdErr == nil {
				initialWSPath = cwd
			}
		}
		if err := app.registry.Register(app.initialWorkspaceID, initialWSPath); err != nil {
			logger.Warn("failed to register initial workspace", "err", err)
		}
		app.getMutationsSince = app.multiSub.GetMutationsSinceForWorkspace
	}

	// Reconcile all config workspaces (hooks handle pool, subscriber, terminal, fleet atomically).
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
		logger.Info("fleet API key authentication enabled", "component", "fleet")
	} else if app.fleetRegistry != nil && config.FleetAPIKey == "" {
		logger.Warn("fleet store configured but no fleet API key set", "component", "fleet", "env_var", "LOOM_FLEET_API_KEY")
		logger.Warn("fleet registration endpoint will return 503 until fleet API key is configured", "component", "fleet")
	}

	if config.FleetRedis != nil {
		tmClient := fleet.NewRedisClient(config.FleetRedis.Address, config.FleetRedis.Password, 0)
		app.tabMetaStore = tabmeta.NewStore(tmClient, nil)
		cleanups = append(cleanups, func() { _ = app.tabMetaStore.Close() })
		logger.Info("tab metadata store initialized", "redis_address", config.FleetRedis.Address)
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
		logger.Info("issue tab store initialized", "redis_address", config.FleetRedis.Address)
		_, _ = app.issueTabStore.MigrateLegacyKeys(ctx, app.initialWorkspaceID)
	}

	if config.FleetRedis != nil {
		shClient := fleet.NewRedisClient(config.FleetRedis.Address, config.FleetRedis.Password, 0)
		app.sessionHistoryStore = sessionhistory.NewStore(shClient, nil)
		cleanups = append(cleanups, func() { _ = app.sessionHistoryStore.Close() })
		logger.Info("session history store initialized", "redis_address", config.FleetRedis.Address)
		if n, err := app.sessionHistoryStore.MigrateLegacyKeys(ctx, app.initialWorkspaceID); err == nil && n > 0 {
			logger.Info("session history legacy keys migrated", "count", n)
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

	// Initialize workspace service layer
	app.workspaceSvc = service.NewWorkspaceService(service.WorkspaceServiceConfig{
		ConfigFn:       config.WorkspaceConfigFn,
		ConfigByIDFn:   config.WorkspaceConfigByIDFn,
		MultiPool:      app.multiPool,
		CreateFn:       app.wrappedCreateFn,
		DeleteFn:       app.wrappedDeleteFn,
		JobStore:       app.jobStore,
		SetDefaultFn:   config.SetDefaultWorkspaceFn,
		ClearDefaultFn: config.ClearDefaultWorkspaceFn,
	})

	// Generate and persist notify token for session change endpoint auth.
	app.notifyToken, app.notifyTokenFile = generateNotifyToken(config.NotifyTokenDir)

	// Initialize terminal service layer (requires termMgr, stores)
	if app.termMgr != nil {
		var configPool configConnectionGetter
		if app.multiPool != nil {
			configPool = &configPoolAdapter{pool: app.multiPool}
		}
		var rc *redis.Client
		if app.tabMetaStore != nil {
			rc = app.tabMetaStore.RedisClient()
		}
		app.termSvc = NewTerminalService(
			app.termMgr, app.termAuth, configPool,
			app.tabMetaStore, app.hub, app.sessionHistoryStore, rc,
		)
	}

	// Initialize diff service layer (requires GitOps)
	if config.GitOps != nil {
		app.diffSvc = NewDiffService(config.GitOps, app.multiPool)
	}

	// Initialize file service layer (requires FileOps)
	if config.FileOps != nil {
		app.fileSvc = NewFileService(config.FileOps)
	}

	// Initialize session service layer (always constructed; stores may be nil internally)
	app.sessSvc = NewSessionService(config.SessionsStore, app.sessionHistoryStore)

	app.buildHandlers()

	// Create mux and register all routes.
	app.mux = http.NewServeMux()
	app.registerRoutes()
	app.registerWorkerAPIRoutes()

	return app, nil
}

// initExtAuth initializes external JWT auth from ServerConfig. Returns
// the middleware (nil if unconfigured) and a cleanup function for the JWKS cache.
func initExtAuth(config ServerConfig) (middleware.Middleware, func()) {
	if config.ExtAuthURL == "" {
		return nil, nil
	}
	jwksURL := config.ExtAuthURL + "/api/auth/jwks"
	var jwksClient *http.Client
	if config.ExtAuthAllowInsecure {
		jwksClient = middleware.NewJWKSHTTPClient(safeDialContext(true))
	} else {
		jwksClient = middleware.NewJWKSHTTPClient(safeDialContext(false))
	}
	jwksCache := middleware.NewJWKSCache(jwksURL, jwksClient, config.Logger)

	mw := middleware.Auth(middleware.AuthConfig{
		JWKSCache: jwksCache,
		Issuer:    config.ExtAuthIssuer,
		Audience:  config.ExtAuthAudience,
		Logger:    config.Logger,
	})
	logger.Info("external auth enabled",
		"component", "auth",
		"auth_url", config.ExtAuthURL,
		"jwks_url", jwksURL,
		"issuer", config.ExtAuthIssuer,
	)
	return mw, jwksCache.Stop
}

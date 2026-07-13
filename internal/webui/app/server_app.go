package app

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/tysonthomas9/loomcli/internal/webui"
	"github.com/tysonthomas9/loomcli/internal/webui/appinfra"
	"github.com/tysonthomas9/loomcli/internal/webui/appstores"

	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
	"github.com/tysonthomas9/loomcli/internal/webui/storeadapter"
	"github.com/tysonthomas9/loomcli/internal/webui/svcimpl"
	"github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

// NewServer initializes all server dependencies. On failure, it cleans up
// resources allocated before the error point via a cleanup stack.
func NewServer(ctx context.Context, config webui.ServerConfig) (_ *Server, retErr error) { //nolint:gocognit,cyclop,funlen // server initialization requires sequential resource setup
	// Apply defaults for zero values
	if config.Port == 0 {
		config.Port = webui.DefaultPort
	}
	if config.PoolSize == 0 {
		config.PoolSize = webui.DefaultPoolSize
	}
	if config.ShutdownTimeout == 0 {
		config.ShutdownTimeout = webui.DefaultShutdownTimeout
	}
	if config.MaxPortAttempts == 0 {
		config.MaxPortAttempts = webui.DefaultMaxPortAttempts
	}
	if config.BindAddress == "" {
		config.BindAddress = "127.0.0.1"
	}

	initLogger(config.Logger)

	app := &Server{config: config, startedAt: time.Now()}

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
	daemonExpected := !config.FleetClient
	if config.SocketPath != "" && daemonExpected {
		logger.Info("daemon socket configured", "socket", config.SocketPath)
	} else if daemonExpected {
		logger.Info("daemon socket: auto-detect")
	} else {
		logger.Info("issue daemon disabled; using FleetDB-backed issue service")
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
	if config.FrontendDir == "" {
		logger.Info("api-only mode — frontend served externally")
	} else {
		logger.Info("serving bundled frontend", "dir", config.FrontendDir)
	}
	if config.FleetEnabled {
		logger.Info("fleet routes enabled")
	}

	// Find an available port (auto-fallback if requested port is in use)
	var err error
	app.listener, app.actualPort, err = webui.FindAvailablePort(config.BindAddress, config.Port, config.MaxPortAttempts)
	if err != nil {
		return nil, fmt.Errorf("could not find available port: %w", err)
	}
	cleanups = append(cleanups, func() { _ = app.listener.Close() })
	if app.actualPort != config.Port {
		logger.Info("configured port in use, using fallback", "requested_port", config.Port, "actual_port", app.actualPort)
	}

	// MultiPool for workspace-aware connection routing.
	app.multiPool = appinfra.NewMultiPool(middleware.WorkspaceFromContext, config.PoolSize)
	cleanups = append(cleanups, func() { _ = app.multiPool.Close() })

	// Initialize the initial workspace connection pool only for daemon-backed
	// deployments. Store-backed local/cloud modes route issue traffic through
	// FleetDB and must not probe or trip a local daemon circuit breaker.
	app.initialWorkspaceID = config.InitialWorkspaceID
	var rawPool *appinfra.ConnectionPool
	var poolErr error

	if daemonExpected {
		if config.SocketPath != "" {
			rawPool, poolErr = appinfra.NewConnectionPool(config.SocketPath, config.PoolSize)
		} else {
			cwd, err := appinfra.GetCwd()
			if err != nil {
				logger.Warn("failed to get current directory", "err", err)
			} else {
				rawPool, poolErr = appinfra.NewConnectionPoolAutoDiscover(cwd, config.PoolSize)
			}
		}

		if poolErr != nil {
			logger.Warn("failed to initialize daemon connection pool", "err", poolErr)
			logger.Info("web UI will start but API endpoints may not work until daemon is available")
		} else {
			app.pool = appinfra.InitProtectedPool(rawPool, config.Logger)
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
	}

	// Initialize issue service layer.
	//
	// When the cli wiring supplies an IssueBackend factory, prefer the
	// backend-aware constructor so migrated CRUD methods route through
	// backend.IssueBackend. The pool
	// stays around to back the not-yet-migrated paths (ListIssues/Kanban
	// and the cross-workspace MoveIssue helper).
	if config.IssueBackendFn != nil {
		app.issueSvc = service.NewIssueServiceWithBackend(
			app.pool, app.multiPool, middleware.WithWorkspace,
			service.IssueBackendProvider(config.IssueBackendFn),
		)
	} else {
		app.issueSvc = service.NewIssueService(app.pool, app.multiPool, middleware.WithWorkspace)
	}

	// Create SSE hub for real-time push notifications
	app.hub = appstores.NewHub()
	go app.hub.Run()
	cleanups = append(cleanups, func() { app.hub.Stop() })

	// Bridge per-workspace backend mutations to SSE clients.
	app.multiSub = appstores.NewMultiSub(ctx, app.hub, config.Logger)
	app.getMutationsSince = appstores.GetMutationsSinceFn(app.multiSub)
	cleanups = append(cleanups, func() { app.multiSub.Stop() })

	// Main web terminal manager. In desktop local mode this is normally a
	// client to the persistent terminal host so PTYs survive serve/app
	// restarts. In development and tests, keep the in-process fallback.
	if config.TerminalHostSocket != "" {
		hostClient := terminal.NewTerminalHostClient(config.TerminalHostSocket, config.MaxTerminalSessions)
		app.ptyMgr = hostClient
		cleanups = append(cleanups, func() { _ = hostClient.Close() })
		logger.Info("terminal host client initialized", "component", "terminal", "socket", config.TerminalHostSocket)
	} else {
		multi := terminal.NewMultiPTYManager(config.TerminalCmd, config.MaxTerminalSessions)
		app.ptyMgr = multi
		cleanups = append(cleanups, func() { _ = multi.Close() })
		logger.Info("multi pty manager initialized", "component", "terminal", "default_command", config.TerminalCmd)
	}

	// Agent-view tmux manager: kept only so the web UI can attach to tmux
	// sessions that the CLI auto-mode creates for agents. Missing tmux is a
	// soft failure — the agent-terminal live view is disabled, archive logs
	// still work.
	if mgr, agentErr := terminal.NewAgentTmuxManager(config.MaxTerminalSessions); agentErr != nil {
		if errors.Is(agentErr, terminal.ErrTmuxNotFound) {
			logger.Warn("tmux not found, agent terminal live view disabled")
		} else {
			logger.Warn("failed to initialize agent tmux manager", "err", agentErr)
		}
	} else {
		app.agentTmuxMgr = mgr
		cleanups = append(cleanups, func() { _ = app.agentTmuxMgr.Shutdown() })
		logger.Info("agent tmux manager initialized", "component", "terminal")
	}

	// Terminal auth for one-time WebSocket tokens.
	{
		var authErr error
		app.termAuth, authErr = appstores.NewTerminalAuth()
		if authErr != nil {
			logger.Warn("failed to initialize terminal auth; token endpoint disabled", "err", authErr)
		} else {
			cleanups = append(cleanups, func() { app.termAuth.Stop() })
		}
	}

	// Initialize agent service layer (requires ops.GitOps; agentTmuxMgr/termAuth may be nil)
	if config.GitOps != nil {
		app.agentSvc = svcimpl.NewAgentService(config.GitOps, app.agentTmuxMgr, app.termAuth, config.Store)
	}

	// Initialize SSE token exchange store (external auth mode only).
	if config.ExtAuthURL != "" {
		var sseErr error
		app.sseTokens, sseErr = appstores.NewTokenStore()
		if sseErr != nil {
			logger.Warn("failed to initialize SSE token store", "err", sseErr)
		} else {
			cleanups = append(cleanups, func() { app.sseTokens.Stop() })
		}
	}

	// Fleet store registry and JWT config for worker registration.
	if config.FleetRedis != nil {
		app.fleetRegistry, err = appinfra.InitFleetRegistry(*config.FleetRedis, config.Logger)
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
				app.tokenCfg = appinfra.NewFleetTokenConfig(jwtKey, time.Hour)
				logger.Info("fleet store registry initialized", "component", "fleet", "redis_address", config.FleetRedis.Address)
			}
		}
	}

	// Initialize fleet claim metrics
	if app.fleetRegistry != nil {
		app.claimMetrics = appinfra.NewFleetClaimMetrics()
	}

	// Construct lifecycle hooks.
	app.registry = appinfra.NewWorkspaceRegistry(config.Logger)
	cleanups = append(cleanups, func() { _ = app.registry.Close() })

	var ptyRegistrar terminal.WorkspaceRegistrar
	if registrar, ok := app.ptyMgr.(terminal.WorkspaceRegistrar); ok {
		ptyRegistrar = registrar
	}

	appinfra.RegisterHooks(app.registry, appinfra.HookConfig{
		MultiPool:  app.multiPool,
		PoolSize:   config.PoolSize,
		MultiSub:   app.multiSub,
		TermMgr:    app.agentTmuxMgr,
		PTYMgr:     ptyRegistrar,
		FleetReg:   app.fleetRegistry,
		FleetURL:   config.FleetClientURL,
		FleetWS:    config.FleetClientWorkspace,
		FleetKey:   config.FleetClientAPIKey,
		FleetActor: config.FleetClientActor,
		FleetMode:  config.FleetMode,
		Logger:     config.Logger,
	})

	workspacePathsFn := storeWorkspacePathsFn(ctx, config)

	// Register the initial workspace.
	if app.pool != nil && shouldRegisterInitialWorkspace(workspacePathsFn, app.initialWorkspaceID) {
		var initialWSPath string
		if workspacePathsFn != nil {
			if wsMap, listErr := workspacePathsFn(); listErr == nil {
				initialWSPath = wsMap[app.initialWorkspaceID]
			}
		}
		if err := app.registry.Register(app.initialWorkspaceID, initialWSPath); err != nil {
			logger.Warn("failed to register initial workspace", "err", err)
		}
	}

	// Reconcile all store workspaces. Subscribers for secondary workspaces
	// activate lazily via the workspace SSE token/stream routes.
	appinfra.ReconcileStoreWorkspaces(workspacePathsFn, app.initialWorkspaceID, app.pool != nil, app.registry, config.Logger)

	// Periodic re-reconcile: workspaces created out-of-band (CLI
	// `loom workspace create` while serve is running, or another
	// loom-serve instance against shared fleet-db) need to be picked up
	// without a serve restart. Without this, terminal attach for those
	// workspaces fails with "workspace not registered" until restart.
	appinfra.StartPeriodicWorkspaceReconcile(ctx, workspacePathsFn, app.registry, 15*time.Second, config.Logger)

	if config.DaemonStartupFn != nil {
		onReady := func(string) {}
		go config.DaemonStartupFn(ctx, onReady)
	}

	// Start health doctor to auto-restart daemons with stuck circuit breakers.
	if daemonExpected && workspacePathsFn != nil {
		doctor := webui.NewHealthDoctor(app.multiPool, workspacePathsFn, config.Logger, webui.DefaultHealthDoctorConfig())
		go doctor.Run(ctx)
	}

	// Build fleet registration config.
	if config.FleetAPIKey != "" && app.fleetRegistry != nil {
		var regCleanup func()
		app.fleetRegCfg, regCleanup = appinfra.NewFleetRegisterConfig(config.FleetAPIKey, config.FleetRedis, config.Logger)
		if regCleanup != nil {
			cleanups = append(cleanups, regCleanup)
		}
	} else if app.fleetRegistry != nil && config.FleetAPIKey == "" {
		logger.Warn("fleet store configured but no fleet API key set", "component", "fleet", "env_var", "LOOM_FLEET_API_KEY")
		logger.Warn("fleet registration endpoint will return 503 until fleet API key is configured", "component", "fleet")
	}

	if config.FleetRedis != nil {
		var tmCleanup func()
		app.tabMetaStore, tmCleanup = appstores.InitTabMeta(ctx, config.FleetRedis, config.Logger)
		cleanups = append(cleanups, tmCleanup)
	}

	if config.FleetRedis != nil {
		var itCleanup func()
		app.issueTabStore, itCleanup = appstores.InitIssueTabs(ctx, config.FleetRedis, app.initialWorkspaceID, config.Logger)
		cleanups = append(cleanups, itCleanup)
	}

	if config.FleetRedis != nil {
		var shCleanup func()
		app.sessionHistoryStore, shCleanup = appstores.InitSessionHistory(ctx, config.FleetRedis, app.initialWorkspaceID, config.Logger)
		cleanups = append(cleanups, shCleanup)
	}

	// Initialize external auth (JWKS cache + middleware)
	app.extAuthMiddleware, app.jwksCleanup = initExtAuth(config)
	if app.jwksCleanup != nil {
		cleanups = append(cleanups, app.jwksCleanup)
	}

	app.wrappedCreateFn = wrapWorkspaceCreateFn(config.WorkspaceCreateFn, app.registry)
	app.wrappedDeleteFn = wrapWorkspaceDeleteFn(config.WorkspaceDeleteFn, app.registry, config.WorkspaceIDResolverFn)

	// Async job store for clone workspace creation (202 + polling).
	app.jobStore = svcimpl.NewWorkspaceJobStore()
	cleanups = append(cleanups, func() { app.jobStore.Stop() })

	// Workspace-existence checker. Subscriber activation is deliberately not
	// part of existence resolution: only workspace SSE token/stream routes start
	// FleetDB mutation long-polls.
	//
	// Workspace IDs in URLs are usually UUIDs, but external callers (CLI
	// tooling, parity tests, the fdb client) frequently use the workspace
	// *name* instead. When the direct UUID lookup misses, the resolver maps
	// a name like "PARITY" to the registered UUID rather than 404'ing.
	//
	// The unified store is also authoritative for fleet-db-only workspaces
	// that are not in the daemon registry, so without this check every
	// `/api/workspaces/{ws}/...` route would 404 before the handler could read
	// state from fleet-db.
	wsResolver := config.WorkspaceIDResolverFn
	wsStore := config.Store
	app.wsResolveFn = func(reqCtx context.Context, id string) (middleware.WorkspaceRef, bool) {
		ref := middleware.WorkspaceRef{RequestedID: id, CanonicalID: id}
		if app.registry.Registered(id) {
			return ref, true
		}
		if wsResolver != nil {
			if uuid, err := wsResolver(id); err == nil && uuid != "" && app.registry.Registered(uuid) {
				ref.CanonicalID = uuid
				return ref, true
			}
		}
		if wsStore != nil {
			if reqCtx == nil {
				reqCtx = ctx
			}
			if key, err := storeadapter.ResolveWorkspaceKeyByName(reqCtx, wsStore, id); err == nil && key != "" {
				ref.CanonicalID = key
				return ref, true
			}
		}
		return middleware.WorkspaceRef{}, false
	}
	app.wsExistsFn = func(id string) bool {
		_, ok := app.wsResolveFn(context.Background(), id)
		return ok
	}

	// Initialize workspace service layer. FleetDB Store is the authoritative
	// workspace source in both local and distributed modes.
	app.workspaceSvc = service.NewWorkspaceService(service.WorkspaceServiceConfig{
		Store:          config.Store,
		MultiPool:      app.multiPool,
		CreateFn:       app.wrappedCreateFn,
		AddReposFn:     config.WorkspaceAddReposFn,
		DeleteFn:       app.wrappedDeleteFn,
		JobStore:       app.jobStore,
		SetDefaultFn:   config.SetDefaultWorkspaceFn,
		ClearDefaultFn: config.ClearDefaultWorkspaceFn,
	})

	// Generate and persist notify token for session change endpoint auth.
	app.notifyToken, app.notifyTokenFile = generateNotifyToken(config.NotifyTokenDir)

	// Initialize terminal service layer. The surviving methods are tab
	// metadata CRUD, terminal UI state, and the WS auth token issuer — all
	// backed by Redis / in-memory state, no tmux required.
	{
		var rc *redis.Client
		if app.tabMetaStore != nil {
			rc = app.tabMetaStore.RedisClient()
		}
		app.termSvc = terminal.NewTerminalService(
			app.termAuth, app.tabMetaStore, app.hub, rc, app.ptyMgr, app.startedAt,
		)
	}

	// Initialize diff service layer (requires ops.GitOps)
	if config.GitOps != nil {
		app.diffSvc = svcimpl.NewDiffService(config.GitOps, app.multiPool)
	}

	// Initialize file service layer (requires ops.FileOps)
	if config.FileOps != nil {
		app.fileSvc = svcimpl.NewFileService(config.FileOps)
	}

	// Initialize session service layer (always constructed; stores may be nil internally)
	app.sessSvc = svcimpl.NewSessionServiceWithRuntimeDir(config.Store, app.sessionHistoryStore, config.SessionRuntimeDir)

	app.buildHandlers()
	app.buildModules()

	// Create mux and register all routes.
	app.mux = http.NewServeMux()
	app.registerRoutes()
	app.registerWorkerAPIRoutes()

	return app, nil
}

func (app *Server) activateSSESubscriber(ctx context.Context, wsID string) {
	if app == nil || wsID == "" || app.multiSub == nil {
		return
	}
	if app.registry != nil && app.registry.Registered(wsID) {
		if err := app.registry.ActivateSubscriber(wsID); err != nil {
			logger.Warn("failed to activate registered workspace SSE subscriber",
				"workspace", wsID, "err", err)
		}
		return
	}
	app.ensureStoreBackedSSESubscriber(ctx, wsID)
}

func (app *Server) ensureStoreBackedSSESubscriber(ctx context.Context, wsID string) {
	if app == nil || wsID == "" || app.multiSub == nil || app.config.IssueBackendFn == nil {
		return
	}
	if app.multiSub.HasSubscriber(wsID) {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	be := app.config.IssueBackendFn(middleware.WithWorkspace(ctx, wsID))
	if be == nil {
		return
	}
	if err := app.multiSub.EnsureActive(ctx, wsID, be, appstores.ActivationReasonSSE); err != nil {
		logger.Warn("failed to start store-backed workspace subscriber",
			"workspace", wsID, "err", err)
	}
}

func storeWorkspacePathsFn(ctx context.Context, config webui.ServerConfig) func() (map[string]string, error) {
	if config.Store == nil {
		return nil
	}
	// Capture only the store handle, not the whole ServerConfig, so this
	// long-lived closure (held by the reconcile/health goroutines) doesn't
	// retain the full config struct for the process lifetime.
	store := config.Store
	return func() (map[string]string, error) {
		// Healing variant: re-bind a workspace whose local path is missing from
		// state.json to an existing on-disk checkout, so reconciliation recovers
		// the terminal/readyz instead of leaving the workspace degraded.
		return storeadapter.ListWorkspacePathsOrHeal(ctx, store)
	}
}

func shouldRegisterInitialWorkspace(workspacePathsFn func() (map[string]string, error), workspaceID string) bool {
	if workspaceID == "" {
		return false
	}
	if workspacePathsFn == nil {
		return false
	}
	workspaces, err := workspacePathsFn()
	if err != nil {
		return false
	}
	path, ok := workspaces[workspaceID]
	return ok && path != ""
}

package app

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

	"github.com/tysonthomas9/loomcli/internal/webui"
	"github.com/tysonthomas9/loomcli/internal/webui/appinfra"
	"github.com/tysonthomas9/loomcli/internal/webui/appstores"

	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
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
	logger.Info("api-only mode — frontend served externally")
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

	// Initialize the initial workspace connection pool (stable UUID or CWD basename).
	app.initialWorkspaceID = config.InitialWorkspaceID
	if app.initialWorkspaceID == "" && !(config.FleetMode && config.Store != nil) {
		app.initialWorkspaceID = "default"
		if cwd, err := os.Getwd(); err == nil {
			app.initialWorkspaceID = filepath.Base(cwd)
		}
	}
	var rawPool *appinfra.ConnectionPool
	var poolErr error

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

	// Initialize issue service layer.
	//
	// When the cli wiring supplies an IssueBackend factory, prefer the
	// backend-aware constructor so migrated CRUD methods route through
	// backend.IssueBackend (which supports both beads and fleet). The pool
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
	app.multiSub = appstores.NewMultiSub(app.hub, config.Logger)
	cleanups = append(cleanups, func() { app.multiSub.Stop() })

	// Main web terminal manager: one *PTYManager per workspace, dispatched by
	// SessionKey.Workspace. Per-workspace managers are created lazily on first
	// AttachSession and use workspace.Path as the shell's cwd. Workspaces are
	// registered via PTYHook (see appinfra.RegisterHooks wiring).
	app.ptyMgr = terminal.NewMultiPTYManager(config.TerminalCmd, config.MaxTerminalSessions)
	cleanups = append(cleanups, func() { _ = app.ptyMgr.Close() })
	logger.Info("multi pty manager initialized", "component", "terminal", "default_command", config.TerminalCmd)

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

	appinfra.RegisterHooks(app.registry, appinfra.HookConfig{
		MultiPool:   app.multiPool,
		PoolSize:    config.PoolSize,
		MultiSub:    app.multiSub,
		TermMgr:     app.agentTmuxMgr,
		PTYMultiMgr: app.ptyMgr,
		FleetReg:    app.fleetRegistry,
		FleetURL:    config.FleetClientURL,
		FleetWS:     config.FleetClientWorkspace,
		FleetKey:    config.FleetClientAPIKey,
		FleetActor:  config.FleetClientActor,
		FleetMode:   config.FleetMode,
		Logger:      config.Logger,
	})

	// Register the initial workspace.
	if app.pool != nil && shouldRegisterInitialWorkspace(config, app.initialWorkspaceID) {
		var initialWSPath string
		if config.WorkspaceListFn != nil {
			if wsMap, listErr := config.WorkspaceListFn(); listErr == nil {
				initialWSPath = wsMap[app.initialWorkspaceID]
			}
		}
		if initialWSPath == "" {
			if cwd, cwdErr := appinfra.GetCwd(); cwdErr == nil {
				initialWSPath = cwd
			}
		}
		if err := app.registry.Register(app.initialWorkspaceID, initialWSPath); err != nil {
			logger.Warn("failed to register initial workspace", "err", err)
		} else {
			// Daemon was confirmed running above, activate subscriber immediately.
			_ = app.registry.ActivateSubscriber(app.initialWorkspaceID)
		}
		app.getMutationsSince = appstores.GetMutationsSinceFn(app.multiSub)
	}

	// Reconcile all config workspaces. Subscribers for secondary workspaces
	// activate lazily via the workspace middleware on the first API request.
	appinfra.ReconcileConfigWorkspaces(config.WorkspaceListFn, app.initialWorkspaceID, app.pool != nil, app.registry, config.Logger)

	if config.DaemonStartupFn != nil {
		onReady := func(wsID string) { _ = app.registry.ActivateSubscriber(wsID) }
		go config.DaemonStartupFn(ctx, onReady)
	}

	// Start health doctor to auto-restart daemons with stuck circuit breakers.
	if config.WorkspaceListFn != nil {
		doctor := webui.NewHealthDoctor(app.multiPool, config.WorkspaceListFn, config.Logger, webui.DefaultHealthDoctorConfig())
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
		nameToID := resolveWorkspaceNameToID(config)
		var tmCleanup func()
		app.tabMetaStore, tmCleanup = appstores.InitTabMeta(ctx, config.FleetRedis, nameToID, config.Logger)
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

	// Workspace-existence checker. Also activates the SSE subscriber lazily
	// on the first API request (idempotent — no-op if already active).
	//
	// Workspace IDs in URLs are usually UUIDs, but external callers (CLI
	// tooling, parity tests, the fdb client) frequently use the workspace
	// *name* instead. When the direct UUID lookup misses, fall back to the
	// resolver so a name like "PARITY" still maps to the registered UUID
	// rather than 404'ing.
	//
	// Final fallback: the unified store. Fleet-db-only workspaces (created
	// post-Phase-4) aren't in the beads registry, so without this check
	// every `/api/workspaces/{ws}/...` route would 404 before the handler
	// could read state from fleet-db.
	wsResolver := config.WorkspaceIDResolverFn
	wsStore := config.Store
	app.wsExistsFn = func(id string) bool {
		if app.registry.Registered(id) {
			_ = app.registry.ActivateSubscriber(id)
			return true
		}
		if wsResolver != nil {
			if uuid, err := wsResolver(id); err == nil && uuid != "" && app.registry.Registered(uuid) {
				_ = app.registry.ActivateSubscriber(uuid)
				return true
			}
		}
		if wsStore != nil {
			if ws, err := wsStore.Workspaces().Get(ctx, id); err == nil && ws != nil {
				return true
			}
		}
		return false
	}

	// Initialize workspace service layer. Store is the preferred read
	// source (Phase 4 of fleet-db migration); the legacy ConfigFn /
	// ConfigByIDFn closures stay wired during the transition for code
	// paths that haven't yet been moved to store reads.
	app.workspaceSvc = service.NewWorkspaceService(service.WorkspaceServiceConfig{
		Store:          config.Store,
		ConfigFn:       config.WorkspaceConfigFn,
		ConfigByIDFn:   config.WorkspaceConfigByIDFn,
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
			app.termAuth, app.tabMetaStore, app.hub, rc, app.ptyMgr,
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
	app.sessSvc = svcimpl.NewSessionService(config.WorkspaceConfigByIDFn, app.sessionHistoryStore)

	app.buildHandlers()
	app.buildModules()

	// Create mux and register all routes.
	app.mux = http.NewServeMux()
	app.registerRoutes()
	app.registerWorkerAPIRoutes()

	return app, nil
}

// resolveWorkspaceNameToID creates a name-to-ID mapping from workspace config.
func resolveWorkspaceNameToID(config webui.ServerConfig) map[string]string {
	if config.WorkspaceConfigFn == nil {
		return nil
	}
	wsData, err := config.WorkspaceConfigFn()
	if err != nil || wsData == nil {
		return nil
	}
	nameToID := make(map[string]string, len(wsData.Workspaces))
	for _, ws := range wsData.Workspaces {
		if ws.Name != "" && ws.ID != "" {
			nameToID[ws.Name] = ws.ID
		}
	}
	return nameToID
}

func shouldRegisterInitialWorkspace(config webui.ServerConfig, workspaceID string) bool {
	if workspaceID == "" {
		return false
	}
	if !config.FleetMode || config.Store == nil {
		return true
	}
	if config.WorkspaceListFn == nil {
		return false
	}
	workspaces, err := config.WorkspaceListFn()
	if err != nil {
		return false
	}
	_, ok := workspaces[workspaceID]
	return ok
}

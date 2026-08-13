package app

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/tysonthomas9/loomcli/internal/app/workitemmove"
	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
	"github.com/tysonthomas9/loomcli/internal/webui"
	"github.com/tysonthomas9/loomcli/internal/webui/agentcoord"
	"github.com/tysonthomas9/loomcli/internal/webui/readprojection"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/sessioncoord"
	"github.com/tysonthomas9/loomcli/internal/webui/storeadapter"
	"github.com/tysonthomas9/loomcli/internal/webui/terminal"
	"github.com/tysonthomas9/loomcli/internal/webui/workspacecoord"
)

// NewServer initializes all server dependencies. On failure, it cleans up
// resources allocated before the error point via a cleanup stack.
func NewServer(ctx context.Context, config webui.ServerConfig) (_ *Server, retErr error) { //nolint:gocognit,cyclop,funlen // server initialization requires sequential resource setup
	// Apply defaults for zero values
	if config.Port == 0 {
		config.Port = webui.DefaultPort
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
	logger.Info("starting web UI server", "port", config.Port, "bind_address", config.BindAddress)
	logger.Info("workflow catalog Work Items adapter enabled")
	if app.corsConfig.Enabled {
		logger.Info("CORS enabled", "origins", app.corsConfig.AllowedOrigins)
	}
	if config.HSTSEnabled {
		logger.Info("HSTS enabled: ensure this server is behind a TLS-terminating proxy")
	}
	if config.ExtAuthURL == "" && !isLoopbackBindAddress(config.BindAddress) {
		logger.Warn("open auth mode is reachable on a non-loopback listener; restrict exposure at the host/container boundary or configure --auth-url", "bind_address", config.BindAddress)
	}
	if config.ExtAuthURL != "" && config.WorkspaceRoleResolver == nil {
		logger.Warn("remote file-browser requests will be DENIED (403): file browser RBAC not configured; configure a workspace role resolver to enable remote file access")
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
	addBundledLoopbackFrontendOrigins(&app.config, app.actualPort)

	app.initialWorkspaceID = config.InitialWorkspaceID

	app.workItems = workitems.Route(config.WorkItemsFn)
	if config.Store != nil {
		app.workspaceStore = config.Store.Workspaces()
		app.workspaceCatalog = config.WorkspaceCatalog
		if app.workspaceCatalog == nil {
			app.workspaceCatalog, err = NewWorkspaceCapability(app.workspaceStore, config.Store.Repos())
			if err != nil {
				return nil, fmt.Errorf("compose Workspace capability: %w", err)
			}
		}
	}
	if app.workItems != nil && app.workspaceCatalog != nil {
		app.workItemMover, err = workitemmove.New(app.workItems, app.workspaceCatalog, middleware.WithWorkspace)
		if err != nil {
			return nil, fmt.Errorf("compose work item move workflow: %w", err)
		}
	}

	// Create SSE hub for real-time push notifications
	app.hub = NewHub()
	go app.hub.Run()
	cleanups = append(cleanups, func() { app.hub.Stop() })

	// Bridge per-workspace Work Items mutations to SSE clients.
	app.multiSub = NewMultiSub(ctx, app.hub, config.Logger)
	app.getMutationsSince = GetMutationsSinceFn(app.multiSub)
	cleanups = append(cleanups, func() { app.multiSub.Stop() })

	// Main web terminal manager: one *PTYManager per workspace, dispatched by
	// SessionKey.Workspace. Per-workspace managers are created lazily on first
	// AttachSession and use workspace.Path as the shell's cwd. Workspaces are
	// registered via PTYHook (see RegisterHooks wiring).
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
		app.termAuth, authErr = NewTerminalAuth()
		if authErr != nil {
			logger.Warn("failed to initialize terminal auth; token endpoint disabled", "err", authErr)
		} else {
			cleanups = append(cleanups, func() { app.termAuth.Stop() })
		}
	}

	// Initialize SSE token exchange store (external auth mode only).
	if config.ExtAuthURL != "" {
		var sseErr error
		app.sseTokens, sseErr = NewTokenStore()
		if sseErr != nil {
			logger.Warn("failed to initialize SSE token store", "err", sseErr)
		} else {
			cleanups = append(cleanups, func() { app.sseTokens.Stop() })
		}
	}

	// Fleet store registry and JWT config for worker registration.
	if config.FleetRedis != nil {
		app.fleetRegistry, err = InitFleetRegistry(*config.FleetRedis, config.Logger)
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
				app.tokenCfg = NewFleetTokenConfig(jwtKey, time.Hour)
				logger.Info("fleet store registry initialized", "component", "fleet", "redis_address", config.FleetRedis.Address)
			}
		}
	}

	// Initialize fleet claim metrics
	if app.fleetRegistry != nil {
		app.claimMetrics = NewFleetClaimMetrics()
	}

	// Construct lifecycle hooks.
	app.registry = NewWorkspaceRegistry(config.Logger)
	cleanups = append(cleanups, func() { _ = app.registry.Close() })

	RegisterHooks(app.registry, HookConfig{
		MultiSub:    app.multiSub,
		TermMgr:     app.agentTmuxMgr,
		PTYMultiMgr: app.ptyMgr,
		FleetReg:    app.fleetRegistry,
		FleetURL:    config.FleetClientURL,
		FleetKey:    config.FleetClientAPIKey,
		FleetActor:  config.FleetClientActor,
		FleetMode:   config.FleetMode,
		Logger:      config.Logger,
	})

	workspacePathsFn := storeWorkspacePathsFn(ctx, config)

	// Reconcile all store workspaces. Subscribers for secondary workspaces
	// activate lazily via the workspace SSE token/stream routes.
	ReconcileStoreWorkspaces(workspacePathsFn, app.initialWorkspaceID, false, app.registry, config.Logger)

	// Periodic re-reconcile: workspaces created out-of-band (CLI
	// `loom workspace create` while serve is running, or another
	// loom-serve instance against shared fleet-db) need to be picked up
	// without a serve restart. Without this, terminal attach for those
	// workspaces fails with "workspace not registered" until restart.
	StartPeriodicWorkspaceReconcile(ctx, workspacePathsFn, app.registry, 15*time.Second, config.Logger)

	// Build fleet registration config.
	if config.FleetAPIKey != "" && app.fleetRegistry != nil {
		var regCleanup func()
		app.fleetRegCfg, regCleanup = NewFleetRegisterConfig(config.FleetAPIKey, config.FleetRedis, config.Logger)
		if regCleanup != nil {
			cleanups = append(cleanups, regCleanup)
		}
	} else if app.fleetRegistry != nil && config.FleetAPIKey == "" {
		logger.Warn("fleet store configured but no fleet API key set", "component", "fleet", "env_var", "LOOM_FLEET_API_KEY")
		logger.Warn("fleet registration endpoint will return 503 until fleet API key is configured", "component", "fleet")
	}

	if config.FleetRedis != nil {
		var tmCleanup func()
		app.tabMetaStore, tmCleanup = InitTabMeta(ctx, config.FleetRedis, config.Logger)
		cleanups = append(cleanups, tmCleanup)
	}

	if config.FleetRedis != nil {
		var itCleanup func()
		app.issueTabStore, itCleanup = InitIssueTabs(ctx, config.FleetRedis, app.initialWorkspaceID, config.Logger)
		cleanups = append(cleanups, itCleanup)
	}

	if config.FleetRedis != nil {
		var shCleanup func()
		app.sessionHistoryStore, shCleanup = InitSessionHistory(ctx, config.FleetRedis, app.initialWorkspaceID, config.Logger)
		cleanups = append(cleanups, shCleanup)
	}

	// Initialize external auth (JWKS cache + middleware)
	app.extAuthMiddleware, app.jwksCleanup = initExtAuth(config)
	if app.jwksCleanup != nil {
		cleanups = append(cleanups, app.jwksCleanup)
	}

	app.wrappedCreateFn = wrapWorkspaceCreateFn(config.WorkspaceCreateFn, app.registry)
	app.wrappedDeleteCleanupFn = wrapWorkspaceDeleteCleanupFn(config.WorkspaceDeleteCleanupFn, app.registry)

	// Async job store for clone workspace creation (202 + polling).
	app.jobStore = workspacecoord.NewWorkspaceJobRegistry()
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
	// Initialize workspace service layer. FleetDB Store is the authoritative
	// workspace source in both local and distributed modes.
	var workspaceAgents workspacecoord.WorkspaceAgentDirectory
	if config.AgentsCapability != nil {
		workspaceAgents = config.AgentsCapability.AgentsAPI()
	}
	app.workspaceSvc = workspacecoord.NewWorkspaceService(workspacecoord.WorkspaceServiceConfig{
		Topology:             config.Store,
		Workspace:            app.workspaceCatalog,
		CreateFn:             app.wrappedCreateFn,
		AddReposFn:           config.WorkspaceAddReposFn,
		DeleteCleanupFn:      app.wrappedDeleteCleanupFn,
		JobStore:             app.jobStore,
		AdmissionCoordinator: config.WorkspaceAdmissions,
		AgentDirectory:       workspaceAgents,
	})

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

	// Initialize agent service after terminal metadata so interactive lifecycle
	// authority can bind process-local PTYs to server-owned agent tabs.
	app.ptyMgr.SetBeforeKill(NewInteractionPTYBeforeKill(
		app.tabMetaStore,
		InteractionForceInterrupter(config.InteractionCapability),
	))
	interactiveController := agentcoord.NewInteractiveRuntimeController(
		interactiveRuntimeTabSource{terminalService: app.termSvc},
		app.ptyMgr,
	)
	app.agentRuntime = agentcoord.NewCanonicalInteractiveAgentRuntime(interactiveController)
	// Agent delivery now owns terminal/log access only. Git and checkout
	// behavior is composed independently through Source Control Checkout.
	app.agentSvc = agentcoord.NewAgentService(app.agentTmuxMgr, app.termAuth)

	app.sourceBrowse = config.SourceControlBrowse
	if app.sourceBrowse != nil {
		app.issueDiff, err = readprojection.NewIssueDiffProjection(
			func(ctx context.Context, workspaceKey string) readprojection.IssueDiffWorkItemQuery {
				if config.WorkItemsFn == nil {
					return nil
				}
				return config.WorkItemsFn(middleware.WithWorkspace(ctx, workspaceKey))
			},
			app.sourceBrowse,
		)
		if err != nil {
			return nil, err
		}
	}

	app.sourceMutate = config.SourceControlMutate
	app.sourceCheckout = config.SourceControlCheckout

	// Initialize session delivery over lifecycle-owner queries and the
	// immutable Run Capture projection. WebUI never queries Artifacts directly.
	app.sessSvc = sessioncoord.NewSessionService(
		config.Store,
		app.sessionHistoryStore,
		webui.RunCaptureProjection(config.RunCaptureCapability),
	)

	app.buildHandlers()
	app.buildModules()

	// Create mux and register all routes.
	app.mux = http.NewServeMux()
	app.registerRoutes()
	app.registerWorkerAPIRoutes()

	return app, nil
}

// interactiveRuntimeTabSource translates the terminal service's richer tab
// metadata into the narrow ownership view consumed by agentcoord. Use the
// terminal service rather than the persistence store so PTYAlive reflects the
// current server process.
type interactiveRuntimeTabSource struct {
	terminalService terminal.TerminalService
}

func (s interactiveRuntimeTabSource) ListInteractiveRuntimeTabs(
	ctx context.Context,
	workspace string,
) ([]agentcoord.InteractiveRuntimeTab, error) {
	tabs, err := s.terminalService.ListTabs(ctx, workspace)
	if err != nil {
		return nil, err
	}
	runtimeTabs := make([]agentcoord.InteractiveRuntimeTab, 0, len(tabs))
	for i := range tabs {
		tab := &tabs[i]
		runtimeTabs = append(runtimeTabs, agentcoord.InteractiveRuntimeTab{
			SessionName:           tab.SessionName,
			Kind:                  tab.Kind,
			AgentID:               tab.AgentID,
			InteractionSessionID:  tab.InteractionSessionID,
			InteractionTerminalID: tab.InteractionTerminalID,
			PTYAlive:              tab.PTYAlive,
		})
	}
	return runtimeTabs, nil
}

func addBundledLoopbackFrontendOrigins(config *webui.ServerConfig, actualPort int) {
	if config == nil || config.ExtAuthURL != "" || config.FrontendDir == "" || len(config.FrontendOrigins) > 0 || actualPort <= 0 {
		return
	}
	if !isLoopbackBindAddress(config.BindAddress) {
		return
	}
	config.FrontendOrigins = append(config.FrontendOrigins,
		fmt.Sprintf("http://localhost:%d", actualPort),
		fmt.Sprintf("http://127.0.0.1:%d", actualPort),
	)
}

func isLoopbackBindAddress(bindAddress string) bool {
	if bindAddress == "localhost" {
		return true
	}
	ip := net.ParseIP(bindAddress)
	return ip != nil && ip.IsLoopback()
}

func (app *Server) activateSSESubscriber(_ context.Context, wsID string) {
	if app == nil || wsID == "" || app.multiSub == nil {
		return
	}
	if app.registry != nil && app.registry.Registered(wsID) {
		if err := app.registry.ActivateSubscriber(wsID); err != nil {
			logger.Warn("failed to activate registered workspace SSE subscriber",
				"workspace", wsID, "err", err)
		}
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

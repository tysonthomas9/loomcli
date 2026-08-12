package app

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
	"github.com/tysonthomas9/loomcli/internal/webui/coordinator"
	"github.com/tysonthomas9/loomcli/internal/webui/fleet"
	"github.com/tysonthomas9/loomcli/internal/webui/hooks"
	"github.com/tysonthomas9/loomcli/internal/webui/subscription"
	"github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

// Type aliases so the app package can reference concrete types without
// importing the underlying packages directly.

// WorkspaceRegistry is a type alias for coordinator.WorkspaceRegistry.
type WorkspaceRegistry = coordinator.WorkspaceRegistry

// FleetStoreRegistry is a type alias for fleet.StoreRegistry.
type FleetStoreRegistry = fleet.StoreRegistry

// FleetTokenConfig is a type alias for fleet.TokenConfig.
type FleetTokenConfig = fleet.TokenConfig

// FleetClaimMetrics is a type alias for fleet.ClaimMetrics.
type FleetClaimMetrics = fleet.ClaimMetrics

// FleetRegisterConfig is a type alias for fleet.RegisterConfig.
type FleetRegisterConfig = fleet.RegisterConfig

// FleetStore is a type alias for fleet.Store.
type FleetStore = fleet.Store

// FleetRedisConfig is a type alias for fleet.RedisConfig.
type FleetRedisConfig = fleet.RedisConfig

// NewWorkspaceRegistry creates a new workspace registry.
func NewWorkspaceRegistry(logger *slog.Logger) *WorkspaceRegistry {
	return coordinator.NewWorkspaceRegistry(logger)
}

// HookConfig holds the dependencies for registering lifecycle hooks.
type HookConfig struct {
	MultiSub    *subscription.MultiWorkspaceSubscriber
	TermMgr     *terminal.AgentTmuxManager
	PTYMultiMgr *terminal.MultiPTYManager
	FleetReg    *fleet.StoreRegistry
	FleetURL    string
	FleetKey    string
	FleetActor  string // X-Actor header value (fleet-db --auth-dev-mode)
	FleetMode   bool
	Logger      *slog.Logger
}

// RegisteredHooks returns references to hooks that require post-registration
// operations. FleetSubscriber may be nil when SSE infrastructure is absent.
type RegisteredHooks struct {
	FleetSubscriber *hooks.FleetSubscriberHook
}

// RegisterHooks attaches lifecycle hooks to a workspace registry and returns
// the hook references needed for pre-built pool injection and deferred
// subscriber activation.
func RegisterHooks(registry *WorkspaceRegistry, cfg HookConfig) RegisteredHooks {
	var registered RegisteredHooks
	if cfg.TermMgr != nil {
		_ = registry.AddHook(hooks.NewTerminalHook(cfg.TermMgr, cfg.Logger))
	}
	if cfg.PTYMultiMgr != nil {
		_ = registry.AddHook(hooks.NewPTYHook(cfg.PTYMultiMgr, cfg.Logger))
	}
	if cfg.FleetReg != nil {
		_ = registry.AddHook(hooks.NewFleetStoreHook(cfg.FleetReg, cfg.Logger))
	}
	if cfg.FleetURL != "" {
		// FleetBackendHook MUST be added before FleetSubscriberHook so that
		// by the time FleetSubscriberHook.Activate fires, the FleetBackend
		// resource is already in the workspace handle.
		_ = registry.AddHook(hooks.NewFleetBackendHook(cfg.FleetURL, cfg.FleetKey, cfg.FleetActor, cfg.Logger))
	}

	// FleetDB-backed SSE push: FleetSubscriberHook bridges the per-workspace
	// FleetBackend (provided by FleetBackendHook above) into the shared
	// MultiWorkspaceSubscriber so the SSE hub gets push events. This is needed
	// for both local fleet-db mode and remote fleet mode; FleetMode only means
	// external fleet orchestration owns agent scheduling.
	if cfg.MultiSub != nil && cfg.FleetURL != "" {
		registered.FleetSubscriber = hooks.NewFleetSubscriberHook(cfg.MultiSub, registry, cfg.Logger)
		_ = registry.AddHook(registered.FleetSubscriber)
	}

	return registered
}

// ReconcileStoreWorkspaces registers all FleetDB workspaces via the
// WorkspaceRegistry at startup.
func ReconcileStoreWorkspaces(
	listFn func() (map[string]string, error),
	initialID string,
	initialRegistered bool,
	registry *WorkspaceRegistry,
	loggerArg *slog.Logger,
) {
	logger := loggerArg
	if logger == nil {
		logger = slog.Default()
	}
	if listFn == nil {
		return
	}
	workspaces, err := listFn()
	if err != nil {
		logger.Warn("failed to load workspace list for startup reconciliation", "err", err)
		return
	}
	for wsID, wsPath := range workspaces {
		if initialRegistered && wsID == initialID {
			continue
		}
		if strings.TrimSpace(wsPath) == "" {
			logger.Warn("workspace missing local path during startup reconciliation; terminal disabled",
				"workspace", wsID)
			continue
		}
		if err := registry.Register(wsID, wsPath); err != nil {
			logger.Warn("failed to register workspace during startup reconciliation",
				"workspace", wsID, "err", err)
		}
	}
	logger.Info("startup reconciliation complete",
		"total_workspaces", len(workspaces),
		"registered", len(registry.WorkspaceIDs()))
}

// StartPeriodicWorkspaceReconcile launches a goroutine that periodically
// picks up workspaces created out-of-band (e.g. via the CLI `loom workspace
// create` while serve is running, or by another loom-serve instance against
// shared fleet-db) and registers them with the local WorkspaceRegistry.
//
// Without this, workspaces created after serve startup were invisible to the
// PTY manager (the terminal subsystem rejected attach with "workspace not
// registered: …" until a restart).
//
// The loop exits when ctx is cancelled. Failures are logged but never fatal —
// transient store errors must not take down serve.
func StartPeriodicWorkspaceReconcile(
	ctx context.Context,
	listFn func() (map[string]string, error),
	registry *WorkspaceRegistry,
	interval time.Duration,
	loggerArg *slog.Logger,
) {
	logger := loggerArg
	if logger == nil {
		logger = slog.Default()
	}
	if listFn == nil || registry == nil {
		return
	}
	if interval <= 0 {
		interval = 30 * time.Second
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				reconcileNewWorkspaces(listFn, registry, logger)
			}
		}
	}()
}

func reconcileNewWorkspaces(
	listFn func() (map[string]string, error),
	registry *WorkspaceRegistry,
	logger *slog.Logger,
) {
	workspaces, err := listFn()
	if err != nil {
		logger.Debug("periodic workspace reconcile: list failed (transient ok)", "err", err)
		return
	}
	known := make(map[string]struct{})
	for _, id := range registry.WorkspaceIDs() {
		known[id] = struct{}{}
	}
	for wsID, wsPath := range workspaces {
		if _, ok := known[wsID]; ok {
			continue
		}
		if strings.TrimSpace(wsPath) == "" {
			logger.Debug("periodic workspace reconcile: local path missing; terminal disabled",
				"workspace", wsID)
			continue
		}
		if err := registry.Register(wsID, wsPath); err != nil {
			logger.Warn("periodic workspace reconcile: register failed",
				"workspace", wsID, "err", err)
			continue
		}
		logger.Info("periodic workspace reconcile: registered new workspace",
			"workspace", wsID, "path", wsPath)
	}
}

// InitFleetRegistry creates the fleet store registry from Redis config.
func InitFleetRegistry(redisCfg fleet.RedisConfig, logger *slog.Logger) (*FleetStoreRegistry, error) {
	reg, err := fleet.NewStoreRegistry(redisCfg, fleet.DefaultTimeoutConfig(), nil)
	if err != nil {
		return nil, err
	}
	return reg, nil
}

// NewFleetClaimMetrics creates a new fleet claim metrics instance.
func NewFleetClaimMetrics() *FleetClaimMetrics {
	return fleet.NewClaimMetrics()
}

// NewFleetTokenConfig creates a fleet token config with the given signing key.
func NewFleetTokenConfig(signingKey []byte, expiry time.Duration) *FleetTokenConfig {
	return &fleet.TokenConfig{
		SigningKey: signingKey,
		Expiry:     expiry,
	}
}

// NewFleetRegisterConfig creates a fleet register config.
func NewFleetRegisterConfig(apiKey string, redisCfg *fleet.RedisConfig, logger *slog.Logger) (*FleetRegisterConfig, func()) {
	regCfg := &fleet.RegisterConfig{
		APIKey: apiKey,
	}
	var cleanup func()
	if redisCfg != nil {
		rlClient := fleet.NewRedisClient(redisCfg.Address, redisCfg.Password, 0)
		regCfg.RateLimiter = fleet.NewRateLimiter(rlClient, 10, time.Minute)
		cleanup = func() { _ = regCfg.RateLimiter.Close() }
		logger.Info("fleet API key authentication enabled", "component", "fleet")
	}
	return regCfg, cleanup
}

// NewRedisClient creates a Redis client from fleet config.
func NewRedisClient(address, password string) interface{} {
	return fleet.NewRedisClient(address, password, 0)
}

// GetCwd returns the current working directory (re-export from webui).

// PTYHook re-exports hooks.PTYHook for type references.
type PTYHook = hooks.PTYHook

// FleetModule is a type alias for fleet.Module.
type FleetModule = fleet.Module

// NewFleetModule creates a new fleet workspace-scoped module.
func NewFleetModule(registry *FleetStoreRegistry, tokenCfg *FleetTokenConfig, workItemsFn workitems.Provider, claimMetrics *FleetClaimMetrics, regCfg *FleetRegisterConfig) *FleetModule {
	return fleet.NewModule(registry.Get, tokenCfg, workItemsFn, claimMetrics, regCfg)
}

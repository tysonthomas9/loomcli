// Package appinfra consolidates infrastructure initialization for the webui/app
// composition root. The app package imports this single package instead of
// circuitbreaker, coordinator, daemon, editor, fleet, hooks, and workspace.
package appinfra

import (
	"log/slog"
	"time"

	"github.com/tysonthomas9/loomcli/internal/circuitbreaker"
	"github.com/tysonthomas9/loomcli/internal/webui"
	"github.com/tysonthomas9/loomcli/internal/webui/coordinator"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
	"github.com/tysonthomas9/loomcli/internal/webui/fleet"
	"github.com/tysonthomas9/loomcli/internal/webui/hooks"
	"github.com/tysonthomas9/loomcli/internal/webui/subscription"
	"github.com/tysonthomas9/loomcli/internal/webui/terminal"
	"github.com/tysonthomas9/loomcli/internal/workspace"
)

// Type aliases so the app package can reference concrete types without
// importing the underlying packages directly.

// WorkspaceRegistry is a type alias for coordinator.WorkspaceRegistry.
type WorkspaceRegistry = coordinator.WorkspaceRegistry

// FleetStoreRegistry is a type alias for fleet.StoreRegistry.
type FleetStoreRegistry = fleet.StoreRegistry

// Pool is a type alias for daemon.Pool.
type Pool = daemon.Pool

// MultiPool is a type alias for daemon.MultiPool.
type MultiPool = daemon.MultiPool

// ConnectionPool is a type alias for daemon.ConnectionPool.
type ConnectionPool = daemon.ConnectionPool

// NewMultiPool creates a new MultiPool.
var NewMultiPool = daemon.NewMultiPool

// NewConnectionPool creates a new ConnectionPool.
var NewConnectionPool = daemon.NewConnectionPool

// NewConnectionPoolAutoDiscover creates a connection pool with auto-discovery.
var NewConnectionPoolAutoDiscover = daemon.NewConnectionPoolAutoDiscover

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

// ShortWorkspaceID returns a short version of a workspace ID.
func ShortWorkspaceID(id string) string {
	return workspace.ShortWorkspaceID(id)
}

// NewWorkspaceRegistry creates a new workspace registry.
func NewWorkspaceRegistry(logger *slog.Logger) *WorkspaceRegistry {
	return coordinator.NewWorkspaceRegistry(logger)
}

// InitProtectedPool creates a daemon connection pool with a circuit breaker.
// Returns the pool and a nil error on success.
func InitProtectedPool(rawPool *daemon.ConnectionPool, logger *slog.Logger) daemon.Pool {
	if logger == nil {
		logger = slog.Default()
	}
	breaker := circuitbreaker.NewBreaker("daemon", circuitbreaker.Config{
		FailureThreshold:  3,
		OpenTimeout:       30 * time.Second,
		HalfOpenMaxProbes: 3,
		ShouldTrip:        daemon.DaemonShouldTrip,
		OnStateChange: func(from, to circuitbreaker.State) {
			logger.Info("circuit breaker state change", "component", "circuit_breaker", "from", from, "to", to)
		},
	})
	return daemon.NewProtectedPool(rawPool, breaker)
}

// HookConfig holds the dependencies for registering lifecycle hooks.
type HookConfig struct {
	MultiPool   *daemon.MultiPool
	PoolSize    int
	MultiSub    *subscription.MultiWorkspaceSubscriber
	TermMgr     *terminal.AgentTmuxManager
	PTYMultiMgr *terminal.MultiPTYManager
	FleetReg    *fleet.StoreRegistry
	FleetURL    string
	FleetWS     string
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
		_ = registry.AddHook(hooks.NewFleetBackendHook(cfg.FleetURL, cfg.FleetWS, cfg.FleetKey, cfg.FleetActor, cfg.Logger))
	}

	// Fleet-mode SSE push: FleetSubscriberHook bridges the per-workspace
	// FleetBackend (provided by FleetBackendHook above) into the shared
	// MultiWorkspaceSubscriber so the SSE hub gets push events without a
	// local issue daemon. Skipped when MultiSub is nil (no SSE infrastructure).
	if cfg.FleetMode && cfg.MultiSub != nil && cfg.FleetURL != "" {
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
	first := true
	for wsID, wsPath := range workspaces {
		if initialRegistered && wsID == initialID {
			continue
		}
		// Stagger pool creation to avoid thundering-herd on daemon sockets.
		if !first {
			time.Sleep(200 * time.Millisecond)
		}
		first = false
		if err := registry.Register(wsID, wsPath); err != nil {
			logger.Warn("failed to register workspace during startup reconciliation",
				"workspace", wsID, "err", err)
		}
	}
	logger.Info("startup reconciliation complete",
		"total_workspaces", len(workspaces),
		"registered", len(registry.WorkspaceIDs()))
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
func GetCwd() (string, error) {
	return webui.GetCwd()
}

// PTYHook re-exports hooks.PTYHook for type references.
type PTYHook = hooks.PTYHook

// FleetModule is a type alias for fleet.Module.
type FleetModule = fleet.Module

// NewFleetModule creates a new fleet workspace-scoped module.
func NewFleetModule(registry *FleetStoreRegistry, tokenCfg *FleetTokenConfig, multiPool daemon.Pool, claimMetrics *FleetClaimMetrics, regCfg *FleetRegisterConfig) *FleetModule {
	return fleet.NewModule(registry.Get, tokenCfg, multiPool, claimMetrics, regCfg)
}

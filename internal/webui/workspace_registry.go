package webui

import (
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/circuitbreaker"
	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
	"github.com/tysonthomas9/loomcli/internal/webui/fleet"
)

// Sentinel errors for WorkspaceRegistry operations.
var (
	ErrEmptyWorkspaceID   = errors.New("workspace ID must not be empty")
	ErrEmptyWorkspacePath = errors.New("workspace path must not be empty")
	ErrRegistryClosed     = errors.New("workspace registry is closed")
)

// WorkspaceIDResolverFn resolves a workspace name to its stable UUID.
// Returns ("", error) if the workspace is not found or config cannot be loaded.
type WorkspaceIDResolverFn func(name string) (string, error)

// WorkspaceRegistry is the central coordination point for workspace lifecycle
// events (create, delete, startup registration). It encapsulates MultiPool and
// MultiWorkspaceSubscriber so that Register/Deregister operations update both
// subsystems atomically under a single mutex.
//
// The id parameter is always the workspace UUID. Callers resolve UUID before
// calling. The registry never switches keys.
type WorkspaceRegistry struct {
	mu            sync.RWMutex
	multiPool     *daemon.MultiPool
	multiSub      *MultiWorkspaceSubscriber
	fleetRegistry *fleet.StoreRegistry // nil when fleet is disabled
	poolSize      int
	logger        *slog.Logger
	closed        bool
	poolFactory   func(socketPath string, poolSize int) (*daemon.ConnectionPool, error)
}

// NewWorkspaceRegistry creates a new WorkspaceRegistry.
func NewWorkspaceRegistry(
	multiPool *daemon.MultiPool,
	multiSub *MultiWorkspaceSubscriber,
	poolSize int,
	logger *slog.Logger,
) *WorkspaceRegistry {
	if logger == nil {
		logger = slog.Default()
	}
	return &WorkspaceRegistry{
		multiPool:   multiPool,
		multiSub:    multiSub,
		poolSize:    poolSize,
		logger:      logger,
		poolFactory: daemon.NewConnectionPool,
	}
}

// Register creates a connection pool with circuit breaker for the workspace at
// the given filesystem path and registers it in both MultiPool and the
// subscriber. The id must be the workspace UUID; path is used to derive the
// daemon socket path.
//
// Returns error only for hard failures (closed registry, empty id/path).
// Pool registration failures are logged but non-fatal (workspace was created,
// pool registration is best-effort).
func (r *WorkspaceRegistry) Register(id, path string) error {
	if id == "" {
		return ErrEmptyWorkspaceID
	}
	if path == "" {
		return ErrEmptyWorkspacePath
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed {
		return ErrRegistryClosed
	}

	socketPath := rpc.ShortSocketPath(path)
	rawPool, err := r.poolFactory(socketPath, r.poolSize)
	if err != nil {
		r.logger.Warn("failed to create connection pool for workspace",
			"workspace", id, "socket", socketPath, "err", err)
	} else {
		breaker := circuitbreaker.NewBreaker("ws-"+shortBreakerName(id), circuitbreaker.Config{
			FailureThreshold:  5,
			OpenTimeout:       30 * time.Second,
			HalfOpenMaxProbes: 1,
			ShouldTrip:        daemon.DaemonShouldTrip,
			OnStateChange: func(from, to circuitbreaker.State) {
				r.logger.Info("circuit breaker state change",
					"component", "circuit_breaker", "workspace", id, "from", from, "to", to)
			},
		})
		pool := daemon.NewProtectedPool(rawPool, breaker)

		if err := r.multiPool.Register(id, pool); err != nil {
			r.logger.Warn("failed to register pool for workspace",
				"workspace", id, "err", err)
		} else {
			r.logger.Info("registered connection pool for workspace",
				"workspace", id, "socket", socketPath)
		}
	}

	// Always attempt subscriber registration, even if pool creation failed.
	if err := r.multiSub.AddWorkspace(id); err != nil {
		r.logger.Warn("failed to start subscriber for workspace",
			"workspace", id, "err", err)
	} else {
		r.logger.Info("started subscriber for workspace", "workspace", id)
	}

	// Register in fleet store registry (non-fatal on error).
	if r.fleetRegistry != nil {
		if err := r.fleetRegistry.Register(id); err != nil {
			r.logger.Warn("failed to register workspace in fleet registry",
				"workspace", id, "err", err)
		}
	}

	return nil
}

// RegisterPool registers a pre-built pool for the workspace. This is used for
// the initial workspace at startup where the pool is already constructed with
// custom config (auto-discover, specific OnStateChange callbacks, etc.).
func (r *WorkspaceRegistry) RegisterPool(id string, pool daemon.Pool) error {
	if id == "" {
		return ErrEmptyWorkspaceID
	}
	if pool == nil {
		return errors.New("pool must not be nil")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed {
		return ErrRegistryClosed
	}

	if err := r.multiPool.Register(id, pool); err != nil {
		r.logger.Warn("failed to register pre-built pool for workspace",
			"workspace", id, "err", err)
		return nil
	}

	if err := r.multiSub.AddWorkspace(id); err != nil {
		r.logger.Warn("failed to start subscriber for workspace",
			"workspace", id, "err", err)
	} else {
		r.logger.Info("registered pre-built pool and subscriber for workspace",
			"workspace", id)
	}

	// Register in fleet store registry (non-fatal on error).
	if r.fleetRegistry != nil {
		if err := r.fleetRegistry.Register(id); err != nil {
			r.logger.Warn("failed to register workspace in fleet registry",
				"workspace", id, "err", err)
		}
	}

	return nil
}

// Deregister removes the pool, subscriber, and fleet store for the workspace.
// No-op if the workspace is not registered. All three subsystems are
// deregistered atomically under the registry mutex to prevent concurrent
// requests from seeing partially-torn state.
func (r *WorkspaceRegistry) Deregister(id string) {
	if id == "" {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.logger.Info("deregistering workspace", "workspace", id)

	// Stop subscriber first (stop polling before closing pool).
	r.multiSub.RemoveWorkspace(id)

	// Deregister fleet store (stop timeout enforcer).
	if r.fleetRegistry != nil {
		r.fleetRegistry.Deregister(id)
	}

	// Close pool (closes connections, removes from dispatch).
	r.multiPool.Deregister(id)
}

// WorkspaceIDs returns the IDs of all registered workspaces.
func (r *WorkspaceRegistry) WorkspaceIDs() []string {
	return r.multiPool.WorkspaceIDs()
}

// SetFleetRegistry sets the optional fleet store registry. This is called after
// the fleet registry is initialized (which happens after the workspace registry
// is created, due to init ordering in server_app.go). Workspaces registered
// before this call are NOT retroactively registered in fleet — the caller must
// handle pre-existing workspaces explicitly.
func (r *WorkspaceRegistry) SetFleetRegistry(fr *fleet.StoreRegistry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fleetRegistry = fr
}

// FleetStore returns the fleet Store for a workspace. Returns (nil, false) if
// fleet is disabled or the workspace is not registered in fleet.
func (r *WorkspaceRegistry) FleetStore(wsID string) (*fleet.Store, bool) {
	r.mu.RLock()
	fr := r.fleetRegistry
	r.mu.RUnlock()
	if fr == nil {
		return nil, false
	}
	return fr.Get(wsID)
}

// FleetTimeoutCount returns the total timeout count across all fleet enforcers.
// Returns 0 if fleet is disabled.
func (r *WorkspaceRegistry) FleetTimeoutCount() int64 {
	r.mu.RLock()
	fr := r.fleetRegistry
	r.mu.RUnlock()
	if fr == nil {
		return 0
	}
	return fr.GetTotalTimeoutCount()
}

// Close prevents new registrations and closes the fleet registry (if set).
// Does NOT close MultiPool or MultiWorkspaceSubscriber (they have their own
// lifecycle managed by server.go shutdown).
func (r *WorkspaceRegistry) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	if r.fleetRegistry != nil {
		return r.fleetRegistry.Close()
	}
	return nil
}

// shortBreakerName returns first 8 chars of an ID for readable breaker names.
func shortBreakerName(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

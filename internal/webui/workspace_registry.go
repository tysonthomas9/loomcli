package webui

import (
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/circuitbreaker"
	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
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
	mu        sync.RWMutex
	multiPool *daemon.MultiPool
	multiSub  *MultiWorkspaceSubscriber
	poolSize  int
	logger    *slog.Logger
	closed    bool
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
		multiPool: multiPool,
		multiSub:  multiSub,
		poolSize:  poolSize,
		logger:    logger,
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

	r.mu.RLock()
	closed := r.closed
	r.mu.RUnlock()
	if closed {
		return ErrRegistryClosed
	}

	socketPath := rpc.ShortSocketPath(path)
	rawPool, err := daemon.NewConnectionPool(socketPath, r.poolSize)
	if err != nil {
		r.logger.Warn("failed to create connection pool for workspace",
			"workspace", id, "socket", socketPath, "err", err)
		// Non-fatal: continue to attempt subscriber registration
		return nil
	}

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
		return nil
	}
	r.logger.Info("registered connection pool for workspace",
		"workspace", id, "socket", socketPath)

	if err := r.multiSub.AddWorkspace(id); err != nil {
		r.logger.Warn("failed to start subscriber for workspace",
			"workspace", id, "err", err)
	} else {
		r.logger.Info("started subscriber for workspace", "workspace", id)
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

	r.mu.RLock()
	closed := r.closed
	r.mu.RUnlock()
	if closed {
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

	return nil
}

// Deregister removes the pool and subscriber for the workspace. No-op if the
// workspace is not registered. Subscriber is stopped before pool is closed
// (stop polling before closing connections).
func (r *WorkspaceRegistry) Deregister(id string) {
	if id == "" {
		return
	}

	r.logger.Info("deregistering workspace", "workspace", id)

	// Stop subscriber first (stop polling before closing pool).
	r.multiSub.RemoveWorkspace(id)

	// Close pool (closes connections, removes from dispatch).
	r.multiPool.Deregister(id)
}

// WorkspaceIDs returns the IDs of all registered workspaces.
func (r *WorkspaceRegistry) WorkspaceIDs() []string {
	return r.multiPool.WorkspaceIDs()
}

// Close prevents new registrations. Does NOT close MultiPool or
// MultiWorkspaceSubscriber (they have their own lifecycle managed by
// server.go shutdown).
func (r *WorkspaceRegistry) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	return nil
}

// shortBreakerName returns first 8 chars of an ID for readable breaker names.
func shortBreakerName(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

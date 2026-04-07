package hooks

import (
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/circuitbreaker"
	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/webui/coordinator"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
)

// BeadsPoolHook implements coordinator.LifecycleHook for daemon connection pool
// lifecycle. On workspace registration, it creates a ConnectionPool, wraps it
// with a circuit breaker, registers the resulting ProtectedPool in MultiPool,
// and provides the pool to downstream hooks via the resource bag.
type BeadsPoolHook struct {
	multiPool   *daemon.MultiPool
	poolSize    int
	poolFactory func(socketPath string, poolSize int) (*daemon.ConnectionPool, error)
	logger      *slog.Logger

	// Pre-built pools for workspaces whose pools are created externally
	// (e.g., the initial workspace at startup with auto-discover).
	mu       sync.Mutex
	prebuilt map[string]daemon.Pool
}

// NewBeadsPoolHook creates a BeadsPoolHook. multiPool must not be nil (panics).
// A nil logger defaults to slog.Default().
func NewBeadsPoolHook(multiPool *daemon.MultiPool, poolSize int, logger *slog.Logger) *BeadsPoolHook {
	if multiPool == nil {
		panic("NewBeadsPoolHook: multiPool must not be nil")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &BeadsPoolHook{
		multiPool:   multiPool,
		poolSize:    poolSize,
		poolFactory: daemon.NewConnectionPool,
		logger:      logger,
		prebuilt:    make(map[string]daemon.Pool),
	}
}

// Name returns "beads-pool".
func (h *BeadsPoolHook) Name() string { return "beads-pool" }

// Critical returns true — without a connection pool, nothing works.
func (h *BeadsPoolHook) Critical() bool { return true }

// OnRegister creates a connection pool (or uses a pre-built one), wraps it with
// a circuit breaker, registers it in MultiPool, and provides it to the resource
// bag for downstream hooks.
func (h *BeadsPoolHook) OnRegister(ctx *coordinator.RegistrationContext) error {
	id := ctx.WorkspaceID
	path := ctx.WorkspacePath

	pool, prebuilt := h.consumePrebuilt(id)
	if !prebuilt {
		socketPath := rpc.ShortSocketPath(path)
		rawPool, err := h.poolFactory(socketPath, h.poolSize)
		if err != nil {
			return fmt.Errorf("create connection pool for %q (socket %s): %w", id, socketPath, err)
		}

		breaker := circuitbreaker.NewBreaker("ws-"+shortBreakerName(id), circuitbreaker.Config{
			FailureThreshold:  3,
			OpenTimeout:       30 * time.Second,
			HalfOpenMaxProbes: 3,
			ShouldTrip:        daemon.DaemonShouldTrip,
			OnStateChange: func(from, to circuitbreaker.State) {
				h.logger.Info("circuit breaker state change",
					"component", "circuit_breaker", "workspace", id, "from", from, "to", to)
			},
		})
		pool = daemon.NewProtectedPool(rawPool, breaker)
	}

	if err := h.multiPool.Register(id, pool); err != nil {
		// Close only if we created the pool ourselves; pre-built pools are caller-owned.
		if !prebuilt {
			_ = pool.Close()
		}
		return fmt.Errorf("register pool for %q in MultiPool: %w", id, err)
	}

	ctx.Provide(coordinator.ResourceKeyPool, pool)
	h.logger.Info("registered connection pool for workspace", "workspace", id, "prebuilt", prebuilt)
	return nil
}

// OnDeregister removes and closes the workspace pool from MultiPool.
func (h *BeadsPoolHook) OnDeregister(ctx coordinator.DeregistrationContext) {
	h.multiPool.Deregister(ctx.WorkspaceID)
	h.logger.Debug("deregistered connection pool for workspace", "workspace", ctx.WorkspaceID)
}

// OnRollback undoes OnRegister — same as OnDeregister.
func (h *BeadsPoolHook) OnRollback(ctx coordinator.DeregistrationContext) {
	h.OnDeregister(ctx)
}

// SetPrebuiltPool stores a pre-built pool for a workspace ID. When OnRegister
// is called for this workspace, the pre-built pool is used instead of creating
// a new one. The pre-built pool is consumed (removed from the map) on use.
// Must be called before the workspace is registered.
func (h *BeadsPoolHook) SetPrebuiltPool(id string, pool daemon.Pool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.prebuilt[id] = pool
}

// consumePrebuilt returns and removes the pre-built pool for id, if any.
func (h *BeadsPoolHook) consumePrebuilt(id string) (daemon.Pool, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	pool, ok := h.prebuilt[id]
	if ok {
		delete(h.prebuilt, id)
	}
	return pool, ok
}

// shortBreakerName returns first 8 chars of an ID for readable breaker names.
func shortBreakerName(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

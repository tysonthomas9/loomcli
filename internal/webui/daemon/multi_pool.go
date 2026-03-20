package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/tysonthomas9/loomcli/internal/rpc"
)

// ErrNoWorkspaceInContext is returned when MultiPool.Get is called without a
// workspace ID in the request context.
var ErrNoWorkspaceInContext = errors.New("no workspace ID in request context")

// ErrWorkspaceNotRegistered is returned when the requested workspace has no
// connection pool registered.
var ErrWorkspaceNotRegistered = errors.New("workspace not registered")

// WorkspaceContextKeyFunc is the function used by MultiPool to extract a
// workspace ID from a context. It is injected at construction to avoid a
// circular dependency between the daemon package and the webui package where
// the context key type is defined.
type WorkspaceContextKeyFunc func(ctx context.Context) string

// MultiPool implements the Pool interface and dispatches Get/Put/Discard calls
// to a per-workspace ConnectionPool based on the workspace ID in the context.
type MultiPool struct {
	mu          sync.RWMutex
	pools       map[string]Pool
	clientOwner map[*rpc.Client]Pool // tracks which pool issued each client
	extractWS   WorkspaceContextKeyFunc
	poolSize    int
	closed      bool
}

// NewMultiPool creates a new MultiPool. The extractWS function is used to read
// the workspace ID from the context on every Get call. poolSize is the default
// pool size for newly registered workspaces.
func NewMultiPool(extractWS WorkspaceContextKeyFunc, poolSize int) *MultiPool {
	if poolSize <= 0 {
		poolSize = DefaultPoolSize
	}
	return &MultiPool{
		pools:       make(map[string]Pool),
		clientOwner: make(map[*rpc.Client]Pool),
		extractWS:   extractWS,
		poolSize:    poolSize,
	}
}

// Register creates and stores a ConnectionPool for the given workspace.
// If a pool already exists for wsID it is closed first.
func (mp *MultiPool) Register(wsID string, pool Pool) error {
	mp.mu.Lock()
	defer mp.mu.Unlock()

	if mp.closed {
		return ErrPoolClosed
	}

	// Close existing pool for this workspace if any.
	if existing, ok := mp.pools[wsID]; ok {
		slog.Info("replacing existing pool for workspace", "workspace", wsID)
		mp.pruneClientOwner(existing)
		_ = existing.Close()
	}

	mp.pools[wsID] = pool
	slog.Info("registered workspace pool", "workspace", wsID)
	return nil
}

// Deregister closes and removes the pool for the given workspace.
func (mp *MultiPool) Deregister(wsID string) {
	mp.mu.Lock()
	defer mp.mu.Unlock()

	if p, ok := mp.pools[wsID]; ok {
		mp.pruneClientOwner(p)
		_ = p.Close()
		delete(mp.pools, wsID)
		slog.Info("deregistered workspace pool", "workspace", wsID)
	}
}

// Get extracts the workspace ID from the context and returns a client from
// the corresponding workspace pool. Returns a clear error if no workspace ID
// is found in the context or the workspace is not registered.
func (mp *MultiPool) Get(ctx context.Context) (*rpc.Client, error) {
	wsID := mp.extractWS(ctx)
	if wsID == "" {
		return nil, fmt.Errorf("%w: call WithWorkspace(ctx, id) or ensure WorkspaceMiddleware is applied", ErrNoWorkspaceInContext)
	}

	mp.mu.RLock()
	p, ok := mp.pools[wsID]
	mp.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrWorkspaceNotRegistered, wsID)
	}

	client, err := p.Get(ctx)
	if err != nil {
		return nil, err
	}

	// Track which pool issued this client so Put/Discard return it correctly.
	mp.mu.Lock()
	mp.clientOwner[client] = p
	mp.mu.Unlock()

	return client, nil
}

// Put returns a client to the pool that issued it.
func (mp *MultiPool) Put(client *rpc.Client) {
	if client == nil {
		return
	}

	mp.mu.Lock()
	p, ok := mp.clientOwner[client]
	if ok {
		delete(mp.clientOwner, client)
	}
	mp.mu.Unlock()

	if ok {
		p.Put(client)
	}
}

// Discard discards a bad client, returning it to the pool that issued it.
func (mp *MultiPool) Discard(client *rpc.Client) {
	if client == nil {
		return
	}

	mp.mu.Lock()
	p, ok := mp.clientOwner[client]
	if ok {
		delete(mp.clientOwner, client)
	}
	mp.mu.Unlock()

	if ok {
		p.Discard(client)
	}
}

// Stats returns aggregated pool statistics across all workspaces.
func (mp *MultiPool) Stats() PoolStats {
	mp.mu.RLock()
	defer mp.mu.RUnlock()

	var agg PoolStats
	for _, p := range mp.pools {
		s := p.Stats()
		agg.Size += s.Size
		agg.Created += s.Created
		agg.Active += s.Active
		agg.Available += s.Available
	}
	agg.Closed = mp.closed
	return agg
}

// pruneClientOwner removes all clientOwner entries that reference the given pool.
// Caller must hold mp.mu (write lock).
func (mp *MultiPool) pruneClientOwner(p Pool) {
	for client, owner := range mp.clientOwner {
		if owner == p {
			delete(mp.clientOwner, client)
		}
	}
}

// Close closes all registered pools.
func (mp *MultiPool) Close() error {
	mp.mu.Lock()
	defer mp.mu.Unlock()

	if mp.closed {
		return nil
	}
	mp.closed = true
	mp.clientOwner = make(map[*rpc.Client]Pool)

	var errs []error
	for wsID, p := range mp.pools {
		if err := p.Close(); err != nil {
			errs = append(errs, fmt.Errorf("closing pool for workspace %q: %w", wsID, err))
		}
		delete(mp.pools, wsID)
	}
	return errors.Join(errs...)
}

// WorkspaceIDs returns the list of currently registered workspace IDs.
func (mp *MultiPool) WorkspaceIDs() []string {
	mp.mu.RLock()
	defer mp.mu.RUnlock()

	ids := make([]string, 0, len(mp.pools))
	for id := range mp.pools {
		ids = append(ids, id)
	}
	return ids
}

// PoolForWorkspace returns the underlying pool for a specific workspace,
// or nil if not registered. This is useful for status/health checks.
func (mp *MultiPool) PoolForWorkspace(wsID string) Pool {
	mp.mu.RLock()
	defer mp.mu.RUnlock()
	return mp.pools[wsID]
}

package fleet

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"

	"github.com/redis/go-redis/v9"
)

// StoreRegistry manages per-workspace fleet Store instances and their
// TimeoutEnforcer goroutines. All workspace Stores share a single Redis
// client; namespace isolation is achieved via key prefixes (fleet:{wsID}:...).
type StoreRegistry struct {
	mu            sync.RWMutex
	client        *redis.Client
	stores        map[string]*Store
	enforcers     map[string]*TimeoutEnforcer
	timeoutConfig TimeoutConfig
	logger        *slog.Logger
	closed        bool
}

// NewStoreRegistry creates a StoreRegistry backed by a shared Redis client.
func NewStoreRegistry(cfg RedisConfig, timeoutCfg TimeoutConfig, logger *slog.Logger) (*StoreRegistry, error) {
	if cfg.Address == "" {
		return nil, fmt.Errorf("fleet redis address is required")
	}
	if logger == nil {
		logger = slog.Default()
	}

	password := cfg.Password
	if password == "" {
		if p := os.Getenv("FLEET_REDIS_PASSWORD"); p != "" {
			password = p
		} else if p := os.Getenv("LOOM_REDIS_PASSWORD"); p != "" {
			password = p
		}
	}

	poolSize := cfg.PoolSize
	if poolSize == 0 {
		poolSize = 10
	}

	client := redis.NewClient(&redis.Options{
		Addr:     cfg.Address,
		Password: password,
		DB:       cfg.DB,
		PoolSize: poolSize,
	})

	return &StoreRegistry{
		client:        client,
		stores:        make(map[string]*Store),
		enforcers:     make(map[string]*TimeoutEnforcer),
		timeoutConfig: timeoutCfg,
		logger:        logger,
	}, nil
}

// Register creates a workspace-scoped Store and starts its TimeoutEnforcer.
// Idempotent: registering the same workspace ID twice is a no-op.
func (r *StoreRegistry) Register(wsID string) error {
	if wsID == "" {
		return errors.New("workspace ID must not be empty")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed {
		return errors.New("fleet store registry is closed")
	}

	if _, exists := r.stores[wsID]; exists {
		return nil // idempotent
	}

	store := NewStoreForClient(r.client, wsID, r.logger)
	r.stores[wsID] = store

	enforcer := NewTimeoutEnforcer(store, r.timeoutConfig, r.logger)
	enforcer.Start()
	r.enforcers[wsID] = enforcer

	r.logger.Debug("fleet store registered", "workspace", wsID)
	return nil
}

// Deregister stops the TimeoutEnforcer and removes the Store for a workspace.
// No-op for unknown workspace IDs. Does not close the shared Redis client.
func (r *StoreRegistry) Deregister(wsID string) {
	if wsID == "" {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if enforcer, ok := r.enforcers[wsID]; ok {
		enforcer.Stop()
		delete(r.enforcers, wsID)
	}
	delete(r.stores, wsID)

	r.logger.Debug("fleet store deregistered", "workspace", wsID)
}

// Get returns the Store for a workspace. Returns (nil, false) if not found.
func (r *StoreRegistry) Get(wsID string) (*Store, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.stores[wsID]
	return s, ok
}

// GetTotalTimeoutCount sums timeout counts across all workspace enforcers.
func (r *StoreRegistry) GetTotalTimeoutCount() int64 {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var total int64
	for _, e := range r.enforcers {
		total += e.GetTimeoutCount()
	}
	return total
}

// Client returns the shared Redis client. This is useful for subsystems that
// need the client directly (e.g., signing key management) without going
// through a workspace-scoped Store.
func (r *StoreRegistry) Client() *redis.Client {
	return r.client
}

// Close stops all enforcers, clears maps, and closes the shared Redis client.
func (r *StoreRegistry) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed {
		return nil
	}
	r.closed = true

	for wsID, enforcer := range r.enforcers {
		enforcer.Stop()
		delete(r.enforcers, wsID)
	}

	for wsID := range r.stores {
		delete(r.stores, wsID)
	}

	return r.client.Close()
}

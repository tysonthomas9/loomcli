package daemon

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/rpc"
)

const (
	// DefaultDialTimeout is the default timeout for establishing connections.
	DefaultDialTimeout = 2 * time.Second

	// DefaultRequestTimeout is the default timeout for RPC requests.
	DefaultRequestTimeout = 30 * time.Second

	// HealthCheckInterval is how often to check connection health.
	HealthCheckInterval = 5 * time.Second

	// DefaultPoolSize is the default number of connections in the pool.
	// Set to 100 to support concurrent fleet worker requests.
	DefaultPoolSize = 100

	// DefaultPoolTimeout is the default timeout for acquiring a connection from the pool.
	DefaultPoolTimeout = 10 * time.Second
)

// Pool is the interface for connection pool operations.
// Both ConnectionPool and ProtectedPool implement this interface.
type Pool interface {
	Get(ctx context.Context) (*rpc.Client, error)
	Put(client *rpc.Client)
	PutAfterError(client *rpc.Client)
	Discard(client *rpc.Client)
	Stats() PoolStats
	Close() error
}

// ConnectionPool manages a pool of connections to the daemon.
// It provides concurrent access for multiple HTTP requests.
type ConnectionPool struct {
	socketPath   string
	poolSize     int
	dialTimeout  time.Duration
	poolTimeout  time.Duration
	available    chan *rpc.Client
	mu           sync.Mutex
	closed       bool
	activeCount  int
	createdCount int

	// lastConnectedAt tracks when a connection was last successfully created or
	// returned to the pool. Used by createConnection to distinguish a daemon that
	// is alive-but-overloaded (semaphore full) from one that is genuinely dead.
	lastConnectedAt time.Time
}

// NewConnectionPool creates a new connection pool with the specified size.
func NewConnectionPool(socketPath string, poolSize int) (*ConnectionPool, error) {
	if socketPath == "" {
		return nil, ErrInvalidSocketPath
	}
	if poolSize <= 0 {
		poolSize = DefaultPoolSize
	}

	pool := &ConnectionPool{
		socketPath:  socketPath,
		poolSize:    poolSize,
		dialTimeout: DefaultDialTimeout,
		poolTimeout: DefaultPoolTimeout,
		available:   make(chan *rpc.Client, poolSize),
	}

	return pool, nil
}

// NewConnectionPoolAutoDiscover creates a pool that auto-discovers the daemon.
func NewConnectionPoolAutoDiscover(workspacePath string, poolSize int) (*ConnectionPool, error) {
	socketPath, err := DiscoverSocketPath(workspacePath)
	if err != nil {
		// Try to compute the path for lazy connection
		socketPath, err = ComputeSocketPath(workspacePath)
		if err != nil {
			return nil, err
		}
	}

	return NewConnectionPool(socketPath, poolSize)
}

// Get borrows a connection from the pool.
// If no connection is available, it creates a new one up to poolSize.
// Returns an error if the pool is exhausted or closed.
func (p *ConnectionPool) Get(ctx context.Context) (*rpc.Client, error) {
	// Check if context is already done
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	return p.tryGet(ctx)
}

// tryGet attempts to get a connection from the pool.
func (p *ConnectionPool) tryGet(ctx context.Context) (*rpc.Client, error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, ErrPoolClosed
	}
	p.mu.Unlock()

	// Try to get an existing connection without blocking
	select {
	case client := <-p.available:
		p.mu.Lock()
		p.activeCount++
		p.mu.Unlock()
		return client, nil
	default:
	}

	// Check if we can create a new connection
	p.mu.Lock()
	if p.createdCount < p.poolSize {
		p.createdCount++
		p.activeCount++
		p.mu.Unlock()

		client, err := p.createConnection()
		if err != nil {
			p.mu.Lock()
			p.createdCount--
			p.activeCount--
			p.mu.Unlock()
			return nil, err
		}
		return client, nil
	}
	p.mu.Unlock()

	// Pool is at capacity, wait for a connection
	timeout := p.poolTimeout
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, ctx.Err()
		}
		if remaining < timeout {
			timeout = remaining
		}
	}

	select {
	case client := <-p.available:
		p.mu.Lock()
		p.activeCount++
		p.mu.Unlock()
		return client, nil
	case <-time.After(timeout):
		return nil, ErrPoolExhausted
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Put returns a connection to the pool.
// If the pool is closed or full, the connection is closed instead.
func (p *ConnectionPool) Put(client *rpc.Client) {
	if client == nil {
		return
	}

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		_ = client.Close()
		return
	}
	p.activeCount--
	p.lastConnectedAt = time.Now()
	p.mu.Unlock()

	// Try to return to pool
	select {
	case p.available <- client:
		// Returned to pool successfully
	default:
		// Pool is full, close the connection
		_ = client.Close()
		p.mu.Lock()
		p.createdCount--
		p.mu.Unlock()
	}
}

// PutAfterError validates a connection before deciding to return it to the pool
// or discard it. Use this instead of Put when the caller's RPC operation failed
// and the connection health is unknown.
func (p *ConnectionPool) PutAfterError(client *rpc.Client) {
	if client == nil {
		return
	}
	if p.validateConnection(client) {
		p.Put(client)
		return
	}
	p.Discard(client)
}

// Discard closes a connection without returning it to the pool.
// Use this instead of Put when the connection is known to be in a bad state
// (e.g., after a timeout or protocol error).
func (p *ConnectionPool) Discard(client *rpc.Client) {
	if client == nil {
		return
	}

	_ = client.Close()
	p.mu.Lock()
	p.activeCount--
	p.createdCount--
	p.mu.Unlock()
}

// Close closes all pooled connections.
func (p *ConnectionPool) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	p.mu.Unlock()

	// Close all available connections
	close(p.available)
	for client := range p.available {
		_ = client.Close()
	}

	return nil
}

// Size returns the configured pool size.
func (p *ConnectionPool) Size() int {
	return p.poolSize
}

// Stats returns pool statistics.
func (p *ConnectionPool) Stats() PoolStats {
	p.mu.Lock()
	defer p.mu.Unlock()

	return PoolStats{
		Size:      p.poolSize,
		Created:   p.createdCount,
		Active:    p.activeCount,
		Available: len(p.available),
		Closed:    p.closed,
	}
}

// PoolStats contains statistics about the connection pool.
type PoolStats struct {
	Size      int  `json:"size"`      // Configured pool size
	Created   int  `json:"created"`   // Number of connections created
	Active    int  `json:"active"`    // Number of connections currently in use
	Available int  `json:"available"` // Number of connections available in pool
	Closed    bool `json:"closed"`    // Whether the pool is closed
}

// SetDialTimeout sets the timeout for creating new connections.
func (p *ConnectionPool) SetDialTimeout(timeout time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.dialTimeout = timeout
}

// SetPoolTimeout sets the timeout for acquiring a connection from the pool.
func (p *ConnectionPool) SetPoolTimeout(timeout time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.poolTimeout = timeout
}

// SocketPath returns the socket path being used.
func (p *ConnectionPool) SocketPath() string {
	return p.socketPath
}

// createConnection creates a new RPC connection.
func (p *ConnectionPool) createConnection() (*rpc.Client, error) {
	client, err := rpc.TryConnectWithTimeout(p.socketPath, p.dialTimeout)
	if err != nil {
		return nil, ErrConnectionTimeout
	}
	if client == nil {
		// TryConnectWithTimeout returned nil, nil — the socket accepted and
		// immediately closed (daemon semaphore full) or the daemon is down.
		// Check whether we had a successful connection recently; if so the
		// daemon is alive but overloaded, not dead.
		p.mu.Lock()
		recentlyAlive := !p.lastConnectedAt.IsZero() && time.Since(p.lastConnectedAt) < 30*time.Second
		p.mu.Unlock()
		if recentlyAlive {
			return nil, ErrPoolExhausted // daemon alive but overloaded — don't trip breaker
		}
		return nil, ErrDaemonNotRunning // daemon genuinely unreachable
	}

	// Successful connection — stamp the last-connected time.
	p.mu.Lock()
	p.lastConnectedAt = time.Now()
	p.mu.Unlock()

	client.SetTimeout(DefaultRequestTimeout)
	return client, nil
}

// validateConnection checks if a connection is still healthy.
func (p *ConnectionPool) validateConnection(client *rpc.Client) bool {
	if client == nil {
		return false
	}

	// Try a ping to validate the connection
	if err := client.Ping(); err != nil {
		slog.Warn("pool: connection validation failed", "err", err)
		return false
	}
	return true
}

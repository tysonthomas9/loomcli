package daemon

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/steveyegge/beads/internal/rpc"
)

const (
	// DefaultPoolSize is the default number of connections in the pool.
	DefaultPoolSize = 5

	// DefaultPoolTimeout is the default timeout for acquiring a connection from the pool.
	DefaultPoolTimeout = 10 * time.Second

	// maxRetries limits the number of retry attempts when finding stale connections.
	maxRetries = 3
)

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

	// Use iterative approach with bounded retries to avoid unbounded recursion
	for retries := 0; retries < maxRetries; retries++ {
		client, err, shouldRetry := p.tryGet(ctx)
		if err != nil {
			return nil, err
		}
		if client != nil {
			return client, nil
		}
		if !shouldRetry {
			break
		}
	}

	return nil, ErrPoolExhausted
}

// tryGet attempts to get a connection from the pool.
// Returns (client, nil, false) on success.
// Returns (nil, err, false) on error.
// Returns (nil, nil, true) if a stale connection was found and caller should retry.
func (p *ConnectionPool) tryGet(ctx context.Context) (*rpc.Client, error, bool) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, ErrPoolClosed, false
	}
	p.mu.Unlock()

	// Try to get an existing connection without blocking
	select {
	case client := <-p.available:
		// Validate connection is still healthy
		if p.validateConnection(client) {
			p.mu.Lock()
			p.activeCount++
			p.mu.Unlock()
			return client, nil, false
		}
		// Connection is stale, close it
		_ = client.Close()
		p.mu.Lock()
		p.createdCount--
		p.mu.Unlock()
		// Signal retry - we freed a slot
		return nil, nil, true
	default:
		// No connection available in channel
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
			return nil, err, false
		}
		return client, nil, false
	}
	p.mu.Unlock()

	// Pool is at capacity, wait for a connection
	timeout := p.poolTimeout
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, ctx.Err(), false
		}
		if remaining < timeout {
			timeout = remaining
		}
	}

	select {
	case client := <-p.available:
		if p.validateConnection(client) {
			p.mu.Lock()
			p.activeCount++
			p.mu.Unlock()
			return client, nil, false
		}
		// Connection is stale, close it and signal retry
		_ = client.Close()
		p.mu.Lock()
		p.createdCount--
		p.mu.Unlock()
		return nil, nil, true

	case <-time.After(timeout):
		return nil, ErrPoolExhausted, false

	case <-ctx.Done():
		return nil, ctx.Err(), false
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
		return nil, ErrDaemonNotRunning
	}

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
		log.Printf("Pool: connection validation failed: %v", err)
		return false
	}
	return true
}

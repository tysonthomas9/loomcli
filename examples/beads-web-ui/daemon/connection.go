package daemon

import (
	"sync"
	"time"

	"github.com/steveyegge/beads/internal/rpc"
)

const (
	// DefaultDialTimeout is the default timeout for establishing connections.
	DefaultDialTimeout = 2 * time.Second

	// DefaultRequestTimeout is the default timeout for RPC requests.
	DefaultRequestTimeout = 30 * time.Second

	// HealthCheckInterval is how often to check connection health.
	HealthCheckInterval = 5 * time.Second
)

// DaemonConnection manages the connection to the beads daemon.
// It handles connection lifecycle, health checks, and reconnection.
type DaemonConnection struct {
	socketPath     string
	client         *rpc.Client
	mu             sync.RWMutex
	dialTimeout    time.Duration
	requestTimeout time.Duration
}

// NewDaemonConnection creates a connection manager with an explicit socket path.
func NewDaemonConnection(socketPath string) (*DaemonConnection, error) {
	if socketPath == "" {
		return nil, ErrInvalidSocketPath
	}

	return &DaemonConnection{
		socketPath:     socketPath,
		dialTimeout:    DefaultDialTimeout,
		requestTimeout: DefaultRequestTimeout,
	}, nil
}

// NewDaemonConnectionAutoDiscover creates a connection manager that auto-discovers the daemon
// for the given workspace path.
func NewDaemonConnectionAutoDiscover(workspacePath string) (*DaemonConnection, error) {
	socketPath, err := DiscoverSocketPath(workspacePath)
	if err != nil {
		// If discovery fails, try to compute the path for lazy connection
		socketPath, err = ComputeSocketPath(workspacePath)
		if err != nil {
			return nil, err
		}
	}

	return &DaemonConnection{
		socketPath:     socketPath,
		dialTimeout:    DefaultDialTimeout,
		requestTimeout: DefaultRequestTimeout,
	}, nil
}

// Connect establishes or re-establishes connection to the daemon.
func (dc *DaemonConnection) Connect() error {
	dc.mu.Lock()
	defer dc.mu.Unlock()

	// Close existing connection if any
	if dc.client != nil {
		_ = dc.client.Close()
		dc.client = nil
	}

	// Attempt to connect
	client, err := rpc.TryConnectWithTimeout(dc.socketPath, dc.dialTimeout)
	if err != nil {
		return ErrConnectionTimeout
	}
	if client == nil {
		return ErrDaemonNotRunning
	}

	// Set request timeout
	client.SetTimeout(dc.requestTimeout)
	dc.client = client
	return nil
}

// Disconnect gracefully closes the connection.
func (dc *DaemonConnection) Disconnect() error {
	dc.mu.Lock()
	defer dc.mu.Unlock()

	if dc.client != nil {
		err := dc.client.Close()
		dc.client = nil
		return err
	}
	return nil
}

// IsConnected checks if there's an active connection.
// Note: This doesn't guarantee the daemon is healthy, just that we have a connection.
func (dc *DaemonConnection) IsConnected() bool {
	dc.mu.RLock()
	defer dc.mu.RUnlock()
	return dc.client != nil
}

// Client returns the underlying RPC client for making calls.
// Returns nil if not connected. Callers should use GetClient() instead
// for automatic connection handling.
func (dc *DaemonConnection) Client() *rpc.Client {
	dc.mu.RLock()
	defer dc.mu.RUnlock()
	return dc.client
}

// GetClient returns the RPC client, connecting if necessary.
// This implements lazy connection - it will attempt to connect
// on first use and reconnect if the connection is lost.
func (dc *DaemonConnection) GetClient() (*rpc.Client, error) {
	dc.mu.RLock()
	if dc.client != nil {
		client := dc.client
		dc.mu.RUnlock()
		return client, nil
	}
	dc.mu.RUnlock()

	// Need to connect
	if err := dc.Connect(); err != nil {
		return nil, err
	}

	dc.mu.RLock()
	defer dc.mu.RUnlock()
	return dc.client, nil
}

// SocketPath returns the socket path being used.
func (dc *DaemonConnection) SocketPath() string {
	return dc.socketPath
}

// SetDialTimeout sets the timeout for establishing connections.
func (dc *DaemonConnection) SetDialTimeout(timeout time.Duration) {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	dc.dialTimeout = timeout
}

// SetRequestTimeout sets the timeout for RPC requests.
func (dc *DaemonConnection) SetRequestTimeout(timeout time.Duration) {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	dc.requestTimeout = timeout
	if dc.client != nil {
		dc.client.SetTimeout(timeout)
	}
}

// Health checks the daemon health via the connection.
// Returns the health response or an error if unhealthy/unreachable.
func (dc *DaemonConnection) Health() (*rpc.HealthResponse, error) {
	client, err := dc.GetClient()
	if err != nil {
		return nil, err
	}

	health, err := client.Health()
	if err != nil {
		// Connection may be stale, mark as disconnected only if it's still the same client
		// This prevents race conditions where another goroutine reconnected
		dc.mu.Lock()
		if dc.client == client {
			dc.client = nil
		}
		dc.mu.Unlock()
		return nil, ErrDaemonUnhealthy
	}

	if health.Status == "unhealthy" {
		return health, ErrDaemonUnhealthy
	}

	return health, nil
}

// Reconnect forces a reconnection to the daemon.
// Useful after detecting a stale connection.
func (dc *DaemonConnection) Reconnect() error {
	dc.mu.Lock()
	defer dc.mu.Unlock()

	// Close existing connection if any
	if dc.client != nil {
		_ = dc.client.Close()
		dc.client = nil
	}

	// Attempt to connect (inline to avoid double-locking)
	client, err := rpc.TryConnectWithTimeout(dc.socketPath, dc.dialTimeout)
	if err != nil {
		return ErrConnectionTimeout
	}
	if client == nil {
		return ErrDaemonNotRunning
	}

	// Set request timeout
	client.SetTimeout(dc.requestTimeout)
	dc.client = client
	return nil
}

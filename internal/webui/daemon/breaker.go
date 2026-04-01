package daemon

import (
	"context"
	"errors"

	"github.com/tysonthomas9/loomcli/internal/circuitbreaker"
	"github.com/tysonthomas9/loomcli/internal/rpc"
)

// ProtectedPool wraps a ConnectionPool with a circuit breaker to fail fast
// when the daemon is unavailable, preventing cascading failures.
type ProtectedPool struct {
	pool    *ConnectionPool
	breaker *circuitbreaker.Breaker
}

// NewProtectedPool creates a ProtectedPool wrapping the given pool and breaker.
func NewProtectedPool(pool *ConnectionPool, breaker *circuitbreaker.Breaker) *ProtectedPool {
	return &ProtectedPool{pool: pool, breaker: breaker}
}

// Get borrows a connection from the pool, protected by the circuit breaker.
// Returns ErrCircuitOpen if the breaker is open.
func (p *ProtectedPool) Get(ctx context.Context) (*rpc.Client, error) {
	return circuitbreaker.ExecuteWithResult(p.breaker, func() (*rpc.Client, error) {
		return p.pool.Get(ctx)
	})
}

// Put returns a connection to the underlying pool. Not protected by the breaker.
func (p *ProtectedPool) Put(client *rpc.Client) {
	p.pool.Put(client)
}

// PutAfterError validates a connection before deciding to return it or discard it.
func (p *ProtectedPool) PutAfterError(client *rpc.Client) {
	p.pool.PutAfterError(client)
}

// Discard closes a connection without returning it to the pool.
// Use this instead of Put when the connection is known to be in a bad state.
func (p *ProtectedPool) Discard(client *rpc.Client) {
	p.pool.Discard(client)
}

// Stats returns pool statistics from the underlying pool.
func (p *ProtectedPool) Stats() PoolStats {
	return p.pool.Stats()
}

// Close closes the underlying connection pool.
func (p *ProtectedPool) Close() error {
	return p.pool.Close()
}

// BreakerState returns the current circuit breaker state.
func (p *ProtectedPool) BreakerState() circuitbreaker.State {
	return p.breaker.GetState()
}

// BreakerStats returns circuit breaker statistics.
func (p *ProtectedPool) BreakerStats() circuitbreaker.BreakerStats {
	return p.breaker.Stats()
}

// Size returns the configured pool size from the underlying pool.
func (p *ProtectedPool) Size() int {
	return p.pool.Size()
}

// SocketPath returns the socket path from the underlying pool.
func (p *ProtectedPool) SocketPath() string {
	return p.pool.SocketPath()
}

// DaemonShouldTrip classifies daemon errors for the circuit breaker.
// Transport/availability errors trip the breaker; application errors do not.
func DaemonShouldTrip(err error) bool {
	if err == nil {
		return false
	}
	// Trip on daemon availability errors
	if errors.Is(err, ErrDaemonNotRunning) ||
		errors.Is(err, ErrConnectionTimeout) ||
		errors.Is(err, ErrDaemonUnhealthy) ||
		errors.Is(err, ErrPoolExhausted) {
		return true
	}
	// Don't trip on client-side cancellation
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	// Don't trip on pool lifecycle errors
	if errors.Is(err, ErrPoolClosed) || errors.Is(err, ErrInvalidSocketPath) {
		return false
	}
	return false
}

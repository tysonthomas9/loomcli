// Package daemon provides RPC client connection infrastructure for the beads web UI server.
package daemon

import "errors"

// Error types for better HTTP error responses
var (
	// ErrDaemonNotRunning indicates the daemon is not found or not responding.
	ErrDaemonNotRunning = errors.New("daemon not running")

	// ErrDaemonUnhealthy indicates the daemon health check failed.
	ErrDaemonUnhealthy = errors.New("daemon unhealthy")

	// ErrConnectionTimeout indicates a connection attempt timed out.
	ErrConnectionTimeout = errors.New("connection timeout")

	// ErrInvalidSocketPath indicates the socket path is invalid or missing.
	ErrInvalidSocketPath = errors.New("invalid socket path")

	// ErrPoolExhausted indicates all connections in the pool are in use.
	ErrPoolExhausted = errors.New("connection pool exhausted")

	// ErrPoolClosed indicates the pool has been closed.
	ErrPoolClosed = errors.New("connection pool closed")
)

// IsRetryable determines if an operation can be retried.
// Connection timeout and temporary daemon issues are retryable.
func IsRetryable(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, ErrDaemonNotRunning) ||
		errors.Is(err, ErrConnectionTimeout) ||
		errors.Is(err, ErrDaemonUnhealthy)
}

package rpc

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/debug"
	"github.com/tysonthomas9/loomcli/internal/lockfile"
)

// maxClientMessageSize is the maximum size of a single RPC response the client will read (10 MB).
const maxClientMessageSize int64 = 10 * 1024 * 1024

// rpcDebugEnabled returns true if BD_DEBUG_RPC environment variable is set
func rpcDebugEnabled() bool {
	val := os.Getenv("BD_DEBUG_RPC")
	return val == "1" || val == "true"
}

// rpcDebugLog logs to stderr if BD_DEBUG_RPC is enabled
func rpcDebugLog(format string, args ...interface{}) {
	if rpcDebugEnabled() {
		fmt.Fprintf(os.Stderr, "[RPC DEBUG] "+format+"\n", args...)
	}
}

// ClientVersion is the version of this RPC client
// This should match the bd CLI version for proper compatibility checks
// It's set dynamically by main.go from cmd/bd/version.go before making RPC calls
var ClientVersion = "0.0.0" // Placeholder; overridden at startup

// Client represents an RPC client that connects to the daemon.
// The client is safe for concurrent use of SetTimeout, SetDatabasePath, and SetActor.
// However, the underlying connection (conn) is not safe for concurrent RPC calls -
// use connection pooling for concurrent operations.
type Client struct {
	mu         sync.RWMutex // protects timeout, dbPath, actor, authToken; conn and socketPath are immutable after construction
	conn       net.Conn
	socketPath string
	timeout    time.Duration
	dbPath     string // Expected database path for validation
	actor      string // Actor for audit trail (who is performing operations)
	authToken  string // Shared-secret authentication token
}

// TryConnect attempts to connect to the daemon socket
// Returns nil if no daemon is running or unhealthy
func TryConnect(socketPath string) (*Client, error) {
	return TryConnectWithTimeout(socketPath, 200*time.Millisecond)
}

// TryConnectWithTimeout attempts to connect to the daemon socket using the provided dial timeout.
// Returns nil if no daemon is running or unhealthy.
func TryConnectWithTimeout(socketPath string, dialTimeout time.Duration) (*Client, error) {
	rpcDebugLog("attempting connection to socket: %s", socketPath)

	// Fast probe: check daemon lock before attempting RPC connection if socket doesn't exist
	// This eliminates unnecessary connection attempts when no daemon is running
	// If socket exists, we skip lock check for backwards compatibility and test scenarios
	socketExists := endpointExists(socketPath)
	rpcDebugLog("socket exists check: %v", socketExists)

	if !socketExists {
		beadsDir := filepath.Dir(socketPath)
		running, _ := lockfile.TryDaemonLock(beadsDir)
		if !running {
			debug.Logf("daemon lock not held and socket missing (no daemon running)")
			rpcDebugLog("daemon lock not held (no daemon running)")
			// Self-heal: clean up stale artifacts when lock is free and socket is missing
			cleanupStaleDaemonArtifacts(beadsDir)
			return nil, nil
		}
		// Lock is held but socket was missing - re-check socket existence atomically
		// to handle race where daemon just started between first check and lock check
		rpcDebugLog("daemon lock held but socket was missing - re-checking socket existence")
		socketExists = endpointExists(socketPath)
		if !socketExists {
			// Lock held but socket still missing after re-check - daemon startup or crash
			debug.Logf("daemon lock held but socket missing after re-check (startup race or crash): %s", socketPath)
			rpcDebugLog("connection aborted: socket still missing despite lock being held")
			return nil, nil
		}
		rpcDebugLog("socket now exists after re-check (daemon startup race resolved)")
	}

	if dialTimeout <= 0 {
		dialTimeout = 200 * time.Millisecond
	}

	rpcDebugLog("dialing socket (timeout: %v)", dialTimeout)
	dialStart := time.Now()
	conn, err := dialRPC(socketPath, dialTimeout)
	dialDuration := time.Since(dialStart)

	if err != nil {
		debug.Logf("failed to connect to RPC endpoint: %v", err)
		rpcDebugLog("dial failed after %v: %v", dialDuration, err)

		// Fast-fail: socket exists but dial failed - check if daemon actually alive
		// If lock is not held, daemon crashed and left stale socket - clean up immediately
		beadsDir := filepath.Dir(socketPath)
		running, _ := lockfile.TryDaemonLock(beadsDir)
		if !running {
			rpcDebugLog("daemon not running (lock free) - cleaning up stale socket")
			cleanupStaleDaemonArtifacts(beadsDir)
			_ = os.Remove(socketPath) // Also remove stale socket
		}
		return nil, nil
	}

	rpcDebugLog("dial succeeded in %v", dialDuration)

	// Load auth token from file next to socket (empty if not found = backward compat)
	authToken := loadAuthToken(socketPath)
	rpcDebugLog("auth token loaded: %v", authToken != "")

	client := &Client{
		conn:       conn,
		socketPath: socketPath,
		timeout:    30 * time.Second,
		authToken:  authToken,
	}

	rpcDebugLog("performing health check")
	healthStart := time.Now()
	health, err := client.Health()
	healthDuration := time.Since(healthStart)

	if err != nil {
		debug.Logf("health check failed: %v", err)
		rpcDebugLog("health check failed after %v: %v", healthDuration, err)
		_ = conn.Close()
		return nil, nil
	}

	if health.Status == "unhealthy" {
		debug.Logf("daemon unhealthy: %s", health.Error)
		rpcDebugLog("daemon unhealthy (checked in %v): %s", healthDuration, health.Error)
		_ = conn.Close()
		return nil, nil
	}

	debug.Logf("connected to daemon (status: %s, uptime: %.1fs)",
		health.Status, health.Uptime)
	rpcDebugLog("connection successful (health check: %v, status: %s, uptime: %.1fs)",
		healthDuration, health.Status, health.Uptime)

	return client, nil
}

// Close closes the connection to the daemon
func (c *Client) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// SetTimeout sets the request timeout duration
func (c *Client) SetTimeout(timeout time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.timeout = timeout
}

// SetDatabasePath sets the expected database path for validation
func (c *Client) SetDatabasePath(dbPath string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.dbPath = dbPath
}

// SetActor sets the actor for audit trail (who is performing operations)
func (c *Client) SetActor(actor string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.actor = actor
}

// SetAuthToken sets the authentication token for RPC requests
func (c *Client) SetAuthToken(token string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.authToken = token
}

// Execute sends an RPC request and waits for a response
func (c *Client) Execute(operation string, args interface{}) (*Response, error) {
	return c.ExecuteWithCwd(operation, args, "")
}

// ExecuteWithCwd sends an RPC request with an explicit cwd (or current dir if empty string)
func (c *Client) ExecuteWithCwd(operation string, args interface{}, cwd string) (*Response, error) {
	return c.executeWithTimeout(operation, args, cwd, 0)
}

// executeWithTimeout sends an RPC request with an optional timeout override.
// If timeoutOverride is non-zero, it is used instead of the client's configured timeout.
// Mutable fields (actor, dbPath, timeout) are read under RLock for thread safety.
func (c *Client) executeWithTimeout(operation string, args interface{}, cwd string, timeoutOverride time.Duration) (*Response, error) {
	argsJSON, err := json.Marshal(args)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal args: %w", err)
	}

	// Use provided cwd, or get current working directory for database routing
	if cwd == "" {
		cwd, _ = os.Getwd()
	}

	// Read mutable fields under lock
	c.mu.RLock()
	actor := c.actor
	dbPath := c.dbPath
	timeout := c.timeout
	authToken := c.authToken
	c.mu.RUnlock()

	if timeoutOverride > 0 {
		timeout = timeoutOverride
	}

	req := Request{
		Operation:     operation,
		Args:          argsJSON,
		Actor:         actor,
		ClientVersion: ClientVersion,
		Cwd:           cwd,
		ExpectedDB:    dbPath,
		AuthToken:     authToken,
	}

	reqJSON, err := json.Marshal(req) // #nosec G117 — AuthToken is an internal daemon IPC token, not a user secret
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	if timeout > 0 {
		deadline := time.Now().Add(timeout)
		if err := c.conn.SetDeadline(deadline); err != nil {
			return nil, fmt.Errorf("failed to set deadline: %w", err)
		}
	}

	writer := bufio.NewWriter(c.conn)
	if _, err := writer.Write(reqJSON); err != nil {
		return nil, fmt.Errorf("failed to write request: %w", err)
	}
	if err := writer.WriteByte('\n'); err != nil {
		return nil, fmt.Errorf("failed to write newline: %w", err)
	}
	if err := writer.Flush(); err != nil {
		return nil, fmt.Errorf("failed to flush: %w", err)
	}

	lr := &io.LimitedReader{R: c.conn, N: maxClientMessageSize}
	reader := bufio.NewReader(lr)
	respLine, err := reader.ReadBytes('\n')
	if err != nil {
		if lr.N <= 0 {
			return nil, fmt.Errorf("response too large (exceeds %d bytes)", maxClientMessageSize)
		}
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var resp Response
	if err := json.Unmarshal(respLine, &resp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	if !resp.Success {
		return &resp, fmt.Errorf("operation failed: %s", resp.Error)
	}

	return &resp, nil
}

// cleanupStaleDaemonArtifacts removes stale daemon.pid file when socket is missing and lock is free.
// This prevents stale artifacts from accumulating after daemon crashes.
// Only removes pid file - lock file is managed by OS (released on process exit).
func cleanupStaleDaemonArtifacts(beadsDir string) {
	pidFile := filepath.Join(beadsDir, "daemon.pid")

	// Check if pid file exists
	if _, err := os.Stat(pidFile); err != nil {
		// No pid file to clean up
		return
	}

	// Remove stale pid file
	if err := os.Remove(pidFile); err != nil {
		debug.Logf("failed to remove stale pid file: %v", err)
		return
	}

	debug.Logf("removed stale daemon.pid file (lock free, socket missing)")
}

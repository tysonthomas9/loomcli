package webui

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strconv"
	"time"

	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/webui/fleet"
)

const (
	defaultPort            = 8080
	defaultPoolSize        = 100
	defaultShutdownTimeout = 5 * time.Second
	defaultMaxPortAttempts = 10
)

// ServerConfig holds configuration for the web UI server.
type ServerConfig struct {
	Port                    int
	BindAddress             string // Listen address (default: "127.0.0.1"; use "0.0.0.0" for all interfaces)
	SocketPath              string
	PoolSize                int
	CORSEnabled             bool
	CORSOrigins             []string
	ShutdownTimeout         time.Duration
	MaxPortAttempts         int
	TerminalCmd             string
	MaxTerminalSessions     int  // Maximum concurrent terminal connections (0 = default 20)
	FleetEnabled            bool // Register fleet API routes (requires Redis coordination)
	FleetRedis              *fleet.RedisConfig
	FleetJWTKey             []byte                                               // Pre-provisioned JWT signing key for fleet auth (optional; if nil, server generates one)
	FleetAPIKey             string                                               // Pre-shared API key for fleet worker registration (required for fleet register endpoint)
	APIKey                  string                                               `json:"-"` // Pre-shared API key for WebUI auth (if empty and AuthEnabled, auto-generate)
	AuthEnabled             bool                                                 // Whether API authentication is enabled (default: true)
	HSTSEnabled             bool                                                 // Whether to send Strict-Transport-Security header (use when behind TLS-terminating proxy)
	ExtAuthURL              string                                               // Auth service base URL (e.g., "https://auth.loomcli.com"); empty = open mode
	ExtAuthIssuer           string                                               // Expected JWT issuer (validated against "iss" claim; defaults to ExtAuthURL)
	ExtAuthAudience         string                                               // Expected JWT audience (validated against "aud" claim; defaults to "loom")
	ExtAuthAllowInsecure    bool                                                 // Allow HTTP for non-loopback --auth-url (escape hatch for Docker networks)
	LoomServerURL           string                                               // Default target URL for the loom API proxy (set by 'loom serve')
	DevMode                 bool                                                 // Serve frontend from disk instead of embedded FS
	DevFrontendDir          string                                               // Directory to serve in dev mode (default: internal/webui/frontend/dist)
	GitOps                  GitOps                                               // Git operations interface (optional; nil disables git endpoints)
	FileOps                 FileOps                                              // File operations interface (optional; nil disables file endpoints)
	WorkspaceConfigFn       func() (*WorkspaceData, error)                       // Workspace topology supplier; nil = single-repo mode
	WorkspaceConfigByIDFn   func(string) (*WorkspaceData, error)                 // Workspace topology supplier by ID; nil = falls back to WorkspaceConfigFn
	WorkspaceDeleteFn       func(name string) error                              // Workspace deletion function; nil = deletion unavailable
	SetDefaultWorkspaceFn   func(name string) error                              // Set default workspace in config; nil = feature disabled
	ClearDefaultWorkspaceFn func() error                                         // Clear default workspace in config; nil = feature disabled
	WorkspaceCreateFn       WorkspaceCreateFn                                    // Workspace creation function; nil = creation unavailable
	WorkspaceListFn         func() (map[string]string, error)                    // Returns all configured workspaces as id→path (UUID preferred, name fallback for pre-migration); nil = single-workspace mode
	InitialWorkspaceID      string                                               // Stable UUID of the initial workspace (CWD); if empty, falls back to filepath.Base(cwd)
	WorkspaceIDResolverFn   WorkspaceIDResolverFn                                // Resolves workspace name → UUID; nil = no resolution available
	BackendOps              BackendOps                                           // Backend health operations interface (optional; nil disables backend health endpoint)
	ScrollbackMaxLines      int                                                  // Maximum lines per scrollback buffer (0 = default 10000)
	SessionsStore           *sessions.Store                                      // File-based session audit trail store (optional; nil disables session endpoints)
	Logger                  *slog.Logger                                         // Structured logger (optional; nil falls back to slog.Default())
	SentryDSN               string                                               // Sentry/GlitchTip DSN for error tracking (optional; empty disables)
	DaemonStartupFn         func(ctx context.Context, onReady func(wsID string)) // Starts daemons for secondary workspaces; calls onReady(wsID) when each is reachable
	WorkspaceAgentsFn       func(wsPath string) ([]WorkspaceAgentStatus, error)  // Returns agent status for a workspace by scanning its worktrees dir
}

// DefaultConfig returns a ServerConfig with sensible defaults.
func DefaultConfig() ServerConfig {
	return ServerConfig{
		Port:            defaultPort,
		BindAddress:     "127.0.0.1",
		PoolSize:        defaultPoolSize,
		ShutdownTimeout: defaultShutdownTimeout,
		MaxPortAttempts: defaultMaxPortAttempts,
	}
}

// findAvailablePort attempts to find an available port starting from startPort.
// It tries up to maxAttempts consecutive ports and returns a listener on the first
// available port. The caller is responsible for closing the listener.
func findAvailablePort(bindAddr string, startPort, maxAttempts int) (net.Listener, int, error) {
	for i := 0; i < maxAttempts; i++ {
		port := startPort + i
		addr := net.JoinHostPort(bindAddr, strconv.Itoa(port))
		listener, err := net.Listen("tcp", addr)
		if err != nil {
			continue
		}
		// Return the listener open to avoid race conditions
		return listener, port, nil
	}
	return nil, 0, fmt.Errorf("no available port found on %s in range %d-%d", bindAddr, startPort, startPort+maxAttempts-1)
}

// getCwd returns the current working directory.
func getCwd() (string, error) {
	return os.Getwd()
}

package webui

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/webui/fleet"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/agentcontrol"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

const (
	DefaultPort            = 8080
	DefaultPoolSize        = 100
	DefaultShutdownTimeout = 5 * time.Second
	DefaultMaxPortAttempts = 10
)

// MonitorHandlers holds pre-built HTTP handlers for the monitor/metrics
// endpoints injected by the cli package. Nil fields are skipped during
// route registration.
type MonitorHandlers struct {
	Status               http.HandlerFunc // GET /api/monitor/status
	Agents               http.HandlerFunc // GET /api/monitor/agents
	Tasks                http.HandlerFunc // GET /api/monitor/tasks
	Stats                http.HandlerFunc // GET /api/monitor/stats
	Sync                 http.HandlerFunc // GET /api/monitor/sync
	Workspaces           http.HandlerFunc // GET /api/monitor/workspaces
	StaleDetector        http.HandlerFunc // GET /api/monitor/stale-detector
	Usage                http.HandlerFunc // GET /api/monitor/usage
	Metrics              http.HandlerFunc // GET /metrics (Prometheus)
	ObservabilityMetrics http.HandlerFunc // GET /api/observability/metrics
	ObservabilityEvents  http.HandlerFunc // GET /api/observability/events
}

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
	HSTSEnabled             bool                                                 // Whether to send Strict-Transport-Security header (use when behind TLS-terminating proxy)
	ExtAuthURL              string                                               // Auth service base URL (e.g., "https://auth.loomcli.com"); empty = open mode
	ExtAuthIssuer           string                                               // Expected JWT issuer (validated against "iss" claim; defaults to ExtAuthURL)
	ExtAuthAudience         string                                               // Expected JWT audience (validated against "aud" claim; defaults to "loom")
	ExtAuthAllowInsecure    bool                                                 // Allow HTTP for non-loopback --auth-url (escape hatch for Docker networks)
	APIOnly                 bool                                                 // When true, skip embedded frontend serving (pure API mode). --frontend-url also implies this.
	MonitorHandlers         MonitorHandlers                                      // Pre-built handlers for monitor/metrics endpoints (injected by cli)
	GitOps                  ops.GitOps                                           // Git operations interface (optional; nil disables git endpoints)
	FileOps                 ops.FileOps                                          // File operations interface (optional; nil disables file endpoints)
	WorkspaceConfigFn       func() (*ops.WorkspaceData, error)                   // Workspace topology supplier; nil = single-repo mode
	WorkspaceConfigByIDFn   func(string) (*ops.WorkspaceData, error)             // Workspace topology supplier by ID; nil = falls back to WorkspaceConfigFn
	WorkspaceDeleteFn       func(name string) error                              // Workspace deletion function; nil = deletion unavailable
	SetDefaultWorkspaceFn   func(name string) error                              // Set default workspace in config; nil = feature disabled
	ClearDefaultWorkspaceFn func() error                                         // Clear default workspace in config; nil = feature disabled
	WorkspaceCreateFn       service.WorkspaceCreateFn                            // Workspace creation function; nil = creation unavailable
	WorkspaceListFn         func() (map[string]string, error)                    // Returns all configured workspaces as id→path (UUID preferred, name fallback for pre-migration); nil = single-workspace mode
	InitialWorkspaceID      string                                               // Stable UUID of the initial workspace (CWD); if empty, falls back to filepath.Base(cwd)
	WorkspaceIDResolverFn   WorkspaceIDResolverFn                                // Resolves workspace name → UUID; nil = no resolution available
	BackendOps              ops.BackendOps                                       // Backend health operations interface (optional; nil disables backend health endpoint)
	ScrollbackMaxLines      int                                                  // Maximum lines per scrollback buffer (0 = default 10000)
	NotifyTokenDir          string                                               // Directory to write notify.token (typically beads dir); empty = token file not written
	AgentControlFn          agentcontrol.AgentControlFn                          // Sends agent lifecycle commands to the daemon control socket; nil in fleet mode or --no-daemon
	DaemonSupervisorFn      func() (*DaemonSupervisorData, error)                // Returns daemon supervisor state from state file; nil = endpoint unavailable
	DaemonConfigFn          func() (json.RawMessage, error)                      // Returns effective merged daemon config as JSON; nil = endpoint unavailable
	AgentQueueFn            func(agentName string) ([]AgentQueueEntry, error)    // Returns scored work queue for named agent; nil = endpoint unavailable
	FleetMode               bool                                                 // When true, skip beads-specific lifecycle hooks (pools, subscribers); fleet server manages agents
	FleetClientURL          string                                               // Fleet server URL for fleet-mode workers (e.g., "http://fleet.example.com"); empty = no fleet client
	FleetClientWorkspace    string                                               // Fleet server workspace ID (e.g., "default"); empty = use "default"
	FleetClientAPIKey       string                                               // Pre-shared API key for fleet worker backend auth
	DaemonStartupFn         func(ctx context.Context, onReady func(wsID string)) // Starts daemons for secondary workspaces; calls onReady(wsID) when each is reachable
	// WorkspaceDaemonResolver resolves a workspace ID to its daemon paths
	// (socket, state, config, workdir); nil = workspace-scoped daemon routes unavailable.
	WorkspaceDaemonResolver func(wsID string) (*WorkspaceDaemonPaths, error)
	Logger                  *slog.Logger // Structured logger (optional; nil falls back to slog.Default())
	SentryDSN               string       // Sentry/GlitchTip DSN for error tracking (optional; empty disables)
}

// WorkspaceIDResolverFn resolves a workspace name to its stable UUID.
// Returns ("", error) if the workspace is not found or config cannot be loaded.
type WorkspaceIDResolverFn func(name string) (string, error)

// DefaultConfig returns a ServerConfig with sensible defaults.
func DefaultConfig() ServerConfig {
	return ServerConfig{
		Port:            DefaultPort,
		BindAddress:     "127.0.0.1",
		PoolSize:        DefaultPoolSize,
		ShutdownTimeout: DefaultShutdownTimeout,
		MaxPortAttempts: DefaultMaxPortAttempts,
	}
}

// FindAvailablePort attempts to find an available port starting from startPort.
// It tries up to maxAttempts consecutive ports and returns a listener on the first
// available port. The caller is responsible for closing the listener.
func FindAvailablePort(bindAddr string, startPort, maxAttempts int) (net.Listener, int, error) {
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

// GetCwd returns the current working directory.
func GetCwd() (string, error) {
	return os.Getwd()
}

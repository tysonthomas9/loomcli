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

	"github.com/tysonthomas9/loomcli/internal/app/webhookingestion"
	"github.com/tysonthomas9/loomcli/internal/app/workflowbinding"
	"github.com/tysonthomas9/loomcli/internal/app/workfloweventing"
	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/modules/artifacts"
	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
	workflowcataloghttp "github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog/httpapi"
	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/fleet"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/agentcontrol"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// AutomationCapability is the narrow composition handle consumed by the web
// application and the serve lifecycle root. It exposes no issuer, persistence
// transport, or process-wide Store.
type AutomationCapability interface {
	BindingOperations() automation.BindingOperations
	AuditQueries() automation.AuditQueries
	WebhookWorkflow() *webhookingestion.Workflow
	WorkflowBinding() *workflowbinding.Workflow
	WorkflowEventing() *workfloweventing.Workflow
	OperatorAuthorityResolver() workflowcataloghttp.OperatorAuthorityResolver
}

// ExecutionCapability is the narrow active Phase 4 composition handle. Web
// adapters receive intent APIs and exact-purpose authority resolvers only;
// the shared issuer, admission registry, and persistence adapters stay in
// app/serve.
type ExecutionCapability interface {
	TaskRunAPI() execution.TaskRunAPI
	TaskRunRequestAPI() execution.TaskRunRequestAPI
	TaskRunWorkerAPI() execution.TaskRunWorkerAPI
	TaskRunSchedulingAPI() execution.TaskRunSchedulingAPI
	WorkerProfileAPI() execution.WorkerProfileAPI
	TaskRunConvergenceAPI() execution.TaskRunConvergenceAPI
	TaskRunConvergenceSource() execution.TaskRunConvergenceSource
	TaskRunRecoveryAPI() execution.TaskRunRecoveryAPI
	TaskRunRecoveryScopes() execution.TaskRunRecoveryScopePort
	TaskRunAuthorityResolver() execution.TaskRunAuthorityResolver
	DriverRunAPI() execution.DriverRunAPI
	AwaitEventNotificationAPI() execution.AwaitEventNotificationAPI
	DriverRunOutcomeAPI() execution.DriverRunOutcomeAPI
	DriverRunAuthorityResolver() execution.DriverRunAuthorityResolver
	SystemAuthorityResolver() execution.SystemAuthorityResolver
	OperatorAuthorityResolver() workflowcataloghttp.OperatorAuthorityResolver
}

// ArtifactsCapability is the narrow owner-fenced lifecycle handle published
// by serve composition. Web modules receive only the module API, never the
// low-level FleetDB transport or the process-wide Store mutation surface.
type ArtifactsCapability interface {
	ArtifactsAPI() artifacts.API
}

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
	Port                int
	BindAddress         string // Listen address (default: "127.0.0.1"; use "0.0.0.0" for all interfaces)
	SocketPath          string
	FrontendDir         string // Optional built SPA directory served for non-API routes
	PoolSize            int
	CORSEnabled         bool
	CORSOrigins         []string
	FrontendOrigins     []string // Explicit public frontend origins; used to constrain open local file access
	ShutdownTimeout     time.Duration
	MaxPortAttempts     int
	TerminalCmd         string
	MaxTerminalSessions int  // Maximum concurrent terminal connections (0 = default 40)
	FleetEnabled        bool // Register fleet API routes (requires Redis coordination)
	// FleetClient is true when this loom server is a fleet-db CLIENT (not a
	// fleet API server itself). In this mode there is no local issue daemon to
	// talk to — the IssueBackend is fleet-db over HTTP. Drives the
	// /api/health handler choice so a missing daemon is reported as the
	// expected steady state, not a degraded one.
	FleetClient           bool
	FleetRedis            *fleet.RedisConfig
	FleetJWTKey           []byte                           // Pre-provisioned JWT signing key for fleet auth (optional; if nil, server generates one)
	FleetAPIKey           string                           // Pre-shared API key for fleet worker registration (required for fleet register endpoint)
	HSTSEnabled           bool                             // Whether to send Strict-Transport-Security header (use when behind TLS-terminating proxy)
	ExtAuthURL            string                           // Auth service base URL (e.g., "https://auth.loomcli.com"); empty = open mode
	ExtAuthIssuer         string                           // Expected JWT issuer (validated against "iss" claim; defaults to ExtAuthURL)
	ExtAuthAudience       string                           // Expected JWT audience (validated against "aud" claim; defaults to "loom")
	ExtAuthAllowInsecure  bool                             // Allow HTTP for non-loopback --auth-url (escape hatch for Docker networks)
	WorkspaceRoleResolver middleware.WorkspaceRoleResolver // Authorizes an identity for one canonical workspace; required for remote file access
	// WorkflowCatalogModule is the already-composed capability-owned route
	// module. Web UI composition only registers it; it never receives the
	// capability's persistence adapter or low-level FleetDB client.
	WorkflowCatalogModule   interface{ Register(*http.ServeMux) }
	WorkflowCatalogAPI      workflowcatalog.API
	AutomationCapability    AutomationCapability
	MonitorHandlers         MonitorHandlers             // Pre-built handlers for monitor/metrics endpoints (injected by cli)
	GitOps                  ops.GitOps                  // Git operations interface (optional; nil disables git endpoints)
	FileOps                 ops.FileOps                 // File operations interface (optional; nil disables file endpoints)
	WorkspaceDeleteFn       func(name string) error     // Workspace deletion function; nil = deletion unavailable
	SetDefaultWorkspaceFn   func(name string) error     // Deprecated compatibility hook; default workspace selection is disabled.
	ClearDefaultWorkspaceFn func() error                // Deprecated compatibility hook; default workspace selection is disabled.
	WorkspaceCreateFn       service.WorkspaceCreateFn   // Workspace creation function; nil = creation unavailable
	WorkspaceAddReposFn     service.WorkspaceAddReposFn // Attach local repos to an existing workspace; nil = unavailable
	InitialWorkspaceID      string                      // Stable key of the initial workspace
	WorkspaceIDResolverFn   WorkspaceIDResolverFn       // Resolves workspace name → UUID; nil = no resolution available
	// Store is the unified state store for workspaces, repos, agents, roles,
	// and daemon profiles. Local and distributed modes both use this store
	// as the authoritative workspace/config source.
	Store                store.Store
	BackendOps           ops.BackendOps                                       // Backend health operations interface (optional; nil disables backend health endpoint)
	ScrollbackMaxLines   int                                                  // Maximum lines per scrollback buffer (0 = default 10000)
	NotifyTokenDir       string                                               // Directory to write notify.token (typically runtime dir); empty = token file not written
	SessionRuntimeDir    string                                               // Runtime dir searched for local agent sessions; empty = workspace/repo stores only
	LocalSettingsDir     string                                               // Desktop-local settings directory; empty disables /api/local/settings
	AgentControlFn       agentcontrol.AgentControlFn                          // Sends agent lifecycle commands to the daemon control socket; nil in fleet mode or --no-daemon
	DaemonSupervisorFn   func() (*DaemonSupervisorData, error)                // Returns daemon supervisor state from state file; nil = endpoint unavailable
	DaemonConfigFn       func() (json.RawMessage, error)                      // Returns effective merged daemon config as JSON; nil = endpoint unavailable
	AgentQueueFn         func(agentName string) ([]AgentQueueEntry, error)    // Returns scored work queue for named agent; nil = endpoint unavailable
	FleetMode            bool                                                 // When true, skip local daemon lifecycle hooks; fleet server manages agents
	FleetClientURL       string                                               // Fleet server URL for fleet-mode workers (e.g., "http://fleet.example.com"); empty = no fleet client
	FleetClientWorkspace string                                               // Explicit fleet server workspace ID; empty = unset.
	FleetClientAPIKey    string                                               // Pre-shared API key for fleet worker backend auth
	FleetClientActor     string                                               // X-Actor header value for fleet-db --auth-dev-mode (typically the loom agent name)
	FleetDBBaseURL       string                                               // fleet-db HTTP base URL backing Store; used by the driver-op API to build issue backends
	DriverAPIToken       string                                               // Optional shared bearer token required by the driver-op HTTP API (LOOM_DRIVER_API_TOKEN)
	DriverAPIBaseURL     string                                               // This serve process's own driver/task-run API base URL, required by task runners as LOOM_TASK_RUN_API_URL
	DriverRunTokenKey    []byte                                               // HS256 signing key for run-scoped driver-op tokens (LOOM_RUN_TOKEN_SIGNING_KEY or ephemeral); nil disables the token auth path
	ExecutionCapability  ExecutionCapability                                  // Active Execution APIs and typed authority resolvers; nil fails mutating execution routes closed
	ArtifactsCapability  ArtifactsCapability                                  // Active owner-fenced Artifact lifecycle; nil fails artifact mutations closed
	DaemonStartupFn      func(ctx context.Context, onReady func(wsID string)) // Starts daemons for secondary workspaces; calls onReady(wsID) when each is reachable
	Logger               *slog.Logger                                         // Structured logger (optional; nil falls back to slog.Default())
	SentryDSN            string                                               // Sentry/GlitchTip DSN for error tracking (optional; empty disables)
	// IssueBackendFn returns the active backend.IssueBackend used by the
	// webui issue service for the migrated CRUD operations (Get, Create,
	// Update/Patch, Close, Claim, Delete, AddComment, AddDependency,
	// RemoveDependency, ListEvents). When nil, the service falls back to
	// returning ErrUnavailable from those operations. Wired through
	// service.IssueBackendProvider; see service.NewIssueServiceWithBackend.
	//
	// Threaded as a closure rather than a backend.IssueBackend field so the
	// cli wiring can resolve the backend lazily without webui depending on
	// internal/cli (which would create an import cycle).
	//
	// The ctx carries the per-request workspace ID via middleware.WithWorkspace,
	// allowing the closure to construct a per-workspace fleet-db backend in
	// cloud mode. Local wirings return the process-global fleet-db backend.
	IssueBackendFn func(ctx context.Context) backend.IssueBackend
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

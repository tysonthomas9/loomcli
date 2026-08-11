package webui

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/fleet"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/workspacecoord"
)

const (
	DefaultPort            = 8080
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
	FrontendDir         string // Optional built SPA directory served for non-API routes
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
	WorkflowCatalogModule     interface{ Register(*http.ServeMux) }
	WorkflowCatalogAPI        WorkflowCatalogAPI
	WorkflowCatalogAuthoring  WorkflowCatalogVersionAuthoringAPI
	WorkflowCatalogOperator   WorkflowCatalogOperatorAuthorityResolver
	WorkflowTargetPreparation func(context.Context, string, string) (*WorkflowCatalogDriver, error)
	AutomationCapability      AutomationCapability
	AgentsCapability          AgentsCapability
	AgentProvisioning         AgentProvisioningCapability
	SourceControl             SourceControlMaterializer
	TaskStackBindings         SourceControlStackBindingResolver
	TaskOutcomes              SourceControlTaskOutcomeRecorder
	WorkspaceSourceControl    RepositoryAdmissionMaterializer
	WorkspaceCatalog          WorkspaceAPI
	InteractionCapability     InteractionCapability
	MonitorHandlers           MonitorHandlers                    // Pre-built handlers for monitor/metrics endpoints (injected by cli)
	GitOps                    ops.GitOps                         // Git operations interface (optional; nil disables git endpoints)
	FileOps                   ops.FileOps                        // File operations interface (optional; nil disables file endpoints)
	WorkspaceDeleteCleanupFn  func(key string) error             // Machine-local cleanup after an owner-command deletion.
	WorkspaceCreateFn         workspacecoord.WorkspaceCreateFn   // Workspace creation function; nil = creation unavailable
	WorkspaceAddReposFn       workspacecoord.WorkspaceAddReposFn // Attach local repos to an existing workspace; nil = unavailable
	WorkspaceAdmissions       workspacecoord.WorkspaceAdmissionCoordinator
	InitialWorkspaceID        string                // Stable key of the initial workspace
	WorkspaceIDResolverFn     WorkspaceIDResolverFn // Resolves workspace name → UUID; nil = no resolution available
	// Store is the transitional unified state store for workspace configuration
	// not yet migrated behind capability-owned APIs. Local and distributed modes
	// both use it as the authoritative source for those remaining records.
	Store              store.Store
	BackendOps         ops.BackendOps // Backend health operations interface (optional; nil disables backend health endpoint)
	ScrollbackMaxLines int            // Maximum lines per scrollback buffer (0 = default 10000)
	NotifyTokenDir     string         // Directory to write notify.token (typically runtime dir); empty = token file not written
	SessionRuntimeDir  string         // Runtime dir searched for local agent sessions; empty = workspace/repo stores only
	LocalSettingsDir   string         // Desktop-local settings directory; empty disables /api/local/settings
	FleetMode          bool           // When true, skip local daemon lifecycle hooks; fleet server manages agents
	FleetClientURL     string         // Fleet server URL for fleet-mode workers (e.g., "http://fleet.example.com"); empty = no fleet client
	FleetClientAPIKey  string         // Pre-shared API key for fleet worker backend auth
	FleetClientActor   string         // X-Actor header value for fleet-db --auth-dev-mode (typically the loom agent name)
	FleetDBBaseURL     string         // fleet-db HTTP base URL backing Store; used by the driver-op API to build issue backends
	// ExecutionIssueBackends builds the workspace- and actor-scoped FleetDB
	// clients used behind the run-token-authenticated DriverRun and TaskRun
	// facades. Embedded mode captures its process-local service credential in
	// this closure instead of exporting it through environment state.
	ExecutionIssueBackends func(workspace, actor string) (IssueBackend, error)
	DriverAPIBaseURL       string                // This serve process's own driver/task-run API base URL, required by task runners as LOOM_TASK_RUN_API_URL
	DriverRunTokenKey      []byte                // Required HS256 signing key for run-scoped driver-op tokens (LOOM_RUN_TOKEN_SIGNING_KEY or ephemeral)
	ExecutionCapability    ExecutionCapability   // Active Execution APIs and typed authority resolvers; nil fails mutating execution routes closed
	DaytonaProvider        DaytonaProviderBroker // Host-owned, owner-fenced Daytona provider operation; nil fails closed.
	ArtifactsCapability    ArtifactsCapability   // Active owner-fenced Artifact lifecycle; nil fails artifact mutations closed
	Logger                 *slog.Logger          // Structured logger (optional; nil falls back to slog.Default())
	SentryDSN              string                // Sentry/GlitchTip DSN for error tracking (optional; empty disables)
	// IssueBackendFn returns the active backend.IssueBackend wrapped at server
	// composition by the Work Items capability's narrow durable adapter. When
	// nil, Work Items HTTP and onboarding routes remain unavailable.
	//
	// Threaded as a closure rather than a backend.IssueBackend field so the
	// cli wiring can resolve the backend lazily without webui depending on
	// internal/cli (which would create an import cycle).
	//
	// The ctx carries the per-request workspace ID via middleware.WithWorkspace,
	// allowing the closure to construct a per-workspace fleet-db backend in
	// cloud mode. Local wirings return the process-global fleet-db backend.
	IssueBackendFn func(ctx context.Context) IssueBackend
}

// WorkspaceIDResolverFn resolves a workspace name to its stable UUID.
// Returns ("", error) if the workspace is not found or config cannot be loaded.
type WorkspaceIDResolverFn func(name string) (string, error)

// DefaultConfig returns a ServerConfig with sensible defaults.
func DefaultConfig() ServerConfig {
	return ServerConfig{
		Port:            DefaultPort,
		BindAddress:     "127.0.0.1",
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

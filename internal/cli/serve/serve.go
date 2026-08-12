package serve

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/serve/metricscmd"
	"github.com/tysonthomas9/loomcli/internal/cli/serve/opsimpl"
	"github.com/tysonthomas9/loomcli/internal/cli/serve/serveadapter"
	"github.com/tysonthomas9/loomcli/internal/cli/serve/workspacemgr"
	driverexecutor "github.com/tysonthomas9/loomcli/internal/driver"
	infrafleetdb "github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/infra/workspacecatalog"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui"
	webuiapp "github.com/tysonthomas9/loomcli/internal/webui/app"
)

// envLoomFleetMode is the env var that toggles --fleet-mode when no flag is
// passed. Intentionally separate from LOOM_ISSUE_BACKEND=fleet, which gates
// fleet-aware issue routing at a different layer.
const envLoomFleetMode = "LOOM_FLEET_MODE"
const envLoomDriverExecutor = "LOOM_DRIVER_EXECUTOR"
const envLoomDriverExecutorWorkspace = "LOOM_DRIVER_EXECUTOR_WORKSPACE"

// driverExecutorAllWorkspaces is the LOOM_DRIVER_EXECUTOR_WORKSPACE sentinel
// that unscopes the driver executor + task worker so they claim queued runs in
// EVERY workspace (Executor/TaskWorker treat an empty WorkspaceKey as "all
// workspaces", the same way the CronScheduler sweeps every workspace).
const driverExecutorAllWorkspaces = "*"
const envLoomDriverTaskWorkerConcurrency = "LOOM_DRIVER_TASK_WORKER_CONCURRENCY"
const envLoomDriverTaskRunMaxAttempts = "LOOM_DRIVER_TASK_RUN_MAX_ATTEMPTS"
const envLoomDriverStaleTaskMaxAge = "LOOM_DRIVER_STALE_TASK_MAX_AGE"
const envLoomAwaitSweepInterval = "LOOM_AWAIT_SWEEP_INTERVAL"
const envLoomAwaitSweepBatch = "LOOM_AWAIT_SWEEP_BATCH"
const envLoomIssueBridgeInterval = "LOOM_ISSUE_BRIDGE_INTERVAL"
const envLoomIssueBridgeDisabled = "LOOM_ISSUE_BRIDGE_DISABLED"
const envLoomIssueBridgeStatePath = "LOOM_ISSUE_BRIDGE_STATE_PATH"

const monitorCollectionCacheTTL = 10 * time.Second

const (
	envLocalRuntimeMode = "LOOM_LOCAL_RUNTIME"
	envDesktopDataDir   = "LOOM_DESKTOP_DATA_DIR"

	localRuntimeModeDesktop  = "desktop"
	localRuntimeModeHeadless = "headless"
)

var (
	servePort              int
	serveBindAddr          string
	serveCorsOrigin        string
	serveFrontendURLs      []string
	serveFrontendDir       string
	serveRedisAddr         string
	serveRedisPassword     string
	serveFleetMode         bool
	serveFleetAPIKey       string
	serveHSTS              bool
	serveAuthURL           string
	serveAuthIssuer        string
	serveAuthAudience      string
	serveAuthAllowInsecure bool
	serveSentryDSN         string

	// usageHandler holds the initialized usage HTTP handler.
	usageHandler http.HandlerFunc
)

// parseFrontendURLsEnv reads LOOM_FRONTEND_URL and returns a list of origins
// split on commas and whitespace-trimmed. Returns nil if the env var is unset
// or empty.
func parseFrontendURLsEnv() []string {
	raw := os.Getenv("LOOM_FRONTEND_URL")
	if raw == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start HTTP server for agent status API",
	Long: `Start a pure JSON API / SSE / WebSocket server.

The frontend is served externally (reverse proxy, CDN, Vite preview, etc.).
Use --frontend-url (repeatable) to allow cross-origin frontend deployments
via CORS.

Starts/opens fleet-db for issue and workspace data.

ENVIRONMENT VARIABLES
  LOOM_SERVER_PORT     Server port (default: 8080)
  LOOM_BIND_ADDR       Bind address (default: 127.0.0.1)
  LOOM_CORS_ORIGIN     CORS allowed origin
  LOOM_FRONTEND_URL    Allowed frontend origin(s) for CORS (comma-separated)
  LOOM_REDIS_PASSWORD  Redis password (avoids exposure in process list)
  LOOM_AUTH_URL         External auth service base URL (enables JWT auth)
  LOOM_AUTH_ISSUER      Expected JWT issuer (defaults to LOOM_AUTH_URL)
  LOOM_AUTH_AUDIENCE    Expected JWT audience (defaults to "loom")
  LOOM_DRIVER_EXECUTOR  DriverRun executor toggle (default: on; set 0/false/off/no to disable)
  LOOM_DRIVER_EXECUTOR_WORKSPACE  Scope for serve's driver-run automation loops (cron scheduler, outbox/delivery/stale/await sweepers, run executor + task worker): unset inherits LOOM_WORKSPACE; "*" spans every workspace; a name scopes to that workspace
  LOOM_DRIVER_TASK_WORKER_CONCURRENCY  Local TaskRun worker loops (default: 2)
  LOOM_DRIVER_TASK_RUN_MAX_ATTEMPTS     TaskRun attempts before blocking failed (default: 2)
  LOOM_ISSUE_BRIDGE_INTERVAL            Issue-journal bridge poll interval in seconds (default: 2)
  LOOM_ISSUE_BRIDGE_DISABLED            Disable the issue-journal bridge loop (set 1/true)
  LOOM_ISSUE_BRIDGE_STATE_PATH          Bridge cursor state file (default: <state dir>/issue-bridge-cursor.json)
  LOOM_ISSUE_BRIDGE_REPLAY              Replay journal from zero on first observation (set 1/true)
EXAMPLES
  loom serve                                              # Default port 8080
  loom serve --bind 0.0.0.0 --auth-url https://auth.co   # Exposed with JWT auth
  loom serve --frontend-url https://app.example.com       # Cross-origin frontend`,
	Args: cobra.NoArgs,
	Run:  runServe,
}

func init() {
	registerServeFlags()
	registerServeAuthFlags()
	cli.RegisterCommand(serveCmd)
}

// registerServeFlags binds the non-auth serve flags. Split out so init()
// stays under the funlen threshold; pure flag plumbing, no behavior.
func registerServeFlags() {
	defaultPort := 8080
	if envPort := os.Getenv("LOOM_SERVER_PORT"); envPort != "" {
		if p, err := strconv.Atoi(envPort); err == nil {
			defaultPort = p
		}
	}
	defaultBind := os.Getenv("LOOM_BIND_ADDR")
	if defaultBind == "" {
		defaultBind = "127.0.0.1"
	}

	serveCmd.Flags().IntVarP(&servePort, "port", "p", defaultPort, "Server port")
	serveCmd.Flags().StringVar(&serveBindAddr, "bind", defaultBind, "Bind address (use 0.0.0.0 for all interfaces)")
	serveCmd.Flags().StringVar(&serveCorsOrigin, "cors", os.Getenv("LOOM_CORS_ORIGIN"), "CORS allowed origin")
	serveCmd.Flags().StringSliceVar(&serveFrontendURLs, "frontend-url", parseFrontendURLsEnv(), "Allowed frontend origin(s) for CORS. Repeatable or comma-separated. Env: LOOM_FRONTEND_URL")
	serveCmd.Flags().StringVar(&serveFrontendDir, "frontend-dir", os.Getenv("LOOM_FRONTEND_DIR"), "Built web UI directory to serve for non-API routes. Env: LOOM_FRONTEND_DIR")
	serveCmd.Flags().StringVar(&serveRedisAddr, "redis-addr", os.Getenv("LOOM_REDIS_ADDR"), "Redis address for fleet coordination (enables stale detector)")
	serveCmd.Flags().StringVar(&serveRedisPassword, "redis-password", os.Getenv("LOOM_REDIS_PASSWORD"), "Redis password (prefer LOOM_REDIS_PASSWORD env var to avoid leaking in process list)")
	serveCmd.Flags().BoolVar(&serveFleetMode, "fleet-mode", os.Getenv(envLoomFleetMode) == "true", "Enable fleet coordination features (stale detector, task claims, fleet routes). Default off for local dev. Env: "+envLoomFleetMode)
	serveCmd.Flags().StringVar(&serveFleetAPIKey, "fleet-api-key", os.Getenv("LOOM_FLEET_API_KEY"), "API key for fleet worker registration (required for fleet register endpoint)")
	serveCmd.Flags().BoolVar(&serveHSTS, "hsts", false, "Enable HSTS header (use when behind TLS-terminating proxy)")
	serveCmd.Flags().StringVar(&serveSentryDSN, "sentry-dsn", os.Getenv("LOOM_SENTRY_DSN"), "Sentry/GlitchTip DSN for error tracking (or LOOM_SENTRY_DSN)")
}

// registerServeAuthFlags binds the JWT-auth-related serve flags.
func registerServeAuthFlags() {
	serveCmd.Flags().StringVar(&serveAuthURL, "auth-url", os.Getenv("LOOM_AUTH_URL"), "External auth service base URL (enables JWT auth)")
	serveCmd.Flags().StringVar(&serveAuthIssuer, "auth-issuer", os.Getenv("LOOM_AUTH_ISSUER"), "Expected JWT issuer (defaults to --auth-url)")
	serveCmd.Flags().StringVar(&serveAuthAudience, "auth-audience", os.Getenv("LOOM_AUTH_AUDIENCE"), "Expected JWT audience (defaults to \"loom\")")
	serveCmd.Flags().BoolVar(&serveAuthAllowInsecure, "auth-allow-insecure", false, "Allow HTTP for non-loopback --auth-url (INSECURE, for Docker internal networks only)")
}

//nolint:cyclop,funlen // Serve startup wires process-wide dependencies and shutdown branches in a fixed order.
func runServe(cmd *cobra.Command, args []string) {
	configureServeLocalRuntimeMode()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(stop)

	fleetState := resolveFleetState(ctx)

	// When no external Redis address is configured, run an in-process
	// miniredis so the terminal-state stores (tabmeta, issuetabs,
	// sessionhistory, terminal:ui-state) keep working. State is snapshotted
	// to ~/.loom/terminal-state/snapshot.json every 30s and on shutdown.
	if serveRedisAddr == "" {
		if mgr := startLocalRedis(ctx, serveFleetMode); mgr != nil {
			fleetState.redisConfig = &fleetRedisConfig{Address: mgr.Addr()}
		}
	} else {
		slog.Info("Redis: using external server", "addr", serveRedisAddr)
	}

	applyAuthDefaults()
	warnNonLocalBind()

	// Stale detector is only meaningful with fleet-mode AND a real multi-node
	// Redis (miniredis is single-node, so there are no peer servers to mark
	// stale). When either is missing, the /stale-detector endpoint returns 404.
	var staleDetectorHandler http.HandlerFunc
	if serveFleetMode && serveRedisAddr != "" {
		staleDetectorHandler = initStaleDetectorHandler(ctx, serveRedisAddr, serveRedisPassword)
	}
	initUsageStore()

	// Open a fleet-db-backed store handle for the default fleet-db path.
	storeHandle, storeErr := openServeStore(ctx, fleetState)
	if storeErr != nil {
		log.Fatalf("failed to open fleet-db store: %v", storeErr)
	}
	defer func() { _ = storeHandle.Close() }()
	issueBackendFn := cli.WorkspaceAwareIssueBackendForConfig(
		storeHandle.URL(),
		storeHandle.FleetDBClientAPIKey(),
		fleetState.clientCfg.Actor,
	)
	taskReadyCallbacks := buildTaskReadyBridgeCallbacks(
		storeHandle.Store.Repos(),
		issueBackendFn,
	)
	monitorDefaultWorkspace := resolveMonitorCollectorWorkspace(storeHandle.Store, fleetState.clientCfg.Workspace)
	collectDataFn := buildMonitorCollectDataFn(monitorDefaultWorkspace, issueBackendFn)
	monitorStoreDataSource := metricscmd.NewMonitorStoreDataSource(storeHandle.Store)
	monitorHandlers := buildMonitorHandlers(
		collectDataFn,
		staleDetectorHandler,
		storeHandle.Store,
		issueBackendFn,
		monitorDefaultWorkspace,
		monitorStoreDataSource,
	)
	cfg, capabilities, err := buildServerConfig(monitorHandlers, fleetState, storeHandle)
	if err != nil {
		log.Fatalf("failed to compose serve capabilities: %v", err)
	}
	// Backfill TS-contract prompt bodies only after the Agents capability has
	// been composed. Workspace management receives owner commands, never the
	// horizontal Role or Agent stores.
	if err := workspacemgr.EnsureBuiltinRolePrompts(
		ctx, storeHandle.Store, capabilities.agents,
	); err != nil {
		slog.Warn("builtin role prompt backfill failed", "err", err)
	}
	if cfg.AgentsCapability == nil || cfg.AgentsCapability.AgentsAPI() == nil {
		log.Fatal("failed to compose monitor: canonical Agents directory is required")
	}
	monitorStoreDataSource.SetAgentDirectory(cfg.AgentsCapability.AgentsAPI())
	if capabilities.interaction == nil ||
		capabilities.interaction.ChatMessenger() == nil {
		log.Fatal("failed to compose outbox dispatcher: Interaction chat commands are required")
	}
	runtimeConfig := buildServeRuntimeConfig()
	startOutboxDispatcher(
		ctx,
		storeHandle.Store,
		cfg.ExecutionCapability,
		capabilities.interaction.ChatMessenger(),
		runtimeConfig.WorkspaceScope,
	)
	stopRuntime, err := startServeRuntime(
		ctx,
		storeHandle,
		cfg,
		serveRuntimeCapabilities{
			WorkflowCatalog: capabilities.workflowCatalog,
			Automation:      capabilities.automation,
			Runtime:         capabilities.runtime,
		},
		taskReadyCallbacks,
		runtimeConfig,
	)
	if err != nil {
		log.Fatalf("failed to start platform runtime: %v", err)
	}

	webuiErr := make(chan error, 1)
	go func() {
		webuiErr <- webuiapp.StartServer(ctx, cfg)
	}()

	logServerStartup()
	serveErr := awaitShutdown(stop, webuiErr, cancel)
	runtimeStopContext, runtimeStopCancel := context.WithTimeout(
		context.Background(),
		10*time.Second,
	)
	if err := stopRuntime(runtimeStopContext); err != nil {
		slog.Error("platform runtime did not stop cleanly", "err", err)
	}
	runtimeStopCancel()
	if capabilities.workspaceAdmissions != nil {
		admissionStopContext, admissionStopCancel := context.WithTimeout(
			context.Background(),
			10*time.Second,
		)
		if err := capabilities.workspaceAdmissions.Stop(admissionStopContext); err != nil {
			slog.Error("workspace repository materializations did not stop cleanly", "err", err)
		}
		admissionStopCancel()
	}
	if serveErr != nil {
		cmd.PrintErrf("Server error: %v\n", serveErr)
		_ = storeHandle.Close()
		os.Exit(1)
	}
}

func configureServeLocalRuntimeMode() {
	if strings.TrimSpace(os.Getenv(envLocalRuntimeMode)) != "" {
		return
	}
	if strings.TrimSpace(os.Getenv(envDesktopDataDir)) != "" {
		_ = os.Setenv(envLocalRuntimeMode, localRuntimeModeDesktop)
		return
	}
	_ = os.Setenv(envLocalRuntimeMode, localRuntimeModeHeadless)
}

func buildMonitorCollectDataFn(workspaceHint string, issueBackendFn metricscmd.IssueBackendFn) metricscmd.CollectDataFn {
	// Refresh monitor data only when the UI asks for it. The frontend performs
	// one initial fetch and then uses workspace SSE mutations to trigger
	// additional refreshes, so an unconditional server-side warmer just creates
	// idle fleet-db fanout and OTEL spans.
	return buildCollectDataFn(workspaceHint, issueBackendFn, monitorCollectionCacheTTL)
}

func resolveMonitorCollectorWorkspace(st store.Store, fallbackWorkspace string) string {
	if workspace := os.Getenv(bootstrap.EnvWorkspace); workspace != "" {
		return workspace
	}
	if fallbackWorkspace != "" {
		return fallbackWorkspace
	}
	if st == nil {
		return ""
	}
	return serveadapter.ResolveInitialWorkspaceID(st)
}

func openServeStore(ctx context.Context, fs fleetState) (*bootstrap.StoreHandle, error) {
	if cli.IsFleetActive() {
		ensureFleetStoreEnv(fs.clientCfg)
	}
	required, err := serveadapter.RequiredFleetDBCapabilities(serveAuthURL != "", false)
	if err != nil {
		return nil, fmt.Errorf("derive required FleetDB capabilities: %w", err)
	}
	return cmdstore.OpenStoreWithCapabilities(ctx, required)
}

// buildDriverExecutor assembles serve's DriverRun executor, resolving the
// workflow sandbox launcher (SB2): LOOM_DRIVER_SANDBOX=container runs
// workflow bundles in rootless containers; the default stays the local
// node-process launcher. An invalid sandbox configuration disables the
// executor (fail closed) rather than silently degrading isolation.
func buildDriverExecutor(
	st store.Store,
	workDir string,
	runOutcomes driverexecutor.RunOutcomePublisher,
	executionCapability webui.ExecutionCapability,
	nodeCapacity int,
) (*driverexecutor.Executor, bool) {
	runTokenKey, err := driverRunTokenKey()
	if err != nil {
		slog.Error("driver executor disabled: invalid run-token signing key", "err", err)
		return nil, false
	}
	sandboxLauncher, err := driverexecutor.ResolveSandboxLauncher()
	if err != nil {
		slog.Error("driver executor disabled: invalid sandbox configuration", "err", err)
		return nil, false
	}
	sandboxMode := driverexecutor.SandboxModeProcess
	sandboxEgress := "host"
	if sandboxLauncher != nil {
		sandboxMode = driverexecutor.SandboxModeContainer
		// SB4: empty resolves per run trust level (trusted all, else serve-only).
		if sandboxEgress = os.Getenv(driverexecutor.SandboxEgressEnvVar); sandboxEgress == "" {
			sandboxEgress = "per-trust-default"
		}
	}
	executor := &driverexecutor.Executor{
		Store:           st,
		WorkspaceKey:    driverAutomationWorkspaceScope(),
		WorkDir:         workDir,
		NodeID:          os.Getenv("LOOM_DRIVER_EXECUTOR_NODE_ID"),
		NodeCapacity:    nodeCapacity,
		APIBaseURL:      driverAPIBaseURL(),
		RunTokenKey:     runTokenKey,
		SandboxLauncher: sandboxLauncher,
		RunOutcomes:     runOutcomes,
	}
	configureDriverExecutorCapability(executor, executionCapability)
	workspaceScope := executor.WorkspaceKey
	if workspaceScope == "" {
		workspaceScope = "*all*"
	}
	slog.Info("Driver executor enabled", "workspace", workspaceScope, "work_dir", workDir, "sandbox", sandboxMode,
		"sandbox_egress", sandboxEgress,
		"task_worker_concurrency", nodeCapacity, "task_run_max_attempts", driverTaskRunMaxAttempts())
	return executor, true
}

func configureDriverExecutorCapability(executor *driverexecutor.Executor, executionCapability webui.ExecutionCapability) {
	if executionCapability != nil {
		executor.Execution = executionCapability.DriverRunAPI()
		executor.RunOutcomeQueue = executionCapability.DriverRunOutcomeAPI()
		executor.TerminalWorkRecoveryQueue = executionCapability.TerminalDriverRunWorkRecoveryQueueAPI()
		executor.ExecutionWorkers = executionCapability.TaskRunWorkerAPI()
		executor.ExecutionAuthorities = executionCapability.DriverRunAuthorityResolver()
		executor.SystemAuthorities = executionCapability.SystemAuthorityResolver()
		executor.TaskRunRecovery = executionCapability.TaskRunRecoveryAPI()
		executor.StaleTaskRunMaxAge = driverStaleTaskMaxAge()
	}
}

// startDriverTaskWorkers launches the local TaskRun worker claim loops, one
// goroutine per concurrency slot, each with a distinct runner identity.
// driverAPIBaseURL is the loopback URL of this serve process's driver-op
// HTTP API, exported to driver runtimes as LOOM_DRIVER_API_URL. Driver
// runtimes are local children of the executor, so loopback is always
// reachable regardless of the public bind address. Set
// LOOM_DRIVER_API_URL on the serve process to override (e.g. TLS front).
func driverAPIBaseURL() string {
	if override := strings.TrimSpace(os.Getenv("LOOM_DRIVER_API_URL")); override != "" {
		return override
	}
	host := serveBindAddr
	switch host {
	case "", "0.0.0.0", "::":
		host = "127.0.0.1"
	case "::1":
		host = "[::1]"
	}
	return fmt.Sprintf("http://%s:%d", host, servePort)
}

// driverRunTokenKey resolves the HS256 signing key for run-scoped driver-op
// tokens (LOOM_RUN_TOKEN_SIGNING_KEY, with an ephemeral per-process fallback
// for single-instance deployments). Malformed configuration is returned to
// composition so serve fails before registering a tokenless Driver API.
func driverRunTokenKey() ([]byte, error) {
	return driverexecutor.ResolveRunTokenSigningKey()
}

// resolveDriverExecutorWorkspace resolves the workspace scope for the driver
// run executor + local task-worker claim loops. By default the executor
// inherits LOOM_WORKSPACE, which also drives issue routing and monitor
// collection — fine for a single-workspace process, but wrong for a serve
// process expected to service every workspace the CronScheduler sweeps. The
// scheduler is always unscoped, so a LOOM_WORKSPACE-scoped executor leaves runs
// it fired in every OTHER workspace queued forever. LOOM_DRIVER_EXECUTOR_WORKSPACE
// decouples the executor's claim scope from LOOM_WORKSPACE:
//
//   - unset / empty  → inherit LOOM_WORKSPACE (back-compat, single-workspace)
//   - "*" (sentinel) → unscoped: claim queued runs in EVERY workspace
//     (Executor.nextQueuedRun / TaskWorker.RunOnce treat an empty WorkspaceKey
//     as "all workspaces", mirroring recoverStale + the CronScheduler)
//   - any other name → scope the executor to exactly that workspace
func resolveDriverExecutorWorkspace(override, inherited string) string {
	override = strings.TrimSpace(override)
	if override == "" {
		return strings.TrimSpace(inherited)
	}
	if override == driverExecutorAllWorkspaces {
		return ""
	}
	return override
}

// driverAutomationWorkspaceScope is the single resolved workspace scope shared
// by serve's driver-run automation loops: the cron scheduler (fires ticks →
// runs), the outbox + trigger-delivery sweepers (move ticks → runs and retry),
// the stale-task + await-timeout sweepers (recover those runs), the run
// executor + task worker (claim + execute), and the issue-journal bridge (feeds
// task.ready events that fire prompt-agent bindings). They MUST agree: a
// scheduler or bridge that produces runs in a workspace the executor won't claim
// leaves them queued forever (the original SANDBOX-vs-LOCALMODE bug). The bridge
// was previously left on LOOM_WORKSPACE as issue-plane ingestion; now that its
// task.ready lane drives the same driver-run automation loop, it joins the shared
// scope for parity.
func driverAutomationWorkspaceScope() string {
	return resolveDriverExecutorWorkspace(os.Getenv(envLoomDriverExecutorWorkspace), os.Getenv(bootstrap.EnvWorkspace))
}

func driverExecutorEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(envLoomDriverExecutor))) {
	case "0", "false", "off", "no":
		return false
	default:
		return true
	}
}

func driverTaskWorkerConcurrency() int {
	return boundedIntEnv(envLoomDriverTaskWorkerConcurrency, 2, 32)
}

func driverTaskRunMaxAttempts() int {
	return boundedIntEnv(envLoomDriverTaskRunMaxAttempts, 2, 10)
}

// boundedIntEnv reads an integer env var, falling back to def when unset or
// unparseable and clamping the result to [1, max].
func boundedIntEnv(name string, def, max int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	if n < 1 {
		return 1
	}
	if n > max {
		return max
	}
	return n
}

func ensureFleetStoreEnv(cfg config.FleetClientConfig) {
	if os.Getenv(bootstrap.EnvFleetDBURL) == "" && cfg.URL != "" {
		_ = os.Setenv(bootstrap.EnvFleetDBURL, cfg.URL)
	}
	if os.Getenv(bootstrap.EnvFleetDBActor) == "" && cfg.Actor != "" {
		_ = os.Setenv(bootstrap.EnvFleetDBActor, cfg.Actor)
	}
}

// fleetState bundles fleet-related configuration resolved during startup.
type fleetState struct {
	modeDetected bool
	clientCfg    config.FleetClientConfig
	jwtKey       []byte
	redisConfig  *fleetRedisConfig
}

func resolveFleetState(ctx context.Context) fleetState {
	fs := fleetState{}
	if dc, dcErr := config.LoadRuntimeConfig(ctx, "."); dcErr == nil {
		fs.modeDetected = cli.IsFleetMode(dc)
	} else {
		fs.modeDetected = cli.IsFleetModeFromEnv()
	}
	fs.clientCfg = config.ResolveFleetConfig()
	if fs.clientCfg.URL == "" {
		fs.clientCfg.URL = os.Getenv(bootstrap.EnvFleetDBURL)
	}
	if fs.clientCfg.Actor == "" {
		fs.clientCfg.Actor = resolveFleetClientActorFallback()
	}

	fs.jwtKey, fs.redisConfig = resolveFleetJWTKey(ctx, serveRedisAddr, serveRedisPassword)
	return fs
}

func resolveFleetClientActorFallback() string {
	if v := os.Getenv(bootstrap.EnvFleetDBActor); v != "" {
		return v
	}
	if v := os.Getenv(bootstrap.EnvAgentName); v != "" {
		return v
	}
	return os.Getenv("USER")
}

func applyAuthDefaults() {
	if serveAuthURL == "" {
		return
	}
	validateAuthURL(serveAuthURL, serveAuthAllowInsecure)
	if serveAuthIssuer == "" {
		serveAuthIssuer = serveAuthURL
	}
	if serveAuthAudience == "" {
		serveAuthAudience = "loom"
	}
}

func warnNonLocalBind() {
	if serveBindAddr != "127.0.0.1" && serveBindAddr != "::1" {
		log.Printf("WARNING: Server bound to %s — exposed to network. Ensure this is intentional.", serveBindAddr)
	}
}

func initUsageStore() {
	usageHandler = buildUsageHandler(cli.GetWorkspaceRuntimeDir())
}

func buildMonitorHandlers(
	collectDataFn metricscmd.CollectDataFn,
	staleDetectorHandler http.HandlerFunc,
	st store.Store,
	issueBackendFn metricscmd.IssueBackendFn,
	defaultWorkspace string,
	monitorStoreDataSource *metricscmd.MonitorStoreDataSource,
) webui.MonitorHandlers {
	return composeMonitorHandlers(
		collectDataFn, staleDetectorHandler, issueBackendFn, defaultWorkspace, usageHandler,
		monitorStoreDataSource, metricscmd.HandleWorkspaces(st), st.DriverRuns(),
	)
}

// automationWebCapabilityView deliberately narrows the concrete serve
// capability before it crosses into the web application. Embedding the web
// interface (rather than the concrete pointer) means its promoted method set
// cannot be type-asserted back to issue-journal, run-outcome, or runtime ports.
type automationWebCapabilityView struct{ webui.AutomationCapability }

func applyStoreHandleServerConfig(
	cfg *webui.ServerConfig,
	fs fleetState,
	storeHandle *bootstrap.StoreHandle,
	gitOps *opsimpl.GitOpsImpl,
) (fleetState, error) {
	cfg.Store = storeHandle.Store
	gitOps.WithStore(storeHandle.Store)
	if url := storeHandle.URL(); url != "" {
		fleetAPIKey := storeHandle.FleetDBClientAPIKey()
		cfg.IssueBackendFn = cli.WorkspaceAwareIssueBackendForConfig(
			url,
			fleetAPIKey,
			fs.clientCfg.Actor,
		)
		cfg.FleetDBBaseURL = url
		fs = withStoreFleetConfig(fs, url, fleetAPIKey)
	}
	cfg.DriverAPIBaseURL = driverAPIBaseURL()
	runTokenKey, err := driverRunTokenKey()
	if err != nil {
		return fs, fmt.Errorf("resolve Driver API run-token signing key: %w", err)
	}
	cfg.DriverRunTokenKey = runTokenKey
	return fs, nil
}

type serveCapabilitySet struct {
	workflowCatalog     *serveadapter.WorkflowCatalogModule
	automation          *serveadapter.AutomationCapability
	agents              *serveadapter.AgentsCapability
	interaction         *serveadapter.InteractionCapability
	workspaceAdmissions *workspacemgr.StoreBackedWorkspaceAdmissionOperations
	runtime             []serveadapter.RuntimeContributor
}

//nolint:funlen // Serve composition assembles the complete dependency graph without moving product policy into the application host.
func buildServerConfig(
	monitorHandlers webui.MonitorHandlers,
	fs fleetState,
	storeHandle *bootstrap.StoreHandle,
) (webui.ServerConfig, serveCapabilitySet, error) {
	gitOps := opsimpl.NewGitOps()
	resolvedBackend := cli.ResolveBackendName()
	log.Printf("Terminal backend: %s", resolvedBackend)

	cfg := buildCoreServerConfig(monitorHandlers, gitOps, resolvedBackend)
	cfg.DaytonaProvider = serveadapter.NewDaytonaProviderBroker(cfg.LocalSettingsDir)
	if storeHandle != nil {
		var err error
		fs, err = applyStoreHandleServerConfig(&cfg, fs, storeHandle, gitOps)
		if err != nil {
			return webui.ServerConfig{}, serveCapabilitySet{}, err
		}
	}
	if cfg.Store != nil {
		workspaceCapability, workspaceErr := workspacecatalog.New(cfg.Store.Workspaces(), cfg.Store.Repos())
		if workspaceErr != nil {
			return webui.ServerConfig{}, serveCapabilitySet{}, fmt.Errorf("compose Workspace capability: %w", workspaceErr)
		}
		cfg.WorkspaceCatalog = workspaceCapability
	}
	applyFleetConfig(&cfg, fs)
	module, err := buildServeWorkflowCatalogModule(cfg, storeHandle)
	if err != nil {
		return webui.ServerConfig{}, serveCapabilitySet{}, err
	}
	capabilities := serveCapabilitySet{}
	var automationCapability *serveadapter.AutomationCapability
	var repositoryAdmissionJournal *workspacemgr.RepositoryAdmissionJournal
	if module != nil {
		capabilities.workflowCatalog = module
		cfg.WorkflowCatalogModule = module
		cfg.WorkflowCatalogAPI = module.CatalogAPI()
		cfg.WorkflowCatalogAuthoring = module.VersionAuthoringAPI()
		cfg.WorkflowCatalogOperator = module.OperatorAuthorityResolver()
		cfg.WorkflowTargetPreparation = module.PrepareWorkflowTarget
		automationCapability = module.AutomationCapability()
		if automationCapability != nil {
			cfg.AutomationCapability = automationWebCapabilityView{AutomationCapability: automationCapability}
			capabilities.automation = automationCapability
			capabilities.runtime = append(capabilities.runtime, automationCapability)
		}
	}
	if storeHandle != nil {
		var journalErr error
		repositoryAdmissionJournal, journalErr = workspacemgr.NewRepositoryAdmissionJournal()
		if journalErr != nil {
			return webui.ServerConfig{}, serveCapabilitySet{}, fmt.Errorf(
				"compose repository admission journal: %w",
				journalErr,
			)
		}
		agentsCapability, agentsErr := serveadapter.BuildAgentsCapability(serveadapter.AgentsConfig{
			StoreHandle: storeHandle, ExternalAuth: cfg.ExtAuthURL != "",
			WorkflowCatalogModule: module, LocalSettingsDir: cfg.LocalSettingsDir,
			Workspace:             driverAutomationWorkspaceScope(),
			WorkspaceRoleResolver: cfg.WorkspaceRoleResolver,
			RepositoryAdmissions:  repositoryAdmissionJournal,
		})
		if agentsErr != nil {
			return webui.ServerConfig{}, serveCapabilitySet{}, agentsErr
		}
		cfg.AgentsCapability = agentsCapability
		capabilities.agents = agentsCapability
		gitOps.WithAgentQueries(agentsCapability.AgentsAPI())
		cfg.SourceControl = agentsCapability.SourceControlMaterializer()
		cfg.TaskStackBindings = agentsCapability.TaskStackBindings()
		cfg.TaskOutcomes = agentsCapability.TaskOutcomes()
		cfg.WorkspaceSourceControl = agentsCapability.RepositoryAdmissionMaterializer()
		if agentsCapability.AgentProvisioningCommands() != nil {
			cfg.AgentProvisioning = agentsCapability
			capabilities.runtime = append(capabilities.runtime, agentsCapability)
		}
		interactionCapability, interactionErr := serveadapter.BuildInteractionCapability(
			serveadapter.InteractionConfig{
				StoreHandle: storeHandle, WorkflowCatalogModule: module,
				AgentQueries:          agentsCapability.AgentsAPI(),
				WorkspaceLister:       agentsCapability.WorkspaceLister(),
				Workspace:             driverAutomationWorkspaceScope(),
				ExternalAuth:          cfg.ExtAuthURL != "",
				WorkspaceRoleResolver: cfg.WorkspaceRoleResolver,
			},
		)
		if interactionErr != nil {
			return webui.ServerConfig{}, serveCapabilitySet{}, interactionErr
		}
		cfg.InteractionCapability = interactionCapability
		capabilities.interaction = interactionCapability
		capabilities.runtime = append(capabilities.runtime, interactionCapability)
		executionCapability, artifactsCapability, capabilityErr := serveadapter.BuildExecutionAndArtifactsCapabilities(
			module,
			storeHandle,
			agentsCapability.AgentsAPI(),
		)
		if capabilityErr != nil {
			return webui.ServerConfig{}, serveCapabilitySet{}, capabilityErr
		}
		cfg.ExecutionCapability = executionCapability
		cfg.ArtifactsCapability = artifactsCapability
	}
	// Workspace clone/add-repo wiring is intentionally last: its only remote
	// checkout authority is the Source Control materializer composed above.
	// Store-backed operation remains available for local worktree attachment,
	// while remote clone requests fail closed if capability composition did not
	// provide this owner port.
	workspaceAdmissions := applyWorkspaceConfigWithAdmission(
		&cfg,
		storeHandle,
		repositoryAdmissionJournal,
		capabilities.agents,
	)
	if workspaceAdmissions != nil {
		capabilities.workspaceAdmissions = workspaceAdmissions
		capabilities.runtime = append(capabilities.runtime, workspaceAdmissions)
	}
	applyCORSConfig(&cfg)
	return cfg, capabilities, nil
}

func buildServeWorkflowCatalogModule(
	cfg webui.ServerConfig,
	storeHandle *bootstrap.StoreHandle,
) (*serveadapter.WorkflowCatalogModule, error) {
	enabled, err := serveadapter.WorkflowCatalogEnabled(cfg.ExtAuthURL != "", cfg.WorkspaceRoleResolver != nil)
	if err != nil {
		return nil, fmt.Errorf("configure Workflow Catalog: %w", err)
	}
	automationEnabled, err := serveadapter.AutomationEnabled(cfg.ExtAuthURL != "", cfg.WorkspaceRoleResolver != nil)
	if err != nil {
		return nil, fmt.Errorf("configure Automation: %w", err)
	}
	return serveadapter.BuildWorkflowCatalogModule(serveadapter.WorkflowCatalogConfig{
		Enabled:               enabled,
		AutomationEnabled:     automationEnabled,
		StoreHandle:           storeHandle,
		Workspace:             driverAutomationWorkspaceScope(),
		ExternalAuth:          cfg.ExtAuthURL != "",
		WorkspaceRoleResolver: cfg.WorkspaceRoleResolver,
	})
}

func buildCoreServerConfig(monitorHandlers webui.MonitorHandlers, gitOps *opsimpl.GitOpsImpl, backend string) webui.ServerConfig {
	return webui.ServerConfig{
		Port:                 servePort,
		BindAddress:          serveBindAddr,
		FrontendDir:          serveFrontendDir,
		MonitorHandlers:      monitorHandlers,
		TerminalCmd:          fmt.Sprintf("loom lead --backend %s", backend),
		HSTSEnabled:          serveHSTS,
		ExtAuthURL:           serveAuthURL,
		ExtAuthIssuer:        serveAuthIssuer,
		ExtAuthAudience:      serveAuthAudience,
		ExtAuthAllowInsecure: serveAuthAllowInsecure,
		GitOps:               gitOps,
		FileOps:              gitOps,
		BackendOps:           opsimpl.NewBackendOps(),
		NotifyTokenDir:       cli.GetWorkspaceRuntimeDir(),
		SessionRuntimeDir:    cli.GetWorkspaceRuntimeDir(),
		LocalSettingsDir:     bootstrap.LoomDir(),
		Logger:               slog.Default(),
		SentryDSN:            serveSentryDSN,
		// Wire the active IssueBackend (fleet / fleet-db / api) into
		// the webui service layer so the migrated CRUD endpoints don't
		// hardcode a retired transport path. The closure lets the backend resolve
		// lazily — important because in fleet mode the backend is created on
		// first call, after the serve command has finished its early-startup
		// configuration.
		//
		// Cloud mode (LOOM_FLEET_DB_URL set) takes the workspace ID from the
		// request context and constructs a per-workspace fleet-db backend so
		// /api/workspaces/{ws}/issues stays scoped. Local mode uses the
		// process-global fleet-db backend.
		IssueBackendFn: cli.WorkspaceAwareIssueBackend(),
	}
}

func withStoreFleetConfig(fs fleetState, storeURL, storeAPIKey string) fleetState {
	if fs.clientCfg.URL == "" {
		fs.clientCfg.URL = storeURL
		fs.clientCfg.APIKey = storeAPIKey
	} else if strings.TrimRight(fs.clientCfg.URL, "/") == strings.TrimRight(storeURL, "/") && fs.clientCfg.APIKey == "" {
		// The explicit Fleet URL targets the same Store opened by bootstrap.
		// Reuse that Store's in-memory credential, but never attach a local
		// credential to a different explicitly configured external URL.
		fs.clientCfg.APIKey = storeAPIKey
	}
	return fs
}

func applyFleetConfig(cfg *webui.ServerConfig, fs fleetState) {
	cfg.FleetEnabled = serveFleetMode
	cfg.FleetRedis = fs.redisConfig
	cfg.FleetJWTKey = fs.jwtKey
	cfg.FleetAPIKey = serveFleetAPIKey
	cfg.FleetMode = fs.modeDetected
	cfg.FleetClientURL = fs.clientCfg.URL
	cfg.FleetClientAPIKey = fs.clientCfg.APIKey
	cfg.FleetClientActor = fs.clientCfg.Actor
	// Store-backed serve uses FleetDB directly, either embedded local or
	// external cloud. In both shapes there is no local issue daemon for the
	// web UI to probe.
	cfg.FleetClient = fs.modeDetected || cfg.Store != nil
}

// applyWorkspaceConfig wires store-backed workspace operations into the webui
// server. Nil-store serve leaves workspace management unavailable.
func applyWorkspaceConfig(
	cfg *webui.ServerConfig,
	agentsCommands workspacemgr.ManagedAgentsCommands,
) {
	_ = applyWorkspaceConfigWithAdmission(cfg, nil, nil, agentsCommands)
}

func applyWorkspaceConfigWithAdmission(
	cfg *webui.ServerConfig,
	storeHandle *bootstrap.StoreHandle,
	journal *workspacemgr.RepositoryAdmissionJournal,
	agentsCommands workspacemgr.ManagedAgentsCommands,
) *workspacemgr.StoreBackedWorkspaceAdmissionOperations {
	if cfg.Store == nil {
		return nil
	}
	cfg.WorkspaceIDResolverFn = serveadapter.BuildWorkspaceIDResolverFn(cfg.Store)
	cfg.InitialWorkspaceID = serveadapter.ResolveInitialWorkspaceID(cfg.Store)
	cfg.WorkspaceDeleteCleanupFn = serveadapter.BuildWorkspaceDeleteCleanupFn()
	var admissions infrafleetdb.RepositoryAdmissionTransport
	if storeHandle != nil && storeHandle.FleetDBClient() != nil {
		admissions = storeHandle.FleetDBClient().RepositoryAdmissions()
	}
	operations := workspacemgr.NewStoreBackedWorkspaceAdmissionOperations(
		cfg.WorkspaceCatalog,
		agentsCommands,
		admissions,
		journal,
		cfg.WorkspaceSourceControl,
	)
	if operations == nil {
		return nil
	}
	cfg.WorkspaceCreateFn = operations.CreateWorkspace
	cfg.WorkspaceAddReposFn = operations.AddWorkspaceRepos
	if len(operations.RuntimeRegistrations()) > 0 {
		cfg.WorkspaceAdmissions = operations
	}
	if cfg.WorkspaceAdmissions == nil {
		return nil
	}
	return operations
}

func applyCORSConfig(cfg *webui.ServerConfig) {
	origins := cfg.CORSOrigins
	frontendOrigins := cfg.FrontendOrigins
	if serveCorsOrigin != "" {
		origins = append(origins, serveCorsOrigin)
	}
	for _, u := range serveFrontendURLs {
		u = strings.TrimSpace(u)
		// Strip a single trailing slash — origins are scheme+host+port only,
		// so only the root `/` suffix is normalized. Paths or repeated slashes
		// stay intact and, if present, simply never match an Origin header.
		u = strings.TrimSuffix(u, "/")
		if u != "" {
			origins = append(origins, u)
			frontendOrigins = append(frontendOrigins, u)
		}
	}
	cfg.FrontendOrigins = frontendOrigins
	if len(origins) > 0 {
		cfg.CORSEnabled = true
		cfg.CORSOrigins = origins
	}
}

func logServerStartup() {
	log.Printf("Server starting on %s:%d", serveBindAddr, servePort)
	if serveCorsOrigin != "" {
		log.Printf("CORS enabled for origin: %s", serveCorsOrigin)
	}
	if len(serveFrontendURLs) > 0 {
		log.Printf("Frontend URLs (CORS): %v", serveFrontendURLs)
	}
}

func awaitShutdown(stop chan os.Signal, webuiErr chan error, cancel context.CancelFunc) error {
	select {
	case <-stop:
		log.Println("Shutting down server...")
	case err := <-webuiErr:
		cancel()
		return err
	}

	cancel()

	select {
	case err := <-webuiErr:
		return err
	case <-time.After(10 * time.Second):
		log.Printf("Warning: server did not shut down within timeout")
		return nil
	}
}

// backendProvider maps backend names to their provider labels.

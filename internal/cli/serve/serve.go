package serve

import (
	"context"
	"errors"
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
	"github.com/tysonthomas9/loomcli/internal/cli/monitor"
	"github.com/tysonthomas9/loomcli/internal/cli/serve/daemonwire"
	"github.com/tysonthomas9/loomcli/internal/cli/serve/metricscmd"
	"github.com/tysonthomas9/loomcli/internal/cli/serve/observability"
	"github.com/tysonthomas9/loomcli/internal/cli/serve/opsimpl"
	"github.com/tysonthomas9/loomcli/internal/cli/serve/placementwire"
	"github.com/tysonthomas9/loomcli/internal/cli/serve/serveadapter"
	"github.com/tysonthomas9/loomcli/internal/cli/serve/usagecmd"
	"github.com/tysonthomas9/loomcli/internal/cli/serve/workspacemgr"
	driverexecutor "github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/placement"
	"github.com/tysonthomas9/loomcli/internal/placement/daytona"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui"
	webuiapp "github.com/tysonthomas9/loomcli/internal/webui/app"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// envLoomFleetMode is the env var that toggles --fleet-mode when no flag is
// passed. Intentionally separate from LOOM_ISSUE_BACKEND=fleet, which gates
// fleet-aware issue routing at a different layer.
const envLoomFleetMode = "LOOM_FLEET_MODE"
const envLoomDriverExecutor = "LOOM_DRIVER_EXECUTOR"
const envLoomDriverTaskWorkerConcurrency = "LOOM_DRIVER_TASK_WORKER_CONCURRENCY"
const envLoomDriverTaskRunMaxAttempts = "LOOM_DRIVER_TASK_RUN_MAX_ATTEMPTS"
const envLoomDriverStaleTaskMaxAge = "LOOM_DRIVER_STALE_TASK_MAX_AGE"
const envLoomTriggerCronInterval = "LOOM_TRIGGER_CRON_INTERVAL"
const envLoomIssueBridgeInterval = "LOOM_ISSUE_BRIDGE_INTERVAL"
const envLoomIssueBridgeDisabled = "LOOM_ISSUE_BRIDGE_DISABLED"
const envLoomIssueBridgeStatePath = "LOOM_ISSUE_BRIDGE_STATE_PATH"
const envLoomPlacementReaperInterval = "LOOM_PLACEMENT_REAPER_INTERVAL"
const envLoomPlacementReaperEnforce = "LOOM_PLACEMENT_REAPER_ENFORCE"
const envLoomLeadMaxVCPU = "LOOM_LEAD_MAX_VCPU"
const envLoomLeadLostReleaseGrace = "LOOM_LEAD_LOST_RELEASE_GRACE"
const envLoomLeadMaxMemGiB = "LOOM_LEAD_MAX_MEM_GIB"
const envLoomLeadAllowlist = "LOOM_LEAD_ALLOWLIST"
const envLoomLeadAPIBaseURL = "LOOM_LEAD_API_BASE_URL"
const envLoomLeadDataAllowOpenAuth = "LOOM_LEAD_DATA_ALLOW_OPEN_AUTH"
const envLoomLeadSnapshot = "LOOM_LEAD_SNAPSHOT"
const envLoomLeadBootstrap = "LOOM_LEAD_BOOTSTRAP"

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
	serveWebUISocket       string
	serveNoDaemon          bool
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

Starts/opens fleet-db for issue and workspace data unless --no-daemon is set.

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
  LOOM_DRIVER_TASK_WORKER_CONCURRENCY  Local TaskRun worker loops (default: 2)
  LOOM_DRIVER_TASK_RUN_MAX_ATTEMPTS     TaskRun attempts before blocking failed (default: 2)
  LOOM_TRIGGER_CRON_INTERVAL            Cron trigger sweep interval in seconds (default: 30)
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
	serveCmd.Flags().StringVar(&serveWebUISocket, "webui-socket", "", "Daemon socket path for webui (auto-detect if empty)")
	serveCmd.Flags().BoolVar(&serveNoDaemon, "no-daemon", false, "Skip issue backend startup")
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

//nolint:funlen // Serve startup wires process-wide dependencies in a fixed order.
func runServe(cmd *cobra.Command, args []string) {
	configureServeLocalRuntimeMode()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	daemonWeStarted := ensureIssueBackend()
	if daemonWeStarted {
		defer stopIssueBackend()
	}

	fleetState := resolveFleetState(ctx)

	// When no external Redis address is configured, run an in-process
	// miniredis so the terminal-state stores (tabmeta, issuetabs,
	// sessionhistory, terminal:ui-state) keep working. State is snapshotted
	// to ~/.loom/terminal-state/snapshot.json every 30s and on shutdown.
	if serveRedisAddr == "" {
		if mgr := daemonwire.StartLocalRedis(ctx, serveFleetMode); mgr != nil {
			fleetState.redisConfig = &daemonwire.FleetRedisConfig{Address: mgr.Addr()}
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
		staleDetectorHandler = daemonwire.InitStaleDetectorHandler(ctx, serveRedisAddr, serveRedisPassword)
	}
	initUsageStore()

	// Open a fleet-db-backed store handle for the default fleet-db path.
	storeHandle, storeErr := openServeStore(ctx, fleetState)
	if storeErr != nil {
		log.Fatalf("failed to open fleet-db store: %v", storeErr)
	}
	defer func() { _ = storeHandle.Close() }()
	startDriverExecutorIfEnabled(ctx, storeHandle.Store)
	startStaleTaskSweeper(ctx, storeHandle.Store)
	startOutboxDispatcher(ctx, storeHandle.Store)
	startTriggerCronScheduler(ctx, storeHandle.Store)
	startTriggerDeliverySweeper(ctx, storeHandle.Store)
	startAwaitTimeoutSweeper(ctx, storeHandle.Store)
	startIssueJournalBridge(ctx, storeHandle.Store)
	// The reaper needs the same broker that will drive provisioning (5c-4b).
	// buildPlacementBroker returns nil unless Daytona creds + a deployment id
	// + an occupant-token key are all configured, so this is a no-op on
	// deployments that do not place leads in sandboxes.
	placementBroker, placementProviders := placementwire.Build(storeHandle.Store, driverRunTokenKey())
	startPlacementReaper(ctx, placementBroker, placementReaperInterval(), placementReaperEnforce())

	issueBackendFn := cli.WorkspaceAwareIssueBackendForURL(storeHandle.URL(), fleetState.clientCfg.Actor)
	monitorDefaultWorkspace := resolveMonitorCollectorWorkspace(storeHandle.Store, fleetState.clientCfg.Workspace)
	collectDataFn := buildMonitorCollectDataFn(monitorDefaultWorkspace, issueBackendFn)
	monitorHandlers := buildMonitorHandlers(collectDataFn, staleDetectorHandler, storeHandle.Store, issueBackendFn, monitorDefaultWorkspace)

	webuiErr := make(chan error, 1)
	go func() {
		cfg := buildServerConfig(monitorHandlers, fleetState, storeHandle, placementBroker, placementProviders)
		webuiErr <- webuiapp.StartServer(ctx, cfg)
	}()

	logServerStartup()
	awaitShutdown(cmd, stop, webuiErr, cancel)
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
	collectFn := func() *monitor.MonitorData {
		if workspaceHint != "" && issueBackendFn != nil {
			ctx := middleware.WithWorkspace(context.Background(), workspaceHint)
			if be := issueBackendFn(ctx); be != nil {
				return monitor.CollectMonitorDataWithIssueBackend(be, 10000, "")
			}
		}
		return monitor.CollectMonitorData(10000, "")
	}
	// Refresh monitor data only when the UI asks for it. The frontend performs
	// one initial fetch and then uses workspace SSE mutations to trigger
	// additional refreshes, so an unconditional server-side warmer just creates
	// idle fleet-db fanout and OTEL spans.
	return metricscmd.NewCollectorFunc(monitorCollectionCacheTTL, collectFn)
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
	return cmdstore.OpenStore(ctx)
}

func startDriverExecutorIfEnabled(ctx context.Context, st store.Store) {
	if !driverExecutorEnabled() || st == nil {
		return
	}
	workDir, err := os.Getwd()
	if err != nil {
		slog.Error("driver executor disabled: cannot resolve work dir", "err", err)
		return
	}
	executor, ok := buildDriverExecutor(st, workDir)
	if !ok {
		return
	}
	taskWorkerConcurrency := driverTaskWorkerConcurrency()
	taskRunMaxAttempts := driverTaskRunMaxAttempts()
	taskWorker := &driverexecutor.TaskWorker{
		Store:            st,
		WorkspaceKey:     executor.WorkspaceKey,
		WorkDir:          workDir,
		NodeID:           executor.NodeID,
		RunnerID:         os.Getenv("LOOM_DRIVER_TASK_WORKER_RUNNER_ID"),
		MaxAttempts:      taskRunMaxAttempts,
		APIBaseURL:       driverAPIBaseURL(),
		LocalSettingsDir: bootstrap.LoomDir(),
	}
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			if recovered, err := executor.RecoverStaleOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
				slog.Error("driver executor stale recovery failed", "err", err)
			} else if recovered != nil && recovered.Recovered > 0 {
				slog.Info("driver executor recovered stale driver runs", "count", recovered.Recovered)
			}
			_, err := executor.RunOnce(ctx)
			if err != nil && !errors.Is(err, driverexecutor.ErrNoQueuedRun) && !errors.Is(err, context.Canceled) {
				slog.Error("driver executor run failed", "err", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	startDriverTaskWorkers(ctx, taskWorker, taskWorkerConcurrency)
}

// buildDriverExecutor assembles serve's DriverRun executor, resolving the
// workflow sandbox launcher (SB2): LOOM_DRIVER_SANDBOX=container runs
// workflow bundles in rootless containers; the default stays the local
// node-process launcher. An invalid sandbox configuration disables the
// executor (fail closed) rather than silently degrading isolation.
func buildDriverExecutor(st store.Store, workDir string) (*driverexecutor.Executor, bool) {
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
		WorkspaceKey:    os.Getenv(bootstrap.EnvWorkspace),
		WorkDir:         workDir,
		NodeID:          os.Getenv("LOOM_DRIVER_EXECUTOR_NODE_ID"),
		APIBaseURL:      driverAPIBaseURL(),
		APIToken:        driverAPIToken(),
		RunTokenKey:     driverRunTokenKey(),
		SandboxLauncher: sandboxLauncher,
	}
	slog.Info("Driver executor enabled", "workspace", executor.WorkspaceKey, "work_dir", workDir, "sandbox", sandboxMode,
		"sandbox_egress", sandboxEgress,
		"task_worker_concurrency", driverTaskWorkerConcurrency(), "task_run_max_attempts", driverTaskRunMaxAttempts())
	return executor, true
}

// startDriverTaskWorkers launches the local TaskRun worker claim loops, one
// goroutine per concurrency slot, each with a distinct runner identity.
func startDriverTaskWorkers(ctx context.Context, template *driverexecutor.TaskWorker, concurrency int) {
	for i := 0; i < concurrency; i++ {
		worker := *template
		if worker.RunnerID == "" {
			worker.RunnerID = fmt.Sprintf("loom-serve-task-worker-%d", i+1)
		}
		go func() {
			ticker := time.NewTicker(1 * time.Second)
			defer ticker.Stop()
			for {
				_, err := worker.RunOnce(ctx)
				if err != nil && !errors.Is(err, driverexecutor.ErrNoQueuedTaskRun) && !errors.Is(err, context.Canceled) {
					slog.Error("driver task worker run failed", "err", err)
				}
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
			}
		}()
	}
}

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

// driverAPIToken is the shared bearer token required by the driver-op HTTP
// API; the executor forwards it to driver runtimes. Empty disables the gate.
func driverAPIToken() string {
	return os.Getenv("LOOM_DRIVER_API_TOKEN")
}

// driverRunTokenKey resolves the HS256 signing key for run-scoped driver-op
// tokens (LOOM_RUN_TOKEN_SIGNING_KEY, with an ephemeral per-process fallback
// for single-instance deployments). A malformed env key logs and disables the
// run-token auth path — legacy header-quad auth keeps working — rather than
// aborting serve.
func driverRunTokenKey() []byte {
	key, err := driverexecutor.ResolveRunTokenSigningKey()
	if err != nil {
		slog.Error("driver run-token auth disabled: resolve signing key", "err", err)
		return nil
	}
	return key
}

// leadBootstrapEnabled is the single kill-switch for download-at-boot: it gates
// both the serve endpoint that streams serve's own binary and the provider step
// that installs it into each lead sandbox. Default off (fail-hard downloads
// make it opt-in). Accepts strconv.ParseBool truthy values ("1", "true", ...).

// leadAPIBaseURL is the public serve origin injected into Daytona lead
// sandboxes as LOOM_LEAD_API_URL. It must be reachable from inside the
// sandbox; behind a proxy, set LOOM_LEAD_API_BASE_URL to that public origin.
// When unset, the broker injects no URL and sandbox leads fail their
// preflight loudly instead of falling back to a local fleet-db.

// leadSnapshotRef resolves the snapshot every brokered lead sandbox boots
// from. LOOM_LEAD_SNAPSHOT accepts a Daytona snapshot name or ID and lets an
// operator switch to a rebuilt snapshot without a code change; unset falls
// back to the pinned default. The ref rides ProvisionRequest.SnapshotRef, so
// it need not touch the provider's own name/ID pin.
func leadSnapshotRef() string {
	if ref := strings.TrimSpace(os.Getenv(envLoomLeadSnapshot)); ref != "" {
		return ref
	}
	return daytona.DefaultSnapshotName
}

func leadLostReleaseGrace() time.Duration {
	return boundedDurationEnv(envLoomLeadLostReleaseGrace, 30*time.Minute)
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

// boundedDurationEnv reads a Go duration env var, falling back to def when
// unset, unparseable, or outside the positive-duration domain.
func boundedDurationEnv(name string, def time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return def
	}
	duration, err := time.ParseDuration(raw)
	if err != nil || duration <= 0 {
		return def
	}
	return duration
}

func ensureFleetStoreEnv(cfg config.FleetClientConfig) {
	if os.Getenv(bootstrap.EnvFleetDBURL) == "" && cfg.URL != "" {
		_ = os.Setenv(bootstrap.EnvFleetDBURL, cfg.URL)
	}
	if os.Getenv(bootstrap.EnvFleetDBActor) == "" && cfg.Actor != "" {
		_ = os.Setenv(bootstrap.EnvFleetDBActor, cfg.Actor)
	}
}

func ensureIssueBackend() bool {
	if serveNoDaemon {
		return false
	}
	started, err := cli.EnsureIssueBackendRunning(cli.GetDeps(nil), 5*time.Second)
	if err != nil {
		log.Printf("Warning: issue backend readiness check failed: %v", err)
		return false
	}
	return started
}

func stopIssueBackend() {
	// FleetDB mode does not start a per-workspace issue daemon.
}

// fleetState bundles fleet-related configuration resolved during startup.
type fleetState struct {
	modeDetected   bool
	clientCfg      config.FleetClientConfig
	jwtKey         []byte
	redisConfig    *daemonwire.FleetRedisConfig
	daemonSettings *config.DaemonSettings
}

func resolveFleetState(ctx context.Context) fleetState {
	fs := fleetState{}
	if dc, dcErr := config.LoadDaemonConfig("."); dcErr == nil {
		if serveRedisAddr == "" && dc.Daemon.RedisURL != "" {
			serveRedisAddr = dc.Daemon.RedisURL
		}
		fs.modeDetected = cli.IsFleetMode(dc)
		fs.daemonSettings = &dc.Daemon
	} else {
		fs.modeDetected = cli.IsFleetModeFromEnv()
	}
	fs.clientCfg = config.ResolveFleetConfig(fs.daemonSettings)
	if fs.clientCfg.URL == "" {
		fs.clientCfg.URL = os.Getenv(bootstrap.EnvFleetDBURL)
	}
	if fs.clientCfg.Actor == "" {
		fs.clientCfg.Actor = resolveFleetClientActorFallback()
	}

	fs.jwtKey, fs.redisConfig = daemonwire.ResolveFleetJWTKey(ctx, serveRedisAddr, serveRedisPassword)
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
	dir := cli.GetWorkspaceRuntimeDir()
	if dir == "" {
		dir = "."
	}
	usageHandler = usagecmd.HandleUsage(usagecmd.InitStore(dir))
}

func buildMonitorHandlers(collectDataFn metricscmd.CollectDataFn, staleDetectorHandler http.HandlerFunc, st store.Store, issueBackendFn metricscmd.IssueBackendFn, defaultWorkspace string) webui.MonitorHandlers {
	eventsDir := observability.ResolveEventsDir()
	monitorDataSource := metricscmd.NewMonitorDataSourceWithDefaultWorkspace(collectDataFn, issueBackendFn, defaultWorkspace)
	monitorStoreDataSource := metricscmd.NewMonitorStoreDataSource(st)
	return webui.MonitorHandlers{
		Status:               metricscmd.HandleStatusWithSources(monitorDataSource, monitorStoreDataSource),
		Agents:               metricscmd.HandleAgentsWithSources(monitorDataSource, monitorStoreDataSource),
		Tasks:                metricscmd.HandleTasksWithDataSource(monitorDataSource),
		Stats:                metricscmd.HandleStatsWithDataSource(monitorDataSource),
		Sync:                 metricscmd.HandleSync(collectDataFn),
		Workspaces:           metricscmd.HandleWorkspaces(st),
		StaleDetector:        staleDetectorHandler,
		Usage:                usageHandler,
		Metrics:              metricscmd.HandleMetrics(collectDataFn),
		ObservabilityMetrics: observability.HandleMetrics(eventsDir, observability.NewMetricsCache(eventsDir)),
		ObservabilityEvents:  observability.HandleEvents(eventsDir),
	}
}

func buildServerConfig(monitorHandlers webui.MonitorHandlers, fs fleetState, storeHandle *bootstrap.StoreHandle, placementBroker *placement.Broker, placementProviders placement.ProviderRegistry) webui.ServerConfig {
	gitOps := opsimpl.NewGitOps()
	resolvedBackend := cli.ResolveBackendName()
	log.Printf("Terminal backend: %s", resolvedBackend)

	cfg := buildCoreServerConfig(monitorHandlers, gitOps, resolvedBackend)
	if storeHandle != nil {
		cfg.Store = storeHandle.Store
		gitOps.WithStore(storeHandle.Store)
		if url := storeHandle.URL(); url != "" {
			cfg.IssueBackendFn = cli.WorkspaceAwareIssueBackendForURL(url, fs.clientCfg.Actor)
			cfg.FleetDBBaseURL = url
			fs = withStoreFleetURL(fs, url)
		}
		cfg.DriverAPIToken = driverAPIToken()
		cfg.DriverAPIBaseURL = driverAPIBaseURL()
		cfg.DriverRunTokenKey = driverRunTokenKey()
		cfg.LeadProvisioner = placementwire.LeadProvisioner(storeHandle.Store, placementBroker, leadSnapshotRef())
		if cfg.LeadProvisioner != nil && len(placementProviders) > 0 {
			cfg.LeadReviveCoordinator = placementwire.LeadReviveCoordinator(placementProviders, cfg.LeadProvisioner)
		}
	}
	applyFleetConfig(&cfg, fs)
	applyWorkspaceConfig(&cfg)
	applyCORSConfig(&cfg)
	return cfg
}

func buildCoreServerConfig(monitorHandlers webui.MonitorHandlers, gitOps *opsimpl.GitOpsImpl, backend string) webui.ServerConfig {
	leadDataAllowOpenAuth := strings.TrimSpace(os.Getenv(envLoomLeadDataAllowOpenAuth)) == "1"
	if leadDataAllowOpenAuth {
		if serveAuthURL == "" {
			slog.Error("lead data mount enabled in OPEN AUTH MODE (LOOM_LEAD_DATA_ALLOW_OPEN_AUTH=1) — POC-only posture, see .scratch/lead-in-daytona/issues/27-lead-origin-isolation.md")
		} else {
			slog.Warn("lead data mount open-auth override armed but inert (ext auth configured)")
		}
	}
	return webui.ServerConfig{
		Port:                  servePort,
		BindAddress:           serveBindAddr,
		SocketPath:            serveWebUISocket,
		FrontendDir:           serveFrontendDir,
		MonitorHandlers:       monitorHandlers,
		AgentControlFn:        daemonwire.BuildAgentControlFn(),
		AgentInputFn:          daemonwire.BuildAgentInputFn(),
		DaemonSupervisorFn:    daemonwire.BuildDaemonSupervisorFn(),
		DaemonConfigFn:        daemonwire.BuildDaemonConfigFn(),
		AgentQueueFn:          daemonwire.BuildAgentQueueFn(),
		TerminalCmd:           fmt.Sprintf("loom lead --backend %s", backend),
		HSTSEnabled:           serveHSTS,
		ExtAuthURL:            serveAuthURL,
		ExtAuthIssuer:         serveAuthIssuer,
		ExtAuthAudience:       serveAuthAudience,
		ExtAuthAllowInsecure:  serveAuthAllowInsecure,
		WorkspaceRoleResolver: buildFileBrowserRoleResolver(),
		LeadDataAllowOpenAuth: leadDataAllowOpenAuth,
		LeadBootstrapEnabled:  placementwire.LeadBootstrapEnabled(),
		GitOps:                gitOps,
		FileOps:               gitOps,
		BackendOps:            opsimpl.NewBackendOps(),
		NotifyTokenDir:        cli.GetWorkspaceRuntimeDir(),
		SessionRuntimeDir:     cli.GetWorkspaceRuntimeDir(),
		LocalSettingsDir:      bootstrap.LoomDir(),
		Logger:                slog.Default(),
		SentryDSN:             serveSentryDSN,
		// Wire the active IssueBackend (fleet / fleet-db / api) into
		// the webui service layer so the migrated CRUD endpoints don't
		// hardcode the rpc.Client path. The closure lets the backend resolve
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

// buildFileBrowserRoleResolver returns a WorkspaceRoleResolver that grants every
// authenticated identity a single fixed role for REMOTE file-browser access,
// controlled by LOOM_FILE_BROWSER_DEFAULT_ROLE ("viewer" = read-only, "editor"
// = read/write+sensitive). Unset/empty returns nil, preserving the fail-closed
// default (remote file access denied). This is a coarse deployment-level policy
// with NO per-user/per-workspace membership — pair a restrictive role (viewer)
// with a trusted-auth deployment. An unrecognized role fails closed (remote
// file access denied) and says so at startup, rather than looking enabled in
// the logs while every file request 403s.
func buildFileBrowserRoleResolver() middleware.WorkspaceRoleResolver {
	role := strings.ToLower(strings.TrimSpace(os.Getenv("LOOM_FILE_BROWSER_DEFAULT_ROLE")))
	if role == "" {
		return nil
	}
	if !middleware.KnownFileRole(role) {
		slog.Default().Error("LOOM_FILE_BROWSER_DEFAULT_ROLE is not a recognized role; remote file access stays disabled", "role", role)
		return nil
	}
	slog.Default().Warn("file-browser default role enabled: EVERY authenticated user gets this role for remote file access (no per-workspace membership)", "role", role)
	return func(_ context.Context, _ string, _ middleware.UserIdentity) (string, error) {
		return role, nil
	}
}

func withStoreFleetURL(fs fleetState, storeURL string) fleetState {
	if fs.clientCfg.URL == "" {
		fs.clientCfg.URL = storeURL
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
	cfg.FleetClientWorkspace = fs.clientCfg.Workspace
	cfg.FleetClientAPIKey = fs.clientCfg.APIKey
	cfg.FleetClientActor = fs.clientCfg.Actor
	// Store-backed serve uses FleetDB directly, either embedded local or
	// external cloud. In both shapes there is no local issue daemon for the
	// web UI to probe.
	cfg.FleetClient = fs.modeDetected || cfg.Store != nil
}

// applyWorkspaceConfig wires store-backed workspace operations into the webui
// server. Nil-store serve leaves workspace management unavailable.
func applyWorkspaceConfig(cfg *webui.ServerConfig) {
	if cfg.Store == nil {
		applyFleetInitialWorkspaceFallback(cfg, true)
		return
	}
	cfg.WorkspaceIDResolverFn = serveadapter.BuildWorkspaceIDResolverFn(cfg.Store)
	cfg.InitialWorkspaceID = serveadapter.ResolveInitialWorkspaceID(cfg.Store)
	applyFleetInitialWorkspaceFallback(cfg, false)
	cfg.WorkspaceDeleteFn = serveadapter.BuildWorkspaceDeleteFn(cfg.Store)
	cfg.SetDefaultWorkspaceFn = nil
	cfg.ClearDefaultWorkspaceFn = nil
	cfg.WorkspaceCreateFn = workspacemgr.BuildStoreBackedCreateWorkspace(cfg.Store)
	cfg.WorkspaceAddReposFn = workspacemgr.BuildStoreBackedAddRepos(cfg.Store)
	cfg.DaemonConfigFn = daemonwire.BuildStoreBackedDaemonConfigFn(cfg.Store)
}

func applyFleetInitialWorkspaceFallback(cfg *webui.ServerConfig, force bool) {
	if cfg == nil || !cfg.FleetClient || cfg.FleetClientWorkspace == "" {
		return
	}
	if cfg.Store != nil {
		if _, err := cfg.Store.Workspaces().Get(context.Background(), cfg.FleetClientWorkspace); err != nil {
			return
		}
	}
	if force || cfg.InitialWorkspaceID == "" || cfg.InitialWorkspaceID == "workspace" || cfg.InitialWorkspaceID == "default" {
		cfg.InitialWorkspaceID = cfg.FleetClientWorkspace
	}
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

func awaitShutdown(cmd *cobra.Command, stop chan os.Signal, webuiErr chan error, cancel context.CancelFunc) {
	select {
	case <-stop:
		log.Println("Shutting down server...")
	case err := <-webuiErr:
		if err != nil {
			cmd.PrintErrf("Server error: %v\n", err)
			cancel()
			os.Exit(1)
		}
	}

	cancel()

	select {
	case <-webuiErr:
	case <-time.After(10 * time.Second):
		log.Printf("Warning: server did not shut down within timeout")
	}
}

// backendProvider maps backend names to their provider labels.

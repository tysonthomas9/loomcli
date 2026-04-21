package serve

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/serve/daemonwire"
	"github.com/tysonthomas9/loomcli/internal/cli/serve/metricscmd"
	"github.com/tysonthomas9/loomcli/internal/cli/serve/observability"
	"github.com/tysonthomas9/loomcli/internal/cli/serve/opsimpl"
	"github.com/tysonthomas9/loomcli/internal/cli/serve/workspacemgr"
	"github.com/tysonthomas9/loomcli/internal/webui"
	webuiapp "github.com/tysonthomas9/loomcli/internal/webui/app"
)

// envLoomFleetMode is the env var that toggles --fleet-mode when no flag is
// passed. Intentionally separate from LOOM_ISSUE_BACKEND=fleet, which gates
// fleet-aware issue routing at a different layer.
const envLoomFleetMode = "LOOM_FLEET_MODE"

var (
	servePort              int
	serveBindAddr          string
	serveCorsOrigin        string
	serveFrontendURLs      []string
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

Auto-starts the bd daemon if not running (disable with --no-daemon).

ENVIRONMENT VARIABLES
  LOOM_SERVER_PORT     Server port (default: 8080)
  LOOM_BIND_ADDR       Bind address (default: 127.0.0.1)
  LOOM_CORS_ORIGIN     CORS allowed origin
  LOOM_FRONTEND_URL    Allowed frontend origin(s) for CORS (comma-separated)
  LOOM_REDIS_PASSWORD  Redis password (avoids exposure in process list)
  LOOM_AUTH_URL         External auth service base URL (enables JWT auth)
  LOOM_AUTH_ISSUER      Expected JWT issuer (defaults to LOOM_AUTH_URL)
  LOOM_AUTH_AUDIENCE    Expected JWT audience (defaults to "loom")

EXAMPLES
  loom serve                                              # Default port 8080
  loom serve --bind 0.0.0.0 --auth-url https://auth.co   # Exposed with JWT auth
  loom serve --frontend-url https://app.example.com       # Cross-origin frontend`,
	Args: cobra.NoArgs,
	Run:  runServe,
}

func init() {
	// Get defaults from environment
	defaultPort := 8080
	if envPort := os.Getenv("LOOM_SERVER_PORT"); envPort != "" {
		if p, err := strconv.Atoi(envPort); err == nil {
			defaultPort = p
		}
	}

	defaultCors := os.Getenv("LOOM_CORS_ORIGIN")

	defaultBind := os.Getenv("LOOM_BIND_ADDR")
	if defaultBind == "" {
		defaultBind = "127.0.0.1"
	}

	serveCmd.Flags().IntVarP(&servePort, "port", "p", defaultPort, "Server port")
	serveCmd.Flags().StringVar(&serveBindAddr, "bind", defaultBind, "Bind address (use 0.0.0.0 for all interfaces)")
	serveCmd.Flags().StringVar(&serveCorsOrigin, "cors", defaultCors, "CORS allowed origin")
	serveCmd.Flags().StringSliceVar(&serveFrontendURLs, "frontend-url", parseFrontendURLsEnv(), "Allowed frontend origin(s) for CORS. Repeatable or comma-separated. Env: LOOM_FRONTEND_URL")
	serveCmd.Flags().StringVar(&serveWebUISocket, "webui-socket", "", "Daemon socket path for webui (auto-detect if empty)")
	serveCmd.Flags().BoolVar(&serveNoDaemon, "no-daemon", false, "Skip auto-starting the bd daemon")

	defaultRedisAddr := os.Getenv("LOOM_REDIS_ADDR")
	serveCmd.Flags().StringVar(&serveRedisAddr, "redis-addr", defaultRedisAddr, "Redis address for fleet coordination (enables stale detector)")

	defaultRedisPassword := os.Getenv("LOOM_REDIS_PASSWORD")
	serveCmd.Flags().StringVar(&serveRedisPassword, "redis-password", defaultRedisPassword, "Redis password (prefer LOOM_REDIS_PASSWORD env var to avoid leaking in process list)")

	defaultFleetMode := os.Getenv(envLoomFleetMode) == "true"
	serveCmd.Flags().BoolVar(&serveFleetMode, "fleet-mode", defaultFleetMode, "Enable fleet coordination features (stale detector, task claims, fleet routes). Default off for local dev. Env: "+envLoomFleetMode)

	defaultFleetAPIKey := os.Getenv("LOOM_FLEET_API_KEY")
	serveCmd.Flags().StringVar(&serveFleetAPIKey, "fleet-api-key", defaultFleetAPIKey, "API key for fleet worker registration (required for fleet register endpoint)")

	serveCmd.Flags().BoolVar(&serveHSTS, "hsts", false, "Enable HSTS header (use when behind TLS-terminating proxy)")

	defaultAuthURL := os.Getenv("LOOM_AUTH_URL")
	serveCmd.Flags().StringVar(&serveAuthURL, "auth-url", defaultAuthURL, "External auth service base URL (enables JWT auth)")

	defaultAuthIssuer := os.Getenv("LOOM_AUTH_ISSUER")
	serveCmd.Flags().StringVar(&serveAuthIssuer, "auth-issuer", defaultAuthIssuer, "Expected JWT issuer (defaults to --auth-url)")

	defaultAuthAudience := os.Getenv("LOOM_AUTH_AUDIENCE")
	serveCmd.Flags().StringVar(&serveAuthAudience, "auth-audience", defaultAuthAudience, "Expected JWT audience (defaults to \"loom\")")

	serveCmd.Flags().BoolVar(&serveAuthAllowInsecure, "auth-allow-insecure", false, "Allow HTTP for non-loopback --auth-url (INSECURE, for Docker internal networks only)")

	defaultSentryDSN := os.Getenv("LOOM_SENTRY_DSN")
	serveCmd.Flags().StringVar(&serveSentryDSN, "sentry-dsn", defaultSentryDSN, "Sentry/GlitchTip DSN for error tracking (or LOOM_SENTRY_DSN)")

	cli.RegisterCommand(serveCmd)
}

func runServe(cmd *cobra.Command, args []string) {
	checkTmuxInstalled()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Proactively refresh the monitor cache every 6s so HTTP requests
	// always read warm data instead of blocking on collectMonitorData.
	// (TTL is 10s, so 6s refresh leaves a 4s safety margin.)
	collectDataFn := metricscmd.NewCollectorWithBackground(ctx, 10*time.Second, 6*time.Second)

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
	go workspacemgr.PurgeOldSessions()

	monitorHandlers := buildMonitorHandlers(collectDataFn, staleDetectorHandler)
	workspacemgr.EnsureProjectRegistered()

	webuiErr := make(chan error, 1)
	go func() {
		cfg := buildServerConfig(monitorHandlers, fleetState)
		if !serveNoDaemon {
			cfg.DaemonStartupFn = workspacemgr.EnsureDaemonsForAllWorkspaces
		}
		webuiErr <- webuiapp.StartServer(ctx, cfg)
	}()

	logServerStartup()
	awaitShutdown(cmd, stop, webuiErr, cancel)
}

func checkTmuxInstalled() {
	if _, err := exec.LookPath("tmux"); err != nil {
		fmt.Fprintf(os.Stderr, "Error: tmux is required but not found in PATH.\nInstall it with: brew install tmux (macOS) or apt install tmux (Linux)\n")
		os.Exit(1)
	}
}

func ensureIssueBackend() bool {
	if serveNoDaemon {
		return false
	}
	started, err := cli.EnsureIssueBackendRunning(cli.GetDeps(nil), 5*time.Second)
	if err != nil {
		log.Printf("Warning: failed to auto-start issue backend: %v (endpoints may return incomplete data)", err)
		return false
	}
	if started {
		log.Printf("Auto-started issue backend daemon")
		return true
	}
	log.Printf("Issue backend daemon already running")
	return false
}

func stopIssueBackend() {
	result := cli.GetDeps(nil).Exec.Run(cli.GetBeadsDir(), "bd", "daemon", "stop")
	if result.Err != nil {
		log.Printf("Warning: failed to stop issue backend daemon: %v", result.Err)
	}
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
	if fs.modeDetected {
		fs.clientCfg = config.ResolveFleetConfig(fs.daemonSettings)
	}

	fs.jwtKey, fs.redisConfig = daemonwire.ResolveFleetJWTKey(ctx, serveRedisAddr, serveRedisPassword)
	return fs
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

func buildMonitorHandlers(collectDataFn metricscmd.CollectDataFn, staleDetectorHandler http.HandlerFunc) webui.MonitorHandlers {
	eventsDir := observability.ResolveEventsDir()
	return webui.MonitorHandlers{
		AgentsScoped:         metricscmd.HandleAgentsScoped(collectDataFn, workspacemgr.ResolveWorkspaceNameByID),
		Workspaces:           metricscmd.HandleWorkspaces(),
		StaleDetector:        staleDetectorHandler,
		Metrics:              metricscmd.HandleMetrics(collectDataFn),
		ObservabilityMetrics: observability.HandleMetrics(eventsDir, observability.NewMetricsCache(eventsDir)),
		ObservabilityEvents:  observability.HandleEvents(eventsDir),
	}
}

func buildServerConfig(monitorHandlers webui.MonitorHandlers, fs fleetState) webui.ServerConfig {
	gitOps := opsimpl.NewGitOps()
	resolvedBackend := cli.ResolveBackendName()
	log.Printf("Terminal backend: %s", resolvedBackend)

	cfg := buildCoreServerConfig(monitorHandlers, gitOps, resolvedBackend)
	cfg.ScopedMonitorHandlersFn = metricscmd.BuildScopedMonitorHandlers(workspacemgr.ResolveWorkspaceNameByID)
	applyFleetConfig(&cfg, fs)
	applyWorkspaceConfig(&cfg)
	applyCORSConfig(&cfg)
	return cfg
}

func buildCoreServerConfig(monitorHandlers webui.MonitorHandlers, gitOps *opsimpl.GitOpsImpl, backend string) webui.ServerConfig {
	return webui.ServerConfig{
		Port:                   servePort,
		BindAddress:            serveBindAddr,
		SocketPath:             serveWebUISocket,
		MonitorHandlers:        monitorHandlers,
		AgentControlFn:         daemonwire.BuildAgentControlFn(),
		DaemonSupervisorFn:     daemonwire.BuildDaemonSupervisorFn(),
		DaemonConfigFn:         daemonwire.BuildDaemonConfigFn(),
		LoadDaemonSupervisorFn: daemonwire.LoadDaemonSupervisor,
		LoadDaemonConfigFn:     daemonwire.LoadDaemonConfigRaw,
		AgentQueueFn:           daemonwire.BuildAgentQueueFn(),
		TerminalCmd:            fmt.Sprintf("loom lead --backend %s", backend),
		HSTSEnabled:            serveHSTS,
		ExtAuthURL:             serveAuthURL,
		ExtAuthIssuer:          serveAuthIssuer,
		ExtAuthAudience:        serveAuthAudience,
		ExtAuthAllowInsecure:   serveAuthAllowInsecure,
		GitOps:                 gitOps,
		FileOps:                gitOps,
		BackendOps:             opsimpl.NewBackendOps(),
		NotifyTokenDir:         cli.GetBeadsDir(),
		Logger:                 slog.Default(),
		SentryDSN:              serveSentryDSN,
	}
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
}

func applyWorkspaceConfig(cfg *webui.ServerConfig) {
	cfg.WorkspaceConfigFn = workspacemgr.BuildWorkspaceInfo
	cfg.WorkspaceConfigByIDFn = workspacemgr.BuildWorkspaceInfoForID
	cfg.WorkspaceDeleteFn = workspacemgr.DeleteWorkspace
	cfg.SetDefaultWorkspaceFn = workspacemgr.SetDefaultWorkspace
	cfg.ClearDefaultWorkspaceFn = workspacemgr.ClearDefaultWorkspace
	cfg.WorkspaceCreateFn = workspacemgr.CreateWorkspace
	cfg.WorkspaceListFn = daemonwire.ListWorkspaces
	cfg.InitialWorkspaceID = workspacemgr.ResolveInitialWorkspaceID()
	cfg.WorkspaceIDResolverFn = workspacemgr.ResolveWorkspaceID
}

func applyCORSConfig(cfg *webui.ServerConfig) {
	origins := cfg.CORSOrigins
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
		}
	}
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

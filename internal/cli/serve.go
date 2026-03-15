package cli

import (
	"context"
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/circuitbreaker"
	"github.com/tysonthomas9/loomcli/internal/kv"
	"github.com/tysonthomas9/loomcli/internal/usage"
	"github.com/tysonthomas9/loomcli/internal/webui"
	"github.com/tysonthomas9/loomcli/internal/webui/fleet"
)

var (
	servePort          int
	serveBindAddr      string
	serveCorsOrigin    string
	serveWebUIPort     int
	serveWebUISocket   string
	serveNoWebUI       bool
	serveNoDaemon      bool
	serveRedisAddr     string
	serveRedisPassword string
	serveAPIKey        string
	serveFleetAPIKey   string
	serveAuth          bool
	serveHSTS          bool
	serveDev           bool
	serveDevFrontDir   string

	// collectDataFunc is the function used to collect monitor data.
	// This is a package-level variable to allow tests to inject mock data.
	collectDataFunc = func() *MonitorData { return collectMonitorData(50, monitorBranch) }

	// staleDetectorInstance holds the running stale detector for status queries.
	staleDetectorInstance *kv.StaleDetector

	// usageStoreInstance holds the usage store for the /api/usage endpoint.
	usageStoreInstance *usage.Store
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start HTTP server for agent status API",
	Long: `Start an HTTP server that exposes agent status via REST API.

This server is designed to be consumed by web UIs (like beads-web-ui)
that want to display agent status and task information.

The server automatically starts the bd daemon if it's not running.
Use --no-daemon to disable this behavior.

ENDPOINTS
  GET /health          Health check
  GET /metrics         Prometheus metrics for KEDA scaling
  GET /api/status      Full dashboard data (agents, tasks, stats, sync)
  GET /api/agents      Just agent status
  GET /api/tasks       Task queue and lists
  GET /api/stats       Statistics (open/closed/completion)
  GET /api/sync        Sync status
  GET /api/usage       Token usage and cost data (filterable)
  GET /api/workspaces  Workspace configuration (workspace mode only)
  GET /api/observability/{metrics,events}  Observability data

ENVIRONMENT VARIABLES
  LOOM_SERVER_PORT    Server port (default: 8081)
  LOOM_BIND_ADDR      Bind address (default: 127.0.0.1)
  LOOM_CORS_ORIGIN    CORS allowed origin (default: http://localhost:<webui-port>)
  LOOM_REDIS_PASSWORD Redis password (avoids exposure in process list)

DEVELOPMENT MODE
  Use --dev to serve the frontend from disk instead of the embedded filesystem.
  This allows iterating on the frontend without recompiling the Go binary.
  CORS is automatically enabled for the Vite dev server origin.
  WARNING: Dev mode is for local development only. Do not use in production.

EXAMPLES
  loom serve                          # Start on default port 8081
  loom serve --port 9000              # Start on port 9000
  loom serve --bind 0.0.0.0           # Listen on all interfaces
  loom serve --cors http://localhost:8080   # Allow specific origin
  loom serve --dev                    # Dev mode: serve frontend from disk
  loom serve --dev --dev-frontend-dir ./my-frontend/dist  # Custom frontend dir`,
	Args: cobra.NoArgs,
	Run:  runServe,
}

func init() {
	// Get defaults from environment
	defaultPort := 8081
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
	serveCmd.Flags().StringVar(&serveCorsOrigin, "cors", defaultCors, "CORS allowed origin (default: http://localhost:<webui-port>)")
	serveCmd.Flags().IntVar(&serveWebUIPort, "webui-port", 8080, "Port for the web UI server")
	serveCmd.Flags().StringVar(&serveWebUISocket, "webui-socket", "", "Daemon socket path for webui (auto-detect if empty)")
	serveCmd.Flags().BoolVar(&serveNoWebUI, "no-webui", false, "Disable the web UI server, run only the API")
	serveCmd.Flags().BoolVar(&serveNoDaemon, "no-daemon", false, "Skip auto-starting the bd daemon")

	defaultRedisAddr := os.Getenv("LOOM_REDIS_ADDR")
	serveCmd.Flags().StringVar(&serveRedisAddr, "redis-addr", defaultRedisAddr, "Redis address for fleet coordination (enables stale detector)")

	defaultRedisPassword := os.Getenv("LOOM_REDIS_PASSWORD")
	serveCmd.Flags().StringVar(&serveRedisPassword, "redis-password", defaultRedisPassword, "Redis password (prefer LOOM_REDIS_PASSWORD env var to avoid leaking in process list)")

	defaultAPIKey := os.Getenv("LOOM_WEBUI_API_KEY")
	serveCmd.Flags().StringVar(&serveAPIKey, "api-key", defaultAPIKey, "API key for WebUI authentication (auto-generated if empty)")

	defaultFleetAPIKey := os.Getenv("LOOM_FLEET_API_KEY")
	serveCmd.Flags().StringVar(&serveFleetAPIKey, "fleet-api-key", defaultFleetAPIKey, "API key for fleet worker registration (required for fleet register endpoint)")

	serveCmd.Flags().BoolVar(&serveAuth, "auth", false, "Enable WebUI API authentication")
	serveCmd.Flags().BoolVar(&serveHSTS, "hsts", false, "Enable HSTS header (use when behind TLS-terminating proxy)")
	serveCmd.Flags().BoolVar(&serveDev, "dev", false, "Development mode: serve frontend from disk, enable CORS for Vite dev server")
	serveCmd.Flags().StringVar(&serveDevFrontDir, "dev-frontend-dir", "", "Frontend directory to serve in dev mode (default: internal/webui/frontend/dist)")

	rootCmd.AddCommand(serveCmd)
}

func runServe(cmd *cobra.Command, args []string) {
	// Check tmux is installed (required for terminal relay)
	if _, err := exec.LookPath("tmux"); err != nil {
		fmt.Fprintf(os.Stderr, "Error: tmux is required but not found in PATH.\nInstall it with: brew install tmux (macOS) or apt install tmux (Linux)\n")
		os.Exit(1)
	}

	// Cache collectMonitorData results to avoid redundant shell-outs from
	// concurrent API requests. The frontend polls 3 endpoints every 5s;
	// without this cache each poll cycle spawns ~60-90 subprocesses.
	collector := newCachedValue[*MonitorData](2*time.Second, func() *MonitorData {
		return collectMonitorData(50, monitorBranch)
	})
	collectDataFunc = collector.get

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Signal handling
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	// Auto-start bd daemon if needed (unless --no-daemon)
	var daemonWeStarted bool
	if !serveNoDaemon {
		started, err := EnsureBdDaemonRunning(5 * time.Second)
		if err != nil {
			log.Printf("Warning: failed to auto-start bd daemon: %v", err)
			log.Printf("Some API endpoints may return incomplete data. Run 'bd daemon start' manually.")
		} else if started {
			daemonWeStarted = true
			log.Printf("Auto-started bd daemon")
		} else {
			log.Printf("bd daemon already running")
		}
	}

	// Ensure daemon cleanup on any exit path (including os.Exit in error handlers)
	if daemonWeStarted {
		defer func() {
			log.Printf("Stopping bd daemon (we started it)...")
			result := execCommand(GetBeadsDir(), "bd", "daemon", "stop")
			if result.Err != nil {
				log.Printf("Warning: failed to stop bd daemon: %v", result.Err)
			}
		}()
	}

	// Fall back to daemon config for Redis/API key when CLI flags/env vars are not set
	if dc, dcErr := LoadDaemonConfig("."); dcErr == nil {
		if serveRedisAddr == "" && dc.Daemon.RedisURL != "" {
			serveRedisAddr = dc.Daemon.RedisURL
		}
		if serveAPIKey == "" && dc.Daemon.APIKey != "" {
			serveAPIKey = dc.Daemon.APIKey
		}
	}

	// Provision shared JWT signing key from Redis (if configured) or environment
	var fleetJWTKey []byte
	var fleetRedisConfig *fleet.RedisConfig
	if serveRedisAddr != "" {
		fleetRedisConfig = &fleet.RedisConfig{Address: serveRedisAddr, Password: serveRedisPassword}

		if envKey := os.Getenv("LOOM_FLEET_JWT_KEY"); envKey != "" {
			decoded, err := hex.DecodeString(envKey)
			if err != nil || len(decoded) < 32 {
				log.Fatalf("LOOM_FLEET_JWT_KEY must be a hex-encoded string of at least 32 bytes")
			}
			fleetJWTKey = decoded
			log.Printf("Using JWT signing key from LOOM_FLEET_JWT_KEY environment variable")
		} else {
			// Get or create shared signing key from Redis
			redisClient := fleet.NewRedisClient(serveRedisAddr, serveRedisPassword, 0)
			mgr := fleet.NewSigningKeyManager(redisClient, nil)
			key, err := mgr.GetOrCreateSigningKey(ctx)
			_ = redisClient.Close()
			if err != nil {
				log.Printf("Warning: failed to provision JWT signing key from Redis: %v", err)
				log.Printf("Fleet auth will use an ephemeral key (tokens won't validate on other servers)")
			} else {
				fleetJWTKey = key
				log.Printf("JWT signing key provisioned from Redis")
			}
		}
	} else if envKey := os.Getenv("LOOM_FLEET_JWT_KEY"); envKey != "" {
		decoded, err := hex.DecodeString(envKey)
		if err != nil || len(decoded) < 32 {
			log.Fatalf("LOOM_FLEET_JWT_KEY must be a hex-encoded string of at least 32 bytes")
		}
		fleetJWTKey = decoded
		log.Printf("Using JWT signing key from LOOM_FLEET_JWT_KEY environment variable")
	}

	// Warn if binding to non-localhost address
	if serveBindAddr != "127.0.0.1" && serveBindAddr != "::1" {
		log.Printf("WARNING: Server bound to %s — exposed to network. Ensure this is intentional.", serveBindAddr)
	}

	// Create backend ops for health checking.
	backendOps := NewBackendOps()

	// Resolve backend name for terminal sessions.
	// ResolveBackendName() checks: flag > env > project-local loom.yaml > global config > default.
	resolvedBackend := ResolveBackendName()
	log.Printf("Terminal backend: %s", resolvedBackend)
	terminalCmd := fmt.Sprintf("loom lead --backend %s", resolvedBackend)

	// Start webui server in goroutine (unless --no-webui)
	webuiErr := make(chan error, 1)
	if !serveNoWebUI {
		if !serveAuth {
			log.Printf("WebUI API authentication is disabled (enable with --auth)")
		}
		go func() {
			gitOps := NewGitOps()
			cfg := webui.ServerConfig{
				Port:              serveWebUIPort,
				BindAddress:       serveBindAddr,
				SocketPath:        serveWebUISocket,
				LoomServerURL:     fmt.Sprintf("http://localhost:%d", servePort),
				TerminalCmd:       terminalCmd,
				FleetEnabled:      serveRedisAddr != "",
				FleetRedis:        fleetRedisConfig,
				FleetJWTKey:       fleetJWTKey,
				FleetAPIKey:       serveFleetAPIKey,
				APIKey:            serveAPIKey,
				AuthEnabled:       serveAuth,
				HSTSEnabled:       serveHSTS,
				DevMode:           serveDev,
				DevFrontendDir:    serveDevFrontDir,
				GitOps:            gitOps,
				FileOps:           gitOps, // GitOpsImpl satisfies FileOps (same ResolveAgentWorktree)
				WorkspaceConfigFn: buildWorkspaceInfo,
				WorkspaceDeleteFn: deleteWorkspace,
				BackendOps:        backendOps,
			}
			if serveCorsOrigin != "" {
				cfg.CORSEnabled = true
				cfg.CORSOrigins = []string{serveCorsOrigin}
			} else if serveDev {
				cfg.CORSEnabled = true
				// Uses default origin (http://localhost:3000) in server.go
			}
			webuiErr <- webui.StartServer(ctx, cfg)
		}()
		log.Printf("Web UI server starting on port %d", serveWebUIPort)
		if serveDev {
			dir := serveDevFrontDir
			if dir == "" {
				dir = "internal/webui/frontend/dist"
			}
			log.Printf("Development mode enabled: serving frontend from %s", dir)
		}
	}

	// Start stale detector if Redis is configured
	var kvClient *kv.Client
	if serveRedisAddr != "" {
		kvClient = kv.NewClient(serveRedisAddr, serveRedisPassword, 0)
		defer func() {
			if err := kvClient.Close(); err != nil {
				log.Printf("Error closing Redis client: %v", err)
			}
		}()

		breaker := circuitbreaker.NewBreaker("redis-stale-detector", circuitbreaker.Config{
			FailureThreshold: 5,
			OpenTimeout:      30 * time.Second,
			ShouldTrip:       kv.RedisShouldTrip,
		})
		kvClient.SetCircuitBreaker(breaker)

		cfg := kv.DefaultStaleDetectorConfig()
		serverID := kv.GenerateServerID()
		reconciler := kv.NewReconciler("")

		detector := kv.NewStaleDetector(kvClient, cfg, serverID, reconciler)
		staleDetectorInstance = detector

		go func() {
			if err := detector.Run(ctx); err != nil && ctx.Err() == nil {
				log.Printf("Stale detector error: %v", err)
			}
		}()
		log.Printf("Stale detector enabled (redis=%s, server=%s)", serveRedisAddr, serverID)
	}

	// Initialize usage store for /api/usage endpoint
	loomDir := GetBeadsDir()
	if loomDir == "" {
		loomDir = "."
	}
	usageStore, err := usage.NewStore(loomDir)
	if err != nil {
		log.Printf("Warning: failed to create usage store: %v", err)
	} else {
		usageStoreInstance = usageStore
	}

	// Set up the loom API server
	mux := http.NewServeMux()

	// Register routes
	mux.HandleFunc("GET /health", handleHealth)
	mux.HandleFunc("GET /api/status", handleStatus)
	mux.HandleFunc("GET /api/agents", handleAgents)
	mux.HandleFunc("GET /api/tasks", handleTasks)
	mux.HandleFunc("GET /api/stats", handleStats)
	mux.HandleFunc("GET /api/sync", handleSync)
	mux.HandleFunc("GET /api/workspaces", handleWorkspaces)
	mux.HandleFunc("GET /api/stale-detector", handleStaleDetector)
	mux.HandleFunc("GET /api/usage", handleUsage)
	mux.HandleFunc("GET /metrics", handleMetrics)
	eventsDir := resolveEventsDir()
	mux.HandleFunc("GET /api/observability/metrics", handleObservabilityMetrics(eventsDir, newMetricsCache(eventsDir)))
	mux.HandleFunc("GET /api/observability/events", handleObservabilityEvents(eventsDir))

	// Wrap with CORS middleware
	handler := corsMiddleware(serveCorsOrigin, mux)

	server := &http.Server{
		Addr:              net.JoinHostPort(serveBindAddr, strconv.Itoa(servePort)),
		Handler:           handler,
		ReadTimeout:       10 * time.Second,
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      10 * time.Second,
	}

	// Start API server
	apiErr := make(chan error, 1)
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			apiErr <- err
		}
		close(apiErr)
	}()

	log.Printf("Starting loom API server on %s:%d", serveBindAddr, servePort)
	if serveCorsOrigin != "" {
		log.Printf("CORS enabled for origin: %s", serveCorsOrigin)
	}

	// Wait for signal, API error, or webui error
	select {
	case <-stop:
		log.Println("Shutting down servers...")
	case err := <-apiErr:
		if err != nil {
			cmd.PrintErrf("API server error: %v\n", err)
			cancel()
			os.Exit(1)
		}
	case err := <-webuiErr:
		if err != nil {
			log.Printf("Warning: webui server error: %v", err)
		}
		// Webui failure should not bring down the API server; wait for signal or API error
		select {
		case <-stop:
			log.Println("Shutting down servers...")
		case err := <-apiErr:
			if err != nil {
				cmd.PrintErrf("API server error: %v\n", err)
				cancel()
				os.Exit(1)
			}
		}
	}

	// Cancel context to stop webui server
	cancel()

	// Gracefully shut down API server
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("API server shutdown error: %v", err)
	}

	// Wait for webui goroutine to finish its shutdown
	if !serveNoWebUI {
		select {
		case <-webuiErr:
		case <-time.After(10 * time.Second):
			log.Printf("Warning: webui server did not shut down within timeout")
		}
	}
}

// backendProvider maps backend names to their provider labels.

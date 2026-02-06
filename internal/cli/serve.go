package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/tysonthomas9/loomcli/internal/circuitbreaker"
	"github.com/tysonthomas9/loomcli/internal/kv"
	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/webui"
)

var (
	servePort        int
	serveCorsOrigin  string
	serveWebUIPort   int
	serveWebUISocket string
	serveNoWebUI     bool
	serveNoDaemon    bool
	serveRedisAddr   string

	// collectDataFunc is the function used to collect monitor data.
	// This is a package-level variable to allow tests to inject mock data.
	collectDataFunc = collectMonitorData

	// staleDetectorInstance holds the running stale detector for status queries.
	staleDetectorInstance *kv.StaleDetector
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
  GET /api/workspaces  Workspace configuration (workspace mode only)

ENVIRONMENT VARIABLES
  LOOM_SERVER_PORT    Server port (default: 8081)
  LOOM_CORS_ORIGIN    CORS allowed origin (default: * for all)

EXAMPLES
  loom serve                          # Start on default port 8081
  loom serve --port 9000              # Start on port 9000
  loom serve --cors http://localhost:8080   # Allow specific origin`,
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

	serveCmd.Flags().IntVarP(&servePort, "port", "p", defaultPort, "Server port")
	serveCmd.Flags().StringVar(&serveCorsOrigin, "cors", defaultCors, "CORS allowed origin (empty for all)")
	serveCmd.Flags().IntVar(&serveWebUIPort, "webui-port", 8080, "Port for the web UI server")
	serveCmd.Flags().StringVar(&serveWebUISocket, "webui-socket", "", "Daemon socket path for webui (auto-detect if empty)")
	serveCmd.Flags().BoolVar(&serveNoWebUI, "no-webui", false, "Disable the web UI server, run only the API")
	serveCmd.Flags().BoolVar(&serveNoDaemon, "no-daemon", false, "Skip auto-starting the bd daemon")

	defaultRedisAddr := os.Getenv("LOOM_REDIS_ADDR")
	serveCmd.Flags().StringVar(&serveRedisAddr, "redis-addr", defaultRedisAddr, "Redis address for fleet coordination (enables stale detector)")

	rootCmd.AddCommand(serveCmd)
}

func runServe(cmd *cobra.Command, args []string) {
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

	// Start webui server in goroutine (unless --no-webui)
	webuiErr := make(chan error, 1)
	if !serveNoWebUI {
		go func() {
			cfg := webui.ServerConfig{
				Port:       serveWebUIPort,
				SocketPath: serveWebUISocket,
			}
			if serveCorsOrigin != "" {
				cfg.CORSEnabled = true
				cfg.CORSOrigins = []string{serveCorsOrigin}
			}
			webuiErr <- webui.StartServer(ctx, cfg)
		}()
		log.Printf("Web UI server starting on port %d", serveWebUIPort)
	}

	// Start stale detector if Redis is configured
	var kvClient *kv.Client
	if serveRedisAddr != "" {
		kvClient = kv.NewClient(serveRedisAddr, "", 0)
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
	mux.HandleFunc("GET /metrics", handleMetrics)

	// Wrap with CORS middleware
	handler := corsMiddleware(serveCorsOrigin, mux)

	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", servePort),
		Handler:      handler,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	// Start API server
	apiErr := make(chan error, 1)
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			apiErr <- err
		}
		close(apiErr)
	}()

	log.Printf("Starting loom API server on port %d", servePort)
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

// corsMiddleware adds CORS headers to responses.
func corsMiddleware(corsOrigin string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := corsOrigin
		if origin == "" {
			origin = "*"
		}

		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// Response types for JSON API

// WorkspaceInfo represents workspace metadata for API responses.
type WorkspaceInfo struct {
	Mode       string   `json:"mode"`                 // "workspace" or "legacy"
	Name       string   `json:"name,omitempty"`       // workspace name (workspace mode only)
	Workspaces []string `json:"workspaces,omitempty"` // all workspace names (workspace mode only)
}

// WorkspacesResponse lists all configured workspaces.
type WorkspacesResponse struct {
	Mode       string                     `json:"mode"`       // "workspace" or "legacy"
	Default    string                     `json:"default"`    // default workspace name
	Workspaces map[string]WorkspaceDetail `json:"workspaces"` // workspace details
	Timestamp  time.Time                  `json:"timestamp"`
}

// WorkspaceDetail contains details about a single workspace.
type WorkspaceDetail struct {
	Path  string   `json:"path"`  // workspace root path
	Repos []string `json:"repos"` // repo names in this workspace
}

// HealthResponse is the health check response.
type HealthResponse struct {
	Status    string    `json:"status"`
	Timestamp time.Time `json:"timestamp"`
}

// AgentsResponse wraps the agents list with optional workspace grouping.
type AgentsResponse struct {
	Workspace   WorkspaceInfo              `json:"workspace"`               // workspace mode info
	Agents      []AgentStatus              `json:"agents"`                  // flat list (existing)
	ByWorkspace map[string][]AgentStatus   `json:"by_workspace,omitempty"` // grouped by workspace
	Timestamp   time.Time                  `json:"timestamp"`
}

// TasksResponse wraps task information.
type TasksResponse struct {
	Summary          TaskSummary `json:"summary"`
	NeedsPlanning    []TaskInfo  `json:"needs_planning"`
	ReadyToImplement []TaskInfo  `json:"ready_to_implement"`
	NeedsReview      []TaskInfo  `json:"needs_review"`
	InProgress       []TaskInfo  `json:"in_progress"`
	Backlog          []TaskInfo  `json:"backlog"`
	Timestamp        time.Time   `json:"timestamp"`
}

// StatsResponse wraps statistics.
type StatsResponse struct {
	Stats     MonitorStats `json:"stats"`
	Timestamp time.Time    `json:"timestamp"`
}

// SyncResponse wraps sync status.
type SyncResponse struct {
	Sync      SyncInfo  `json:"sync"`
	Timestamp time.Time `json:"timestamp"`
}

// StatusResponse is the full status (like monitor dashboard).
type StatusResponse struct {
	Workspace      WorkspaceInfo       `json:"workspace"`
	Agents         []AgentStatus       `json:"agents"`
	Tasks          TaskSummary         `json:"tasks"`
	InProgressList []TaskInfo          `json:"in_progress_list"`
	AgentTasks     map[string]TaskInfo `json:"agent_tasks"`
	Stats          MonitorStats        `json:"stats"`
	Sync           SyncInfo            `json:"sync"`
	Timestamp      time.Time           `json:"timestamp"`
}

// HTTP Handlers

func handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, HealthResponse{
		Status:    "ok",
		Timestamp: time.Now(),
	})
}

func handleStatus(w http.ResponseWriter, r *http.Request) {
	data := collectDataFunc()
	writeJSON(w, StatusResponse{
		Workspace:      getWorkspaceInfo(),
		Agents:         data.Agents,
		Tasks:          data.Tasks,
		InProgressList: data.InProgressTasks,
		AgentTasks:     data.AgentTasks,
		Stats:          data.Stats,
		Sync:           data.SyncStatus,
		Timestamp:      data.Timestamp,
	})
}

func handleAgents(w http.ResponseWriter, r *http.Request) {
	data := collectDataFunc()
	wsInfo := getWorkspaceInfo()

	response := AgentsResponse{
		Workspace: wsInfo,
		Agents:    data.Agents,
		Timestamp: data.Timestamp,
	}

	// Group agents by workspace if in workspace mode
	if wsInfo.Mode == "workspace" {
		response.ByWorkspace = groupAgentsByWorkspace(data.Agents)
	}

	writeJSON(w, response)
}

func handleTasks(w http.ResponseWriter, r *http.Request) {
	data := collectDataFunc()
	writeJSON(w, TasksResponse{
		Summary:          data.Tasks,
		NeedsPlanning:    data.NeedsPlanningTasks,
		ReadyToImplement: data.ReadyToImplement,
		NeedsReview:      data.ReviewTasks,
		InProgress:       data.InProgressTasks,
		Backlog:          data.BlockedTasks,
		Timestamp:        data.Timestamp,
	})
}

func handleStats(w http.ResponseWriter, r *http.Request) {
	data := collectDataFunc()
	writeJSON(w, StatsResponse{
		Stats:     data.Stats,
		Timestamp: data.Timestamp,
	})
}

func handleSync(w http.ResponseWriter, r *http.Request) {
	data := collectDataFunc()
	writeJSON(w, SyncResponse{
		Sync:      data.SyncStatus,
		Timestamp: data.Timestamp,
	})
}

func handleStaleDetector(w http.ResponseWriter, r *http.Request) {
	if staleDetectorInstance == nil {
		writeJSON(w, kv.StaleDetectorStatus{Enabled: false})
		return
	}
	writeJSON(w, staleDetectorInstance.Status())
}

func handleMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

	// Collect task data
	data := collectDataFunc()

	// Get ready tasks broken down by priority
	readyByPriority := collectReadyTasksByPriority()

	// Get in-progress count
	inProgress := 0
	if data != nil {
		inProgress = data.Tasks.InProgress
	}

	// Write loom_ready_tasks metric
	fmt.Fprintf(w, "# HELP loom_ready_tasks Number of tasks ready to be claimed\n")
	fmt.Fprintf(w, "# TYPE loom_ready_tasks gauge\n")
	for p := 0; p <= 4; p++ {
		fmt.Fprintf(w, "loom_ready_tasks{priority=\"%d\"} %d\n", p, readyByPriority[p])
	}

	// Write loom_in_progress_tasks metric
	fmt.Fprintf(w, "\n# HELP loom_in_progress_tasks Number of tasks currently being worked on\n")
	fmt.Fprintf(w, "# TYPE loom_in_progress_tasks gauge\n")
	fmt.Fprintf(w, "loom_in_progress_tasks %d\n", inProgress)

	// Write loom_fleet_workers metric
	workerCounts := collectWorkerStatusCounts()
	fmt.Fprintf(w, "\n# HELP loom_fleet_workers Number of fleet workers by status\n")
	fmt.Fprintf(w, "# TYPE loom_fleet_workers gauge\n")
	for _, status := range []string{"active", "idle", "blocked"} {
		fmt.Fprintf(w, "loom_fleet_workers{status=\"%s\"} %d\n", status, workerCounts[status])
	}
}

// collectWorkerStatusCounts connects to the daemon via RPC and aggregates
// worker counts by status. Returns zeros if daemon is unavailable.
func collectWorkerStatusCounts() map[string]int {
	counts := map[string]int{"active": 0, "idle": 0, "blocked": 0}

	beadsDir := GetBeadsDir()
	if beadsDir == "" {
		beadsDir = "."
	}

	// Resolve absolute path for socket discovery
	absPath, err := filepath.Abs(beadsDir)
	if err != nil {
		log.Printf("metrics: failed to resolve beads dir: %v", err)
		return counts
	}

	socketPath := rpc.ShortSocketPath(absPath)
	client, err := rpc.TryConnect(socketPath)
	if err != nil || client == nil {
		// Daemon not running - return zeros
		return counts
	}
	defer client.Close()

	resp, err := client.GetWorkerStatus(&rpc.GetWorkerStatusArgs{})
	if err != nil || resp == nil {
		log.Printf("metrics: failed to get worker status: %v", err)
		return counts
	}

	for _, worker := range resp.Workers {
		switch worker.Status {
		case "in_progress", "active":
			counts["active"]++
		case "idle", "":
			counts["idle"]++
		case "blocked":
			counts["blocked"]++
		default:
			log.Printf("metrics: unknown worker status %q for %s", worker.Status, worker.Assignee)
		}
	}

	return counts
}

// writeJSON writes a JSON response.
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("Failed to encode JSON response: %v", err)
	}
}

// getWorkspaceInfo returns workspace metadata for API responses.
func getWorkspaceInfo() WorkspaceInfo {
	resolver, err := NewResolver()
	if err != nil || resolver.Mode() != ModeWorkspace {
		return WorkspaceInfo{Mode: "legacy"}
	}
	return WorkspaceInfo{
		Mode:       "workspace",
		Name:       resolver.WorkspaceName(),
		Workspaces: resolver.WorkspaceNames(),
	}
}

// groupAgentsByWorkspace groups agents by their workspace field.
func groupAgentsByWorkspace(agents []AgentStatus) map[string][]AgentStatus {
	groups := make(map[string][]AgentStatus)
	for _, agent := range agents {
		ws := agent.Workspace
		if ws == "" {
			ws = "(legacy)"
		}
		groups[ws] = append(groups[ws], agent)
	}
	return groups
}

func handleWorkspaces(w http.ResponseWriter, r *http.Request) {
	response := WorkspacesResponse{
		Workspaces: make(map[string]WorkspaceDetail),
		Timestamp:  time.Now(),
	}

	resolver, err := NewResolver()
	if err != nil || resolver.Mode() != ModeWorkspace {
		response.Mode = "legacy"
		writeJSON(w, response)
		return
	}

	response.Mode = "workspace"
	cfg, err := LoadConfig()
	if err != nil || cfg == nil {
		writeJSON(w, response)
		return
	}

	response.Default = cfg.DefaultWorkspace
	for name, ws := range cfg.Workspaces {
		repos := make([]string, len(ws.Repos))
		for i, repo := range ws.Repos {
			repos[i] = repo.Name
		}
		response.Workspaces[name] = WorkspaceDetail{
			Path:  ws.Path,
			Repos: repos,
		}
	}

	writeJSON(w, response)
}

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/spf13/cobra"
)

var (
	servePort       int
	serveCorsOrigin string

	// collectDataFunc is the function used to collect monitor data.
	// This is a package-level variable to allow tests to inject mock data.
	collectDataFunc = collectMonitorData
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start HTTP server for agent status API",
	Long: `Start an HTTP server that exposes agent status via REST API.

This server is designed to be consumed by web UIs (like beads-web-ui)
that want to display agent status and task information.

ENDPOINTS
  GET /health       Health check
  GET /api/status   Full dashboard data (agents, tasks, stats, sync)
  GET /api/agents   Just agent status
  GET /api/tasks    Task queue and lists
  GET /api/stats    Statistics (open/closed/completion)
  GET /api/sync     Sync status

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

	rootCmd.AddCommand(serveCmd)
}

func runServe(cmd *cobra.Command, args []string) {
	mux := http.NewServeMux()

	// Register routes
	mux.HandleFunc("GET /health", handleHealth)
	mux.HandleFunc("GET /api/status", handleStatus)
	mux.HandleFunc("GET /api/agents", handleAgents)
	mux.HandleFunc("GET /api/tasks", handleTasks)
	mux.HandleFunc("GET /api/stats", handleStats)
	mux.HandleFunc("GET /api/sync", handleSync)

	// Wrap with CORS middleware
	handler := corsMiddleware(serveCorsOrigin, mux)

	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", servePort),
		Handler:      handler,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	// Handle graceful shutdown
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-stop
		log.Println("Shutting down server...")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			log.Printf("Server shutdown error: %v", err)
		}
	}()

	log.Printf("Starting loom server on port %d", servePort)
	if serveCorsOrigin != "" {
		log.Printf("CORS enabled for origin: %s", serveCorsOrigin)
	}

	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		cmd.PrintErrf("Server error: %v\n", err)
		os.Exit(1)
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

// HealthResponse is the health check response.
type HealthResponse struct {
	Status    string    `json:"status"`
	Timestamp time.Time `json:"timestamp"`
}

// AgentsResponse wraps the agents list.
type AgentsResponse struct {
	Agents    []AgentStatus `json:"agents"`
	Timestamp time.Time     `json:"timestamp"`
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
	writeJSON(w, AgentsResponse{
		Agents:    data.Agents,
		Timestamp: data.Timestamp,
	})
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

// writeJSON writes a JSON response.
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("Failed to encode JSON response: %v", err)
	}
}

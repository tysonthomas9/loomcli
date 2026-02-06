package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/steveyegge/beads/examples/beads-web-ui/daemon"
	"github.com/steveyegge/beads/internal/rpc"
	"github.com/steveyegge/beads/internal/types"
)

// setupRoutes configures all HTTP routes for the server.
// defaultTerminalCmd is the command to run in terminal sessions when not specified via query parameter.
func setupRoutes(mux *http.ServeMux, pool *daemon.ConnectionPool, hub *SSEHub, getMutationsSince func(since int64) []rpc.MutationEvent, termManager *TerminalManager, defaultTerminalCmd string) {
	// Health check endpoint for load balancers and monitoring
	mux.HandleFunc("GET /health", handleHealth(pool))

	// API health endpoint that reports daemon connection status
	mux.HandleFunc("GET /api/health", handleAPIHealth(pool))

	// Stats endpoint for project statistics
	mux.HandleFunc("GET /api/stats", handleStats(pool))

	// SSE hub metrics endpoint
	mux.HandleFunc("GET /api/metrics", handleMetrics(hub))

	// Daemon status endpoint - exposes daemon configuration (auto-commit, auto-push, etc.)
	mux.HandleFunc("GET /api/daemon/status", handleDaemonStatus(pool))

	// Issue endpoints
	mux.HandleFunc("GET /api/issues/{id}", handleGetIssue(pool))
	mux.HandleFunc("GET /api/issues", handleListIssues(pool))
	mux.HandleFunc("POST /api/issues", handleCreateIssue(pool))
	mux.HandleFunc("PATCH /api/issues/{id}", handlePatchIssue(pool))
	mux.HandleFunc("POST /api/issues/{id}/close", handleCloseIssue(pool))
	mux.HandleFunc("POST /api/issues/{id}/comments", handleAddComment(pool))

	// Dependency management endpoints
	mux.HandleFunc("POST /api/issues/{id}/dependencies", handleAddDependency(pool))
	mux.HandleFunc("DELETE /api/issues/{id}/dependencies/{depId}", handleRemoveDependency(pool))

	// Ready endpoint for issues ready to work on
	mux.HandleFunc("GET /api/ready", handleReady(pool))

	// Blocked endpoint for issues with blocking dependencies
	mux.HandleFunc("GET /api/blocked", handleBlocked(pool))

	// Graph endpoint for dependency visualization
	mux.HandleFunc("GET /api/issues/graph", handleGraph(pool))

	// Server-Sent Events endpoint for real-time push notifications
	if hub != nil {
		mux.HandleFunc("GET /api/events", handleSSE(hub, getMutationsSince))
	}

	// Loom proxy for agent status endpoints (same-origin to avoid CORS/CSP issues)
	if loomProxy := newLoomProxy(); loomProxy != nil {
		mux.Handle("/api/loom/", loomProxy)
	}

	// Terminal WebSocket endpoint for real-time terminal relay
	if termManager != nil {
		mux.HandleFunc("GET /api/terminal/ws", handleTerminalWS(termManager, defaultTerminalCmd))
	}

	// Log streaming endpoints
	mux.HandleFunc("GET /api/agents/{name}/logs", handleGetAgentLog())
	mux.HandleFunc("GET /api/agents/{name}/logs/stream", handleAgentLogStream())
	mux.HandleFunc("GET /api/tasks/{id}/logs", handleListTaskPhases())
	mux.HandleFunc("GET /api/tasks/{id}/logs/{phase}", handleGetTaskLog())
	mux.HandleFunc("GET /api/tasks/{id}/logs/{phase}/stream", handleTaskLogStream())

	// Static file serving with SPA routing (must be last - catches all paths)
	mux.Handle("/", frontendHandler())
}

// handleHealth returns a simple health check response.
// This is for load balancers and basic monitoring - it doesn't check daemon connectivity.
func handleHealth(pool *daemon.ConnectionPool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(map[string]string{"status": "ok"}); err != nil {
			log.Printf("Failed to encode health response: %v", err)
		}
	}
}

// HealthStatus represents the detailed health status of the API.
type HealthStatus struct {
	Status string            `json:"status"`         // "ok", "degraded", "unhealthy"
	Daemon DaemonStatus      `json:"daemon"`         // Daemon connection status
	Pool   *daemon.PoolStats `json:"pool,omitempty"` // Connection pool stats
}

// DaemonStatus represents the daemon connection status.
type DaemonStatus struct {
	Connected bool    `json:"connected"`         // Whether we can connect to daemon
	Status    string  `json:"status,omitempty"`  // Daemon health status if connected
	Uptime    float64 `json:"uptime,omitempty"`  // Daemon uptime in seconds if connected
	Version   string  `json:"version,omitempty"` // Daemon version if connected
	Error     string  `json:"error,omitempty"`   // Error message if not connected
}

// handleAPIHealth returns a detailed health check including daemon connectivity.
func handleAPIHealth(pool *daemon.ConnectionPool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		status := HealthStatus{
			Status: "ok",
			Daemon: DaemonStatus{
				Connected: false,
			},
		}

		// Check daemon connection if pool is available
		if pool != nil {
			poolStats := pool.Stats()
			status.Pool = &poolStats

			// Try to get a connection and check daemon health
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()

			client, err := pool.Get(ctx)
			if err != nil {
				status.Status = "degraded"
				status.Daemon.Error = err.Error()
			} else {
				defer pool.Put(client)

				// Get daemon health
				health, err := client.Health()
				if err != nil {
					status.Status = "degraded"
					status.Daemon.Error = err.Error()
				} else {
					status.Daemon.Connected = true
					status.Daemon.Status = health.Status
					status.Daemon.Uptime = health.Uptime
					status.Daemon.Version = health.Version

					if health.Status == "unhealthy" {
						status.Status = "degraded"
						status.Daemon.Error = health.Error
					}
				}
			}
		} else {
			status.Status = "degraded"
			status.Daemon.Error = "connection pool not initialized"
		}

		w.Header().Set("Content-Type", "application/json")
		if status.Status == "ok" {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
		}

		if err := json.NewEncoder(w).Encode(status); err != nil {
			log.Printf("Failed to encode API health response: %v", err)
		}
	}
}

// StatsResponse wraps the statistics data for JSON response.
type StatsResponse struct {
	Success bool              `json:"success"`
	Data    *types.Statistics `json:"data,omitempty"`
	Error   string            `json:"error,omitempty"`
}

// statsClient is an internal interface for testing stats operations.
// The production code uses *rpc.Client which implements this interface.
type statsClient interface {
	Stats() (*rpc.Response, error)
}

// statsConnectionGetter is an internal interface for testing stats handler pool operations.
type statsConnectionGetter interface {
	Get(ctx context.Context) (statsClient, error)
	Put(client statsClient)
}

// statsPoolAdapter wraps *daemon.ConnectionPool to implement statsConnectionGetter.
type statsPoolAdapter struct {
	pool *daemon.ConnectionPool
}

func (p *statsPoolAdapter) Get(ctx context.Context) (statsClient, error) {
	return p.pool.Get(ctx)
}

func (p *statsPoolAdapter) Put(client statsClient) {
	if c, ok := client.(*rpc.Client); ok {
		p.pool.Put(c)
	}
}

// handleStats returns project statistics from the daemon.
func handleStats(pool *daemon.ConnectionPool) http.HandlerFunc {
	if pool == nil {
		return handleStatsWithPool(nil)
	}
	return handleStatsWithPool(&statsPoolAdapter{pool: pool})
}

// handleStatsWithPool is the internal implementation that accepts an interface for testing.
func handleStatsWithPool(pool statsConnectionGetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if pool == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			if err := json.NewEncoder(w).Encode(StatsResponse{
				Success: false,
				Error:   "connection pool not initialized",
			}); err != nil {
				log.Printf("Failed to encode stats response: %v", err)
			}
			return
		}

		// Acquire connection with timeout
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()

		client, err := pool.Get(ctx)
		if err != nil {
			status := http.StatusServiceUnavailable
			if errors.Is(err, context.DeadlineExceeded) {
				status = http.StatusGatewayTimeout
			}
			w.WriteHeader(status)
			if err := json.NewEncoder(w).Encode(StatsResponse{
				Success: false,
				Error:   err.Error(),
			}); err != nil {
				log.Printf("Failed to encode stats response: %v", err)
			}
			return
		}
		defer pool.Put(client)

		// Execute Stats RPC call
		resp, err := client.Stats()
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			if err := json.NewEncoder(w).Encode(StatsResponse{
				Success: false,
				Error:   fmt.Sprintf("rpc error: %v", err),
			}); err != nil {
				log.Printf("Failed to encode stats response: %v", err)
			}
			return
		}

		if !resp.Success {
			w.WriteHeader(http.StatusInternalServerError)
			if err := json.NewEncoder(w).Encode(StatsResponse{
				Success: false,
				Error:   resp.Error,
			}); err != nil {
				log.Printf("Failed to encode stats response: %v", err)
			}
			return
		}

		// Parse the statistics from RPC response
		var stats types.Statistics
		if err := json.Unmarshal(resp.Data, &stats); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			if err := json.NewEncoder(w).Encode(StatsResponse{
				Success: false,
				Error:   fmt.Sprintf("failed to parse stats: %v", err),
			}); err != nil {
				log.Printf("Failed to encode stats response: %v", err)
			}
			return
		}

		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(StatsResponse{
			Success: true,
			Data:    &stats,
		}); err != nil {
			log.Printf("Failed to encode stats response: %v", err)
		}
	}
}

// DaemonStatusResponse wraps the daemon status for JSON response.
type DaemonStatusResponse struct {
	Success bool                `json:"success"`
	Data    *rpc.StatusResponse `json:"data,omitempty"`
	Error   string              `json:"error,omitempty"`
}

// handleDaemonStatus returns the daemon's runtime configuration.
// This includes auto-commit, auto-push, auto-pull, local-mode, sync-interval, and daemon-mode.
func handleDaemonStatus(pool *daemon.ConnectionPool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if pool == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			if err := json.NewEncoder(w).Encode(DaemonStatusResponse{
				Success: false,
				Error:   "connection pool not initialized",
			}); err != nil {
				log.Printf("Failed to encode daemon status response: %v", err)
			}
			return
		}

		// Acquire connection with timeout
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()

		client, err := pool.Get(ctx)
		if err != nil {
			status := http.StatusServiceUnavailable
			if errors.Is(err, context.DeadlineExceeded) {
				status = http.StatusGatewayTimeout
			}
			w.WriteHeader(status)
			if err := json.NewEncoder(w).Encode(DaemonStatusResponse{
				Success: false,
				Error:   err.Error(),
			}); err != nil {
				log.Printf("Failed to encode daemon status response: %v", err)
			}
			return
		}
		defer pool.Put(client)

		// Get daemon status
		daemonStatus, err := client.Status()
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			if err := json.NewEncoder(w).Encode(DaemonStatusResponse{
				Success: false,
				Error:   fmt.Sprintf("rpc error: %v", err),
			}); err != nil {
				log.Printf("Failed to encode daemon status response: %v", err)
			}
			return
		}

		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(DaemonStatusResponse{
			Success: true,
			Data:    daemonStatus,
		}); err != nil {
			log.Printf("Failed to encode daemon status response: %v", err)
		}
	}
}

package webui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/tysonthomas9/loomcli/internal/circuitbreaker"
	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/types"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
	"github.com/tysonthomas9/loomcli/internal/webui/fleet"
)

// setupRoutes configures all HTTP routes for the server.
// defaultTerminalCmd is the command to run in terminal sessions.
// allowedOrigins is the list of allowed CORS origins for WebSocket validation.
func setupRoutes(mux *http.ServeMux, pool daemon.Pool, hub *SSEHub, getMutationsSince func(since int64) []rpc.MutationEvent, termManager *TerminalManager, defaultTerminalCmd string, termAuth *terminalAuth, fleetStore *fleet.Store, tokenCfg *TokenConfig, apiKey string, authEnabled bool, allowedOrigins []string, fleetRegCfg *FleetRegisterConfig, timeoutEnforcer *fleet.TimeoutEnforcer, claimMetrics *fleet.ClaimMetrics, fleetEnabled bool, devMode bool, devFrontendDir string, loomServerURL string) {
	// Health check endpoint for load balancers and monitoring
	mux.HandleFunc("GET /health", handleHealth(pool))

	// API health endpoint that reports daemon connection status
	mux.HandleFunc("GET /api/health", handleAPIHealth(pool))

	// Auth token bootstrap endpoint (same-origin only)
	if authEnabled {
		mux.HandleFunc("GET /api/auth/token", handleAuthToken(apiKey))
	}

	// Stats endpoint for project statistics
	mux.HandleFunc("GET /api/stats", handleStats(pool))

	// SSE hub metrics endpoint
	mux.HandleFunc("GET /api/metrics", handleMetrics(hub, timeoutEnforcer, claimMetrics))

	// Daemon status endpoint - exposes daemon configuration (auto-commit, auto-push, etc.)
	mux.HandleFunc("GET /api/daemon/status", handleDaemonStatus(pool))

	// Backend configuration endpoints
	mux.HandleFunc("GET /api/config/backend", handleGetBackendConfig(pool))
	mux.HandleFunc("PATCH /api/config/backend", handlePatchBackendConfig(pool))

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

	// Fleet endpoints for worker registration, task acquisition, and completion
	// Only registered when fleet coordination (Redis) is configured.
	if fleetEnabled {
		mux.HandleFunc("POST /api/fleet/register", handleFleetRegister(fleetStore, tokenCfg, fleetRegCfg))
		if tokenCfg != nil && len(tokenCfg.SigningKey) > 0 {
			fleetAuth := NewFleetAuthMiddleware(tokenCfg.SigningKey)
			mux.Handle("POST /api/fleet/claim", fleetAuth(handleFleetClaim(pool, claimMetrics)))
		} else {
			mux.HandleFunc("POST /api/fleet/claim", handleFleetClaim(pool, claimMetrics))
		}
		mux.HandleFunc("POST /api/fleet/done/{id}", handleFleetDone(fleetStore))
		if tokenCfg != nil && len(tokenCfg.SigningKey) > 0 {
			fleetAuth := NewFleetAuthMiddleware(tokenCfg.SigningKey)
			mux.Handle("POST /api/fleet/heartbeat", fleetAuth(handleFleetHeartbeat(fleetStore)))
		} else {
			mux.HandleFunc("POST /api/fleet/heartbeat", handleFleetHeartbeat(fleetStore))
		}
	}

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
	if loomProxy := newLoomProxy(loomServerURL); loomProxy != nil {
		mux.Handle("/api/loom/", loomProxy)
	}

	// Terminal token and WebSocket endpoints for authenticated terminal relay
	if termManager != nil {
		if termAuth != nil {
			mux.HandleFunc("GET /api/terminal/token", handleTerminalToken(termAuth))
			mux.HandleFunc("GET /api/agents/{name}/terminal/token", handleGetAgentTerminalToken(termAuth))
		}
		mux.HandleFunc("GET /api/terminal/ws", handleTerminalWS(termManager, defaultTerminalCmd, termAuth, allowedOrigins))
		mux.HandleFunc("GET /api/agents/{name}/terminal/ws", handleAgentTerminalWS(termManager, termAuth, allowedOrigins))
		mux.HandleFunc("GET /api/agents/{name}/terminal/info", handleGetAgentTerminalInfo(termManager))
		mux.HandleFunc("POST /api/terminal/restart", handleTerminalRestart(termManager, pool, termAuth))
	}

	// Log streaming endpoints
	mux.HandleFunc("GET /api/agents/{name}/logs", handleGetAgentLog())
	mux.HandleFunc("GET /api/tasks/{id}/logs", handleListTaskPhases())
	mux.HandleFunc("GET /api/tasks/{id}/logs/{phase}", handleGetTaskLog())

	// Static file serving with SPA routing (must be last - catches all paths)
	if devMode {
		mux.Handle("/", devFrontendHandler(devFrontendDir))
	} else {
		mux.Handle("/", frontendHandler())
	}
}

// handleHealth returns a simple health check response.
// This is for load balancers and basic monitoring - it doesn't check daemon connectivity.
func handleHealth(pool daemon.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		respondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

// CircuitBreakerStatus represents the circuit breaker state in health responses.
type CircuitBreakerStatus struct {
	State           string    `json:"state"`
	FailureCount    int       `json:"failure_count"`
	LastStateChange time.Time `json:"last_state_change"`
}

// HealthStatus represents the detailed health status of the API.
type HealthStatus struct {
	Status         string                `json:"status"`                    // "ok", "degraded", "unhealthy"
	Daemon         DaemonStatus          `json:"daemon"`                    // Daemon connection status
	Pool           *daemon.PoolStats     `json:"pool,omitempty"`            // Connection pool stats
	CircuitBreaker *CircuitBreakerStatus `json:"circuit_breaker,omitempty"` // Circuit breaker state
}

// breakerStater is an optional interface for pools that have a circuit breaker.
type breakerStater interface {
	BreakerState() circuitbreaker.State
	BreakerStats() circuitbreaker.BreakerStats
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
func handleAPIHealth(pool daemon.Pool) http.HandlerFunc {
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

			// Include circuit breaker state if available
			if bs, ok := pool.(breakerStater); ok {
				stats := bs.BreakerStats()
				status.CircuitBreaker = &CircuitBreakerStatus{
					State:           stats.State.String(),
					FailureCount:    stats.ConsecutiveFail,
					LastStateChange: stats.LastStateChange,
				}
			}

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

		httpStatus := http.StatusOK
		if status.Status != "ok" {
			httpStatus = http.StatusServiceUnavailable
		}
		respondJSON(w, httpStatus, status)
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

// statsPoolAdapter wraps daemon.Pool to implement statsConnectionGetter.
type statsPoolAdapter struct {
	pool daemon.Pool
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
func handleStats(pool daemon.Pool) http.HandlerFunc {
	if pool == nil {
		return handleStatsWithPool(nil)
	}
	return handleStatsWithPool(&statsPoolAdapter{pool: pool})
}

// handleStatsWithPool is the internal implementation that accepts an interface for testing.
func handleStatsWithPool(pool statsConnectionGetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if pool == nil {
			respondJSON(w, http.StatusServiceUnavailable, StatsResponse{
				Success: false,
				Error:   "connection pool not initialized",
			})
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
			respondJSON(w, status, StatsResponse{Success: false, Error: err.Error()})
			return
		}
		defer pool.Put(client)

		// Execute Stats RPC call
		resp, err := client.Stats()
		if err != nil {
			respondJSON(w, http.StatusInternalServerError, StatsResponse{
				Success: false,
				Error:   fmt.Sprintf("rpc error: %v", err),
			})
			return
		}

		if !resp.Success {
			respondJSON(w, http.StatusInternalServerError, StatsResponse{
				Success: false,
				Error:   resp.Error,
			})
			return
		}

		// Parse the statistics from RPC response
		var stats types.Statistics
		if err := json.Unmarshal(resp.Data, &stats); err != nil {
			respondJSON(w, http.StatusInternalServerError, StatsResponse{
				Success: false,
				Error:   fmt.Sprintf("failed to parse stats: %v", err),
			})
			return
		}

		respondJSON(w, http.StatusOK, StatsResponse{Success: true, Data: &stats})
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
func handleDaemonStatus(pool daemon.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if pool == nil {
			respondJSON(w, http.StatusServiceUnavailable, DaemonStatusResponse{
				Success: false,
				Error:   "connection pool not initialized",
			})
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
			respondJSON(w, status, DaemonStatusResponse{Success: false, Error: err.Error()})
			return
		}
		defer pool.Put(client)

		// Get daemon status
		daemonStatus, err := client.Status()
		if err != nil {
			respondJSON(w, http.StatusInternalServerError, DaemonStatusResponse{
				Success: false,
				Error:   fmt.Sprintf("rpc error: %v", err),
			})
			return
		}

		respondJSON(w, http.StatusOK, DaemonStatusResponse{Success: true, Data: daemonStatus})
	}
}

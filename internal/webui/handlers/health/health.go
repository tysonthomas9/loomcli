package health

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
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

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

// SSEMetrics represents the runtime metrics for the SSE hub.
type SSEMetrics struct {
	ConnectedClients     int     `json:"connected_clients"`
	DroppedMutations     int64   `json:"dropped_mutations"`
	RetryQueueDepth      int     `json:"retry_queue_depth"`
	UptimeSeconds        float64 `json:"uptime_seconds"`
	FleetTimeoutsTotal   int64   `json:"loom_fleet_timeouts_total,omitempty"`
	FleetClaimsSuccess   int64   `json:"loom_fleet_claims_success,omitempty"`
	FleetClaimsCollision int64   `json:"loom_fleet_claims_collision,omitempty"`
	FleetClaimsTimeout   int64   `json:"loom_fleet_claims_timeout,omitempty"`
	FleetClaimsTotal     int64   `json:"loom_fleet_claims_total,omitempty"`
}

// MetricsResponse wraps the SSE hub metrics for JSON response.
type MetricsResponse struct {
	Success bool        `json:"success"`
	Data    *SSEMetrics `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}

// StatsResponse wraps the statistics data for JSON response.
type StatsResponse struct {
	Success bool              `json:"success"`
	Data    *types.Statistics `json:"data,omitempty"`
	Error   string            `json:"error,omitempty"`
}

// StatsClient is an interface for testing stats operations.
// The production code uses *rpc.Client which implements this interface.
type StatsClient interface {
	Stats() (*rpc.Response, error)
}

// StatsConnectionGetter is an interface for testing stats handler pool operations.
type StatsConnectionGetter interface {
	Get(ctx context.Context) (StatsClient, error)
	Put(client StatsClient)
	Discard(client StatsClient)
}

// statsPoolAdapter wraps daemon.Pool to implement StatsConnectionGetter.
type statsPoolAdapter struct {
	pool daemon.Pool
}

func (p *statsPoolAdapter) Get(ctx context.Context) (StatsClient, error) {
	return p.pool.Get(ctx)
}

func (p *statsPoolAdapter) Put(client StatsClient) {
	if c, ok := client.(*rpc.Client); ok {
		p.pool.Put(c)
	}
}

func (p *statsPoolAdapter) Discard(client StatsClient) {
	if c, ok := client.(*rpc.Client); ok {
		p.pool.Discard(c)
	}
}

// DaemonStatusResponse wraps the daemon status for JSON response.
type DaemonStatusResponse struct {
	Success bool                `json:"success"`
	Data    *rpc.StatusResponse `json:"data,omitempty"`
	Error   string              `json:"error,omitempty"`
}

// HandleHealth returns a simple health check response.
// This is for load balancers and basic monitoring - it doesn't check daemon connectivity.
func HandleHealth(pool daemon.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		handler.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

// HandleAPIHealth returns a detailed health check including daemon connectivity.
func HandleAPIHealth(pool daemon.Pool) http.HandlerFunc { //nolint:funlen
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
				rpcOK := false
				defer func() {
					if rpcOK {
						pool.Put(client)
					} else {
						pool.Discard(client)
					}
				}()

				// Get daemon health
				health, err := client.Health()
				if err != nil {
					status.Status = "degraded"
					status.Daemon.Error = err.Error()
				} else {
					rpcOK = true
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
		handler.WriteJSON(w, httpStatus, status)
	}
}

// HandleStats returns project statistics from the daemon.
func HandleStats(pool daemon.Pool) http.HandlerFunc {
	if pool == nil {
		return HandleStatsWithPool(nil)
	}
	return HandleStatsWithPool(&statsPoolAdapter{pool: pool})
}

// HandleStatsWithPool is the implementation that accepts an interface for testing.
func HandleStatsWithPool(pool StatsConnectionGetter) http.HandlerFunc { //nolint:funlen
	return func(w http.ResponseWriter, r *http.Request) {
		if pool == nil {
			handler.WriteJSON(w, http.StatusServiceUnavailable, StatsResponse{
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
			handler.WriteJSON(w, status, StatsResponse{Success: false, Error: err.Error()})
			return
		}
		rpcOK := false
		defer func() {
			if rpcOK {
				pool.Put(client)
			} else {
				pool.Discard(client)
			}
		}()

		// Execute Stats RPC call
		resp, err := client.Stats()
		if err != nil {
			handler.WriteJSON(w, http.StatusInternalServerError, StatsResponse{
				Success: false,
				Error:   fmt.Sprintf("rpc error: %v", err),
			})
			return
		}
		rpcOK = true

		if !resp.Success {
			handler.WriteJSON(w, http.StatusInternalServerError, StatsResponse{
				Success: false,
				Error:   resp.Error,
			})
			return
		}

		// Parse the statistics from RPC response
		var stats types.Statistics
		if err := json.Unmarshal(resp.Data, &stats); err != nil {
			handler.WriteJSON(w, http.StatusInternalServerError, StatsResponse{
				Success: false,
				Error:   fmt.Sprintf("failed to parse stats: %v", err),
			})
			return
		}

		handler.WriteJSON(w, http.StatusOK, StatsResponse{Success: true, Data: &stats})
	}
}

// HandleMetrics returns a handler that exposes SSE hub runtime metrics.
// getFleetTimeouts returns the aggregate fleet timeout count (nil = fleet disabled).
func HandleMetrics(hub *realtime.Hub, getFleetTimeouts func() int64, claimMetrics *fleet.ClaimMetrics) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if hub == nil {
			handler.WriteJSON(w, http.StatusServiceUnavailable, MetricsResponse{
				Success: false,
				Error:   "SSE hub not initialized",
			})
			return
		}
		metrics := &SSEMetrics{
			ConnectedClients: hub.ClientCount(),
			DroppedMutations: hub.GetDroppedCount(),
			RetryQueueDepth:  hub.GetRetryQueueDepth(),
			UptimeSeconds:    hub.GetUptime().Seconds(),
		}
		if getFleetTimeouts != nil {
			metrics.FleetTimeoutsTotal = getFleetTimeouts()
		}
		if claimMetrics != nil {
			snap := claimMetrics.Snapshot()
			metrics.FleetClaimsSuccess = snap.Success
			metrics.FleetClaimsCollision = snap.Collision
			metrics.FleetClaimsTimeout = snap.Timeout
			metrics.FleetClaimsTotal = snap.Total
		}
		handler.WriteJSON(w, http.StatusOK, MetricsResponse{
			Success: true,
			Data:    metrics,
		})
	}
}

// HandleDaemonStatus returns the daemon's runtime configuration.
// This includes auto-commit, auto-push, auto-pull, local-mode, sync-interval, and daemon-mode.
func HandleDaemonStatus(pool daemon.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if pool == nil {
			handler.WriteJSON(w, http.StatusServiceUnavailable, DaemonStatusResponse{
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
			handler.WriteJSON(w, status, DaemonStatusResponse{Success: false, Error: err.Error()})
			return
		}
		rpcOK := false
		defer func() {
			if rpcOK {
				pool.Put(client)
			} else {
				pool.Discard(client)
			}
		}()

		// Get daemon status
		daemonStatus, err := client.Status()
		if err != nil {
			handler.WriteJSON(w, http.StatusInternalServerError, DaemonStatusResponse{
				Success: false,
				Error:   fmt.Sprintf("rpc error: %v", err),
			})
			return
		}
		rpcOK = true

		handler.WriteJSON(w, http.StatusOK, DaemonStatusResponse{Success: true, Data: daemonStatus})
	}
}

package health

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/circuitbreaker"
	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/types"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
	"github.com/tysonthomas9/loomcli/internal/webui/fleet"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

// IssueBackendFn returns the active backend.IssueBackend or nil. Used by
// HandleStatsWithBackend to serve stats in pool-less fleet mode.
//
// ctx carries the per-request workspace ID so cloud-mode wirings can route
// to a per-workspace fleet-db backend.
type IssueBackendFn func(ctx context.Context) backend.IssueBackend

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

// HandleHealth returns a simple health check response.
// This is for load balancers and basic monitoring - it doesn't check daemon connectivity.
func HandleHealth(pool daemon.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		handler.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

// HandleAPIHealth returns a detailed health check including daemon
// connectivity. Treats the pool as the source of truth: if it can't be
// reached the response flips to 503 so liveness probes restart the pod.
// Use the *NoDaemon* variant for deployments where no daemon is expected
// (fleet mode); 503 there would falsely page on a healthy server.
func HandleAPIHealth(pool daemon.Pool) http.HandlerFunc {
	return handleAPIHealthImpl(pool, true)
}

// HandleAPIHealthNoDaemon is the fleet-mode variant. Skips the pool dial
// entirely (no local issue daemon to talk to in this deployment) and returns 200
// with daemon.connected=false. Avoids both the false-positive 503 and the
// 2s pool.Get() that would otherwise burn on every probe.
func HandleAPIHealthNoDaemon() http.HandlerFunc {
	return handleAPIHealthImpl(nil, false)
}

func handleAPIHealthImpl(pool daemon.Pool, daemonExpected bool) http.HandlerFunc { //nolint:funlen
	return func(w http.ResponseWriter, r *http.Request) {
		status := HealthStatus{
			Status: "ok",
			Daemon: DaemonStatus{Connected: false},
		}

		if !daemonExpected || pool == nil {
			handler.WriteJSON(w, http.StatusOK, status)
			return
		}

		poolStats := pool.Stats()
		status.Pool = &poolStats

		if bs, ok := pool.(breakerStater); ok {
			stats := bs.BreakerStats()
			status.CircuitBreaker = &CircuitBreakerStatus{
				State:           stats.State.String(),
				FailureCount:    stats.ConsecutiveFail,
				LastStateChange: stats.LastStateChange,
			}
		}

		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()

		client, err := pool.Get(ctx)
		if err != nil {
			if errors.Is(err, daemon.ErrDaemonStarting) {
				status.Status = "starting"
				status.Daemon.Error = "daemon is starting up"
			} else {
				status.Status = "degraded"
				status.Daemon.Error = err.Error()
			}
		} else {
			rpcOK := false
			defer func() {
				if rpcOK {
					pool.Put(client)
				} else {
					pool.Discard(client)
				}
			}()

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

		httpStatus := http.StatusOK
		if status.Status != "ok" && status.Status != "starting" {
			httpStatus = http.StatusServiceUnavailable
		}
		handler.WriteJSON(w, httpStatus, status)
	}
}

// HandleStats returns project statistics from the daemon.
func HandleStats(pool daemon.Pool) http.HandlerFunc {
	return HandleStatsWithBackend(pool, nil)
}

// HandleStatsWithBackend returns a handler that serves /stats from exactly one
// configured source: the daemon pool when present, otherwise the IssueBackend
// for pool-less fleet mode. backendFn may be nil — in that case behavior is
// identical to the pool-only path.
func HandleStatsWithBackend(pool daemon.Pool, backendFn IssueBackendFn) http.HandlerFunc {
	var poolAdapter StatsConnectionGetter
	if pool != nil {
		poolAdapter = &statsPoolAdapter{pool: pool}
	}
	poolHandler := HandleStatsWithPool(poolAdapter)
	if poolAdapter != nil || backendFn == nil {
		return poolHandler
	}
	return func(w http.ResponseWriter, r *http.Request) {
		serveStatsViaBackend(w, r, backendFn)
	}
}

// serveStatsViaBackend writes a /stats response sourced from the
// IssueBackend.Stats() projection rather than the daemon RPC pool.
func serveStatsViaBackend(w http.ResponseWriter, r *http.Request, backendFn IssueBackendFn) {
	be := backendFn(r.Context())
	if be == nil {
		handler.WriteJSON(w, http.StatusServiceUnavailable, StatsResponse{
			Success: false,
			Error:   "issue backend not configured",
		})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	data, err := be.Stats(ctx)
	if err != nil {
		handler.WriteJSON(w, http.StatusInternalServerError, StatsResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}
	stats := &types.Statistics{
		TotalIssues:             data.TotalIssues,
		OpenIssues:              data.OpenIssues,
		InProgressIssues:        data.InProgressIssues,
		ClosedIssues:            data.ClosedIssues,
		BlockedIssues:           data.BlockedIssues,
		DeferredIssues:          data.DeferredIssues,
		ReadyIssues:             data.ReadyIssues,
		ReviewIssues:            data.ReviewIssues,
		StatusBlockedIssues:     data.StatusBlockedIssues,
		TombstoneIssues:         data.TombstoneIssues,
		PinnedIssues:            data.PinnedIssues,
		EpicsEligibleForClosure: data.EpicsEligibleForClosure,
	}
	handler.WriteJSON(w, http.StatusOK, StatsResponse{Success: true, Data: stats})
}

// HandleStatsWithPool is the implementation that accepts an interface for testing.
//
// This path unmarshals whatever the daemon RPC returns. OpStats has no
// server-side handler in v5, so it is legacy: review_issues and
// status_blocked_issues simply default to zero here, which is accepted.
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

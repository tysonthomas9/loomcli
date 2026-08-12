package health

import (
	"context"
	"net/http"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
	"github.com/tysonthomas9/loomcli/internal/webui/fleet"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

// HealthStatus represents the detailed health status of the API.
type HealthStatus struct {
	Status string `json:"status"`
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
	Success bool             `json:"success"`
	Data    *workitems.Stats `json:"data,omitempty"`
	Error   string           `json:"error,omitempty"`
}

// HandleHealth returns the process liveness response.
func HandleHealth() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		handler.WriteJSON(w, http.StatusOK, HealthStatus{Status: "ok"})
	}
}

// HandleAPIHealth returns API process health. Dependency readiness is exposed
// by the workspace-scoped readyz endpoint through owned capability ports.
func HandleAPIHealth() http.HandlerFunc { return HandleHealth() }

// HandleStats serves workspace statistics through the Work Items owner.
func HandleStats(queries workitems.StatsQueries) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		serveStats(w, r, queries)
	}
}

// serveStats writes the Work Items-owned /stats projection.
func serveStats(w http.ResponseWriter, r *http.Request, queries workitems.StatsQueries) {
	if queries == nil {
		handler.WriteJSON(w, http.StatusServiceUnavailable, StatsResponse{
			Success: false,
			Error:   "Work Items service not configured",
		})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	data, err := queries.Stats(ctx)
	if err != nil || data == nil {
		message := "Work Items service returned no statistics"
		if err != nil {
			message = err.Error()
		}
		handler.WriteJSON(w, http.StatusInternalServerError, StatsResponse{
			Success: false,
			Error:   message,
		})
		return
	}
	handler.WriteJSON(w, http.StatusOK, StatsResponse{Success: true, Data: data})
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

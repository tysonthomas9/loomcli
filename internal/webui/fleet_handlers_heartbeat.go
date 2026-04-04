package webui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/tysonthomas9/loomcli/internal/webui/fleet"
)

// HeartbeatRequest represents the POST body for the heartbeat endpoint.
type HeartbeatRequest struct {
	WorkerID string `json:"worker_id"`
}

// HeartbeatResponse wraps the heartbeat result for JSON response.
type HeartbeatResponse struct {
	Success       bool   `json:"success"`
	LastHeartbeat string `json:"last_heartbeat,omitempty"` // RFC 3339 timestamp
	Error         string `json:"error,omitempty"`
}

// heartbeatStore is an internal interface for testing heartbeat operations.
type heartbeatStore interface {
	UpdateHeartbeat(ctx context.Context, workerID string) (time.Time, error)
}

// handleFleetHeartbeat returns a handler that processes worker heartbeats.
func handleFleetHeartbeat(store *fleet.Store) http.HandlerFunc {
	return handleFleetHeartbeatWithStore(store)
}

// handleFleetHeartbeatWithStore is the internal implementation that accepts an interface for testing.
func handleFleetHeartbeatWithStore(store heartbeatStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Check store availability
		if store == nil {
			respondJSON(w, http.StatusServiceUnavailable, HeartbeatResponse{
				Success: false,
				Error:   "fleet store not initialized",
			})
			return
		}

		// Parse request body
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
		var req HeartbeatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				respondJSON(w, http.StatusRequestEntityTooLarge, HeartbeatResponse{
					Success: false,
					Error:   "request body too large (max 1MB)",
				})
				return
			}
			logger.Warn("invalid request body", "handler", "handleFleetHeartbeat", "err", err)
			respondJSON(w, http.StatusBadRequest, HeartbeatResponse{
				Success: false,
				Error:   "invalid request body",
			})
			return
		}

		// Validate worker_id
		if req.WorkerID == "" {
			respondJSON(w, http.StatusBadRequest, HeartbeatResponse{
				Success: false,
				Error:   "worker_id is required",
			})
			return
		}

		if len(req.WorkerID) > maxWorkerIDLength {
			respondJSON(w, http.StatusBadRequest, HeartbeatResponse{
				Success: false,
				Error:   fmt.Sprintf("worker_id exceeds maximum length of %d characters", maxWorkerIDLength),
			})
			return
		}

		// Update heartbeat with timeout
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()

		lastHeartbeat, err := store.UpdateHeartbeat(ctx, req.WorkerID)
		if err != nil {
			if errors.Is(err, fleet.ErrWorkerNotFound) {
				respondJSON(w, http.StatusNotFound, HeartbeatResponse{
					Success: false,
					Error:   "worker not found: " + req.WorkerID,
				})
				return
			}
			logger.Error("failed to update heartbeat", "worker_id", req.WorkerID, "err", err)
			respondJSON(w, http.StatusInternalServerError, HeartbeatResponse{
				Success: false,
				Error:   "failed to update heartbeat",
			})
			return
		}

		respondJSON(w, http.StatusOK, HeartbeatResponse{
			Success:       true,
			LastHeartbeat: lastHeartbeat.Format(time.RFC3339),
		})
	}
}

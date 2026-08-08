package fleet

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
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
func handleFleetHeartbeat(store *Store) http.HandlerFunc {
	return handleFleetHeartbeatWithStore(store)
}

// handleFleetHeartbeatWithStore is the internal implementation that accepts an interface for testing.
func handleFleetHeartbeatWithStore(store heartbeatStore) http.HandlerFunc { //nolint:funlen
	return func(w http.ResponseWriter, r *http.Request) {
		// Check store availability
		if store == nil {
			handler.WriteJSON(w, http.StatusServiceUnavailable, HeartbeatResponse{
				Success: false,
				Error:   "fleet store not initialized",
			})
			return
		}

		// Parse request body.
		var req HeartbeatRequest
		if err := handler.DecodeOneJSON(w, r, &req, handler.JSONDecodeOptions{MaxBytes: handler.MaxRequestBody}); err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				handler.WriteJSON(w, http.StatusRequestEntityTooLarge, HeartbeatResponse{
					Success: false,
					Error:   "request body too large (max 1MB)",
				})
				return
			}
			slog.Warn("invalid request body", "handler", "handleFleetHeartbeat", "err", err)
			handler.WriteJSON(w, http.StatusBadRequest, HeartbeatResponse{
				Success: false,
				Error:   "invalid request body",
			})
			return
		}

		// Validate worker_id
		if req.WorkerID == "" {
			handler.WriteJSON(w, http.StatusBadRequest, HeartbeatResponse{
				Success: false,
				Error:   "worker_id is required",
			})
			return
		}

		if len(req.WorkerID) > maxWorkerIDLength {
			handler.WriteJSON(w, http.StatusBadRequest, HeartbeatResponse{
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
			if errors.Is(err, ErrWorkerNotFound) {
				handler.WriteJSON(w, http.StatusNotFound, HeartbeatResponse{
					Success: false,
					Error:   "worker not found: " + req.WorkerID,
				})
				return
			}
			slog.Error("failed to update heartbeat", "worker_id", req.WorkerID, "err", err)
			handler.WriteJSON(w, http.StatusInternalServerError, HeartbeatResponse{
				Success: false,
				Error:   "failed to update heartbeat",
			})
			return
		}

		handler.WriteJSON(w, http.StatusOK, HeartbeatResponse{
			Success:       true,
			LastHeartbeat: lastHeartbeat.Format(time.RFC3339),
		})
	}
}

package fleet

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
)

// FleetDoneRequest is the POST body for task completion.
type FleetDoneRequest struct {
	Success   bool   `json:"success"`              // true = task succeeded, false = failed
	CommitSHA string `json:"commit_sha,omitempty"` // Git commit SHA (for successful tasks)
	Error     string `json:"error,omitempty"`      // Error message (for failed tasks)
}

// FleetDoneResponse wraps the done operation result.
type FleetDoneResponse struct {
	Success  bool   `json:"success"`
	TaskID   string `json:"task_id,omitempty"`   // The released task ID
	WorkerID string `json:"worker_id,omitempty"` // Confirmation of worker ID
	Error    string `json:"error,omitempty"`
}

// fleetDoneStore defines the store operations needed by the done handler.
type fleetDoneStore interface {
	GetWorker(ctx context.Context, workerID string) (*Worker, error)
	GetWorkerClaim(ctx context.Context, workerID string) (*ClaimResponse, error)
	RecordTaskResult(ctx context.Context, result *TaskResult) error
	ReleaseClaim(ctx context.Context, taskID string) error
	ClearWorkerClaim(ctx context.Context, workerID string) error
}

// handleFleetDone returns a handler that completes a task for a fleet worker.
func handleFleetDone(store *Store) http.HandlerFunc {
	return handleFleetDoneWithStore(store)
}

// handleFleetDoneWithStore is the internal implementation that accepts an interface for testing.
func handleFleetDoneWithStore(store fleetDoneStore) http.HandlerFunc { //nolint:funlen
	return func(w http.ResponseWriter, r *http.Request) {
		// Check store availability
		if store == nil {
			handler.WriteJSON(w, http.StatusServiceUnavailable, FleetDoneResponse{
				Success: false,
				Error:   "fleet API not available",
			})
			return
		}

		// Extract worker ID from path
		workerID := r.PathValue("id")
		if workerID == "" {
			handler.WriteJSON(w, http.StatusBadRequest, FleetDoneResponse{
				Success: false,
				Error:   "missing worker ID",
			})
			return
		}

		// Parse request body.
		var req FleetDoneRequest
		if err := handler.DecodeOneJSON(w, r, &req, handler.JSONDecodeOptions{MaxBytes: handler.MaxRequestBody}); err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				handler.WriteJSON(w, http.StatusRequestEntityTooLarge, FleetDoneResponse{
					Success: false,
					Error:   "request body too large (max 1MB)",
				})
				return
			}
			slog.Warn("invalid request body", "handler", "handleFleetDone", "err", err)
			handler.WriteJSON(w, http.StatusBadRequest, FleetDoneResponse{
				Success: false,
				Error:   "invalid request body",
			})
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		// Validate worker exists
		worker, err := store.GetWorker(ctx, workerID)
		if err != nil {
			safeID := strconv.Quote(workerID)
			slog.Error("failed to get worker", "worker_id", safeID, "err", err)
			handler.WriteJSON(w, http.StatusInternalServerError, FleetDoneResponse{
				Success: false,
				Error:   "failed to look up worker",
			})
			return
		}
		if worker == nil {
			handler.WriteJSON(w, http.StatusNotFound, FleetDoneResponse{
				Success: false,
				Error:   fmt.Sprintf("worker not found: %s", workerID),
			})
			return
		}

		// Look up worker's current claim
		claim, err := store.GetWorkerClaim(ctx, workerID)
		if err != nil {
			safeID := strconv.Quote(workerID)
			slog.Error("failed to get worker claim", "worker_id", safeID, "err", err)
			handler.WriteJSON(w, http.StatusInternalServerError, FleetDoneResponse{
				Success: false,
				Error:   "failed to look up worker claim",
			})
			return
		}

		// Idempotent: if worker has no claim, task was already completed
		if claim == nil {
			handler.WriteJSON(w, http.StatusOK, FleetDoneResponse{
				Success:  true,
				WorkerID: workerID,
			})
			return
		}

		taskID := claim.TaskID

		// Record task result before releasing (ensures we capture outcome)
		result := &TaskResult{
			WorkerID:    workerID,
			TaskID:      taskID,
			Success:     req.Success,
			CommitSHA:   req.CommitSHA,
			Error:       req.Error,
			CompletedAt: time.Now(),
		}
		if err := store.RecordTaskResult(ctx, result); err != nil {
			safeWorker, safeTask := strconv.Quote(workerID), strconv.Quote(taskID)
			slog.Error("failed to record task result", "worker_id", safeWorker, "task_id", safeTask, "err", err)
			handler.WriteJSON(w, http.StatusInternalServerError, FleetDoneResponse{
				Success: false,
				Error:   "failed to record task result",
			})
			return
		}

		// Release the claim
		if err := store.ReleaseClaim(ctx, taskID); err != nil {
			slog.Error("failed to release claim", "task_id", taskID, "err", err)
			handler.WriteJSON(w, http.StatusInternalServerError, FleetDoneResponse{
				Success: false,
				Error:   "failed to release task claim",
			})
			return
		}

		// Clean up worker claim cache (best effort, use fresh context since ctx may be nearly expired)
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cleanupCancel()
		if err := store.ClearWorkerClaim(cleanupCtx, workerID); err != nil {
			slog.Warn("failed to clear worker claim cache", "worker_id", workerID, "err", err)
		}

		slog.Info("task completed", "worker_id", workerID, "task_id", taskID, "success", req.Success)
		handler.WriteJSON(w, http.StatusOK, FleetDoneResponse{
			Success:  true,
			TaskID:   taskID,
			WorkerID: workerID,
		})
	}
}

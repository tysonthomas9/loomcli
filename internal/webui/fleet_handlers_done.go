package webui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/tysonthomas9/loomcli/internal/webui/fleet"
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
	GetWorker(ctx context.Context, workerID string) (*fleet.Worker, error)
	GetWorkerClaim(ctx context.Context, workerID string) (*fleet.ClaimResponse, error)
	RecordTaskResult(ctx context.Context, result *fleet.TaskResult) error
	ReleaseClaim(ctx context.Context, taskID string) error
	ClearWorkerClaim(ctx context.Context, workerID string) error
}

// handleFleetDone returns a handler that completes a task for a fleet worker.
func handleFleetDone(store *fleet.Store) http.HandlerFunc {
	return handleFleetDoneWithStore(store)
}

// handleFleetDoneWithStore is the internal implementation that accepts an interface for testing.
func handleFleetDoneWithStore(store fleetDoneStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Check store availability
		if store == nil {
			respondJSON(w, http.StatusServiceUnavailable, FleetDoneResponse{
				Success: false,
				Error:   "fleet API not available",
			})
			return
		}

		// Extract worker ID from path
		workerID := r.PathValue("id")
		if workerID == "" {
			respondJSON(w, http.StatusBadRequest, FleetDoneResponse{
				Success: false,
				Error:   "missing worker ID",
			})
			return
		}

		// Parse request body
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
		var req FleetDoneRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				respondJSON(w, http.StatusRequestEntityTooLarge, FleetDoneResponse{
					Success: false,
					Error:   "request body too large (max 1MB)",
				})
				return
			}
			log.Printf("Invalid request body in handleFleetDone: %v", err)
			respondJSON(w, http.StatusBadRequest, FleetDoneResponse{
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
			log.Printf("Failed to get worker %s: %v", safeID, err)
			respondJSON(w, http.StatusInternalServerError, FleetDoneResponse{
				Success: false,
				Error:   "failed to look up worker",
			})
			return
		}
		if worker == nil {
			respondJSON(w, http.StatusNotFound, FleetDoneResponse{
				Success: false,
				Error:   fmt.Sprintf("worker not found: %s", workerID),
			})
			return
		}

		// Look up worker's current claim
		claim, err := store.GetWorkerClaim(ctx, workerID)
		if err != nil {
			safeID := strconv.Quote(workerID)
			log.Printf("Failed to get worker claim for %s: %v", safeID, err)
			respondJSON(w, http.StatusInternalServerError, FleetDoneResponse{
				Success: false,
				Error:   "failed to look up worker claim",
			})
			return
		}

		// Idempotent: if worker has no claim, task was already completed
		if claim == nil {
			respondJSON(w, http.StatusOK, FleetDoneResponse{
				Success:  true,
				WorkerID: workerID,
			})
			return
		}

		taskID := claim.TaskID

		// Record task result before releasing (ensures we capture outcome)
		result := &fleet.TaskResult{
			WorkerID:    workerID,
			TaskID:      taskID,
			Success:     req.Success,
			CommitSHA:   req.CommitSHA,
			Error:       req.Error,
			CompletedAt: time.Now(),
		}
		if err := store.RecordTaskResult(ctx, result); err != nil {
			safeWorker, safeTask := strconv.Quote(workerID), strconv.Quote(taskID)
			log.Printf("Failed to record task result for %s/%s: %v", safeWorker, safeTask, err)
			respondJSON(w, http.StatusInternalServerError, FleetDoneResponse{
				Success: false,
				Error:   "failed to record task result",
			})
			return
		}

		// Release the claim
		if err := store.ReleaseClaim(ctx, taskID); err != nil {
			log.Printf("Failed to release claim for task %s: %v", taskID, err)
			respondJSON(w, http.StatusInternalServerError, FleetDoneResponse{
				Success: false,
				Error:   "failed to release task claim",
			})
			return
		}

		// Clean up worker claim cache (best effort, use fresh context since ctx may be nearly expired)
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cleanupCancel()
		if err := store.ClearWorkerClaim(cleanupCtx, workerID); err != nil {
			log.Printf("Warning: failed to clear worker claim cache for %s: %v", workerID, err)
		}

		log.Printf("Task completed: worker=%s task=%s success=%v", workerID, taskID, req.Success)
		respondJSON(w, http.StatusOK, FleetDoneResponse{
			Success:  true,
			TaskID:   taskID,
			WorkerID: workerID,
		})
	}
}

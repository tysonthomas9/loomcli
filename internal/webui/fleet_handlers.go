package webui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/types"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
	"github.com/tysonthomas9/loomcli/internal/webui/fleet"
)

// FleetRegisterRequest represents the JSON body for POST /api/fleet/register.
type FleetRegisterRequest struct {
	WorkerID string   `json:"worker_id"`
	Repos    []string `json:"repos,omitempty"`
}

// FleetRegisterResponse wraps the registration result for JSON response.
type FleetRegisterResponse struct {
	Success bool   `json:"success"`
	Token   string `json:"token,omitempty"`
	Error   string `json:"error,omitempty"`
}

// maxWorkerIDLength is the maximum length for a worker_id to prevent abuse.
const maxWorkerIDLength = 256

// workerRegistrar is an internal interface for testing worker registration.
type workerRegistrar interface {
	RegisterWorker(ctx context.Context, worker *fleet.Worker) error
}

// handleFleetRegister returns a handler that registers a fleet worker and issues a JWT.
func handleFleetRegister(store *fleet.Store, tokenCfg *TokenConfig) http.HandlerFunc {
	return handleFleetRegisterWithStore(store, tokenCfg)
}

// handleFleetRegisterWithStore is the internal implementation that accepts an interface for testing.
func handleFleetRegisterWithStore(store workerRegistrar, tokenCfg *TokenConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		// Check store availability
		if store == nil || tokenCfg == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			if err := json.NewEncoder(w).Encode(FleetRegisterResponse{
				Success: false,
				Error:   "fleet API not available",
			}); err != nil {
				log.Printf("Failed to encode fleet register response: %v", err)
			}
			return
		}

		// Parse request body
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
		var req FleetRegisterRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				w.WriteHeader(http.StatusRequestEntityTooLarge)
				if err := json.NewEncoder(w).Encode(FleetRegisterResponse{
					Success: false,
					Error:   "request body too large (max 1MB)",
				}); err != nil {
					log.Printf("Failed to encode fleet register response: %v", err)
				}
				return
			}
			w.WriteHeader(http.StatusBadRequest)
			if err := json.NewEncoder(w).Encode(FleetRegisterResponse{
				Success: false,
				Error:   fmt.Sprintf("invalid request body: %v", err),
			}); err != nil {
				log.Printf("Failed to encode fleet register response: %v", err)
			}
			return
		}

		// Validate worker_id
		if req.WorkerID == "" {
			w.WriteHeader(http.StatusBadRequest)
			if err := json.NewEncoder(w).Encode(FleetRegisterResponse{
				Success: false,
				Error:   "worker_id is required",
			}); err != nil {
				log.Printf("Failed to encode fleet register response: %v", err)
			}
			return
		}

		if len(req.WorkerID) > maxWorkerIDLength {
			w.WriteHeader(http.StatusBadRequest)
			if err := json.NewEncoder(w).Encode(FleetRegisterResponse{
				Success: false,
				Error:   fmt.Sprintf("worker_id exceeds maximum length of %d characters", maxWorkerIDLength),
			}); err != nil {
				log.Printf("Failed to encode fleet register response: %v", err)
			}
			return
		}

		// Register the worker
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()

		worker := &fleet.Worker{
			WorkerID:     req.WorkerID,
			Repos:        req.Repos,
			RegisteredAt: time.Now().Unix(),
		}

		if err := store.RegisterWorker(ctx, worker); err != nil {
			log.Printf("Failed to register worker %s: %v", req.WorkerID, err)
			w.WriteHeader(http.StatusInternalServerError)
			if err := json.NewEncoder(w).Encode(FleetRegisterResponse{
				Success: false,
				Error:   "failed to register worker",
			}); err != nil {
				log.Printf("Failed to encode fleet register response: %v", err)
			}
			return
		}

		// Generate JWT token
		token, err := GenerateWorkerToken(req.WorkerID, req.Repos, tokenCfg.SigningKey, tokenCfg.Expiry)
		if err != nil {
			log.Printf("Failed to generate token for worker %s: %v", req.WorkerID, err)
			w.WriteHeader(http.StatusInternalServerError)
			if err := json.NewEncoder(w).Encode(FleetRegisterResponse{
				Success: false,
				Error:   "failed to generate token",
			}); err != nil {
				log.Printf("Failed to encode fleet register response: %v", err)
			}
			return
		}

		log.Printf("Worker registered: %s", req.WorkerID)
		w.WriteHeader(http.StatusCreated)
		if err := json.NewEncoder(w).Encode(FleetRegisterResponse{
			Success: true,
			Token:   token,
		}); err != nil {
			log.Printf("Failed to encode fleet register response: %v", err)
		}
	}
}

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
		w.Header().Set("Content-Type", "application/json")

		// Check store availability
		if store == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			if err := json.NewEncoder(w).Encode(FleetDoneResponse{
				Success: false,
				Error:   "fleet API not available",
			}); err != nil {
				log.Printf("Failed to encode fleet done response: %v", err)
			}
			return
		}

		// Extract worker ID from path
		workerID := r.PathValue("id")
		if workerID == "" {
			w.WriteHeader(http.StatusBadRequest)
			if err := json.NewEncoder(w).Encode(FleetDoneResponse{
				Success: false,
				Error:   "missing worker ID",
			}); err != nil {
				log.Printf("Failed to encode fleet done response: %v", err)
			}
			return
		}

		// Parse request body
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
		var req FleetDoneRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				w.WriteHeader(http.StatusRequestEntityTooLarge)
				if err := json.NewEncoder(w).Encode(FleetDoneResponse{
					Success: false,
					Error:   "request body too large (max 1MB)",
				}); err != nil {
					log.Printf("Failed to encode fleet done response: %v", err)
				}
				return
			}
			w.WriteHeader(http.StatusBadRequest)
			if err := json.NewEncoder(w).Encode(FleetDoneResponse{
				Success: false,
				Error:   fmt.Sprintf("invalid request body: %v", err),
			}); err != nil {
				log.Printf("Failed to encode fleet done response: %v", err)
			}
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		// Validate worker exists
		worker, err := store.GetWorker(ctx, workerID)
		if err != nil {
			log.Printf("Failed to get worker %s: %v", workerID, err)
			w.WriteHeader(http.StatusInternalServerError)
			if err := json.NewEncoder(w).Encode(FleetDoneResponse{
				Success: false,
				Error:   "failed to look up worker",
			}); err != nil {
				log.Printf("Failed to encode fleet done response: %v", err)
			}
			return
		}
		if worker == nil {
			w.WriteHeader(http.StatusNotFound)
			if err := json.NewEncoder(w).Encode(FleetDoneResponse{
				Success: false,
				Error:   fmt.Sprintf("worker not found: %s", workerID),
			}); err != nil {
				log.Printf("Failed to encode fleet done response: %v", err)
			}
			return
		}

		// Look up worker's current claim
		claim, err := store.GetWorkerClaim(ctx, workerID)
		if err != nil {
			log.Printf("Failed to get worker claim for %s: %v", workerID, err)
			w.WriteHeader(http.StatusInternalServerError)
			if err := json.NewEncoder(w).Encode(FleetDoneResponse{
				Success: false,
				Error:   "failed to look up worker claim",
			}); err != nil {
				log.Printf("Failed to encode fleet done response: %v", err)
			}
			return
		}

		// Idempotent: if worker has no claim, task was already completed
		if claim == nil {
			w.WriteHeader(http.StatusOK)
			if err := json.NewEncoder(w).Encode(FleetDoneResponse{
				Success:  true,
				WorkerID: workerID,
			}); err != nil {
				log.Printf("Failed to encode fleet done response: %v", err)
			}
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
			log.Printf("Failed to record task result for %s/%s: %v", workerID, taskID, err)
			w.WriteHeader(http.StatusInternalServerError)
			if err := json.NewEncoder(w).Encode(FleetDoneResponse{
				Success: false,
				Error:   "failed to record task result",
			}); err != nil {
				log.Printf("Failed to encode fleet done response: %v", err)
			}
			return
		}

		// Release the claim
		if err := store.ReleaseClaim(ctx, taskID); err != nil {
			log.Printf("Failed to release claim for task %s: %v", taskID, err)
			w.WriteHeader(http.StatusInternalServerError)
			if err := json.NewEncoder(w).Encode(FleetDoneResponse{
				Success: false,
				Error:   "failed to release task claim",
			}); err != nil {
				log.Printf("Failed to encode fleet done response: %v", err)
			}
			return
		}

		// Clean up worker claim cache (best effort, use fresh context since ctx may be nearly expired)
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cleanupCancel()
		if err := store.ClearWorkerClaim(cleanupCtx, workerID); err != nil {
			log.Printf("Warning: failed to clear worker claim cache for %s: %v", workerID, err)
		}

		log.Printf("Task completed: worker=%s task=%s success=%v", workerID, taskID, req.Success)
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(FleetDoneResponse{
			Success:  true,
			TaskID:   taskID,
			WorkerID: workerID,
		}); err != nil {
			log.Printf("Failed to encode fleet done response: %v", err)
		}
	}
}

// FleetClaimRequest represents the optional JSON body for POST /api/fleet/claim.
type FleetClaimRequest struct {
	// Optional: specific issue ID to claim
	IssueID string `json:"issue_id,omitempty"`
	// Optional: filter by status (default: "open")
	Status string `json:"status,omitempty"`
	// Optional: filter by issue type
	IssueType string `json:"issue_type,omitempty"`
	// Optional: filter by max priority (claim highest priority first)
	MaxPriority *int `json:"max_priority,omitempty"`
}

// FleetClaimResponse wraps the claim result for JSON response.
type FleetClaimResponse struct {
	Success bool                       `json:"success"`
	Payload *types.WorkHandoffPayload  `json:"payload,omitempty"`
	Error   string                     `json:"error,omitempty"`
}

// fleetClaimClient is an internal interface for testing fleet claim operations.
type fleetClaimClient interface {
	Update(args *rpc.UpdateArgs) (*rpc.Response, error)
	Ready(args *rpc.ReadyArgs) (*rpc.Response, error)
}

// fleetClaimPoolGetter is an internal interface for testing fleet claim pool operations.
type fleetClaimPoolGetter interface {
	Get(ctx context.Context) (fleetClaimClient, error)
	Put(client fleetClaimClient)
}

// fleetClaimPoolAdapter wraps daemon.Pool to implement fleetClaimPoolGetter.
type fleetClaimPoolAdapter struct {
	pool daemon.Pool
}

func (p *fleetClaimPoolAdapter) Get(ctx context.Context) (fleetClaimClient, error) {
	return p.pool.Get(ctx)
}

func (p *fleetClaimPoolAdapter) Put(client fleetClaimClient) {
	if c, ok := client.(*rpc.Client); ok {
		p.pool.Put(c)
	}
}

// handleFleetClaim returns a handler that atomically claims a task for a fleet worker.
func handleFleetClaim(pool daemon.Pool) http.HandlerFunc {
	if pool == nil {
		return handleFleetClaimWithPool(nil)
	}
	return handleFleetClaimWithPool(&fleetClaimPoolAdapter{pool: pool})
}

// handleFleetClaimWithPool is the internal implementation that accepts an interface for testing.
func handleFleetClaimWithPool(pool fleetClaimPoolGetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		// Check pool availability
		if pool == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			if err := json.NewEncoder(w).Encode(FleetClaimResponse{
				Success: false,
				Error:   "connection pool not initialized",
			}); err != nil {
				log.Printf("Failed to encode fleet claim response: %v", err)
			}
			return
		}

		// Parse optional request body
		var req FleetClaimRequest
		if r.Body != nil && r.ContentLength > 0 {
			r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				var maxBytesErr *http.MaxBytesError
				if errors.As(err, &maxBytesErr) {
					w.WriteHeader(http.StatusRequestEntityTooLarge)
					if err := json.NewEncoder(w).Encode(FleetClaimResponse{
						Success: false,
						Error:   "request body too large (max 1MB)",
					}); err != nil {
						log.Printf("Failed to encode fleet claim response: %v", err)
					}
					return
				}
				w.WriteHeader(http.StatusBadRequest)
				if err := json.NewEncoder(w).Encode(FleetClaimResponse{
					Success: false,
					Error:   fmt.Sprintf("invalid request body: %v", err),
				}); err != nil {
					log.Printf("Failed to encode fleet claim response: %v", err)
				}
				return
			}
		}

		// Acquire connection with 5-second timeout
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		client, err := pool.Get(ctx)
		if err != nil {
			status := http.StatusServiceUnavailable
			if errors.Is(err, context.DeadlineExceeded) {
				status = http.StatusGatewayTimeout
			}
			w.WriteHeader(status)
			if err := json.NewEncoder(w).Encode(FleetClaimResponse{
				Success: false,
				Error:   "daemon not available",
			}); err != nil {
				log.Printf("Failed to encode fleet claim response: %v", err)
			}
			return
		}
		defer pool.Put(client)

		// If a specific issue ID was requested, claim it directly
		if req.IssueID != "" {
			claimSpecificIssue(w, client, req.IssueID)
			return
		}

		// Otherwise, find a ready task to claim
		readyArgs := &rpc.ReadyArgs{
			Limit: 10, // Fetch a small batch to try claiming from
		}
		if req.IssueType != "" {
			readyArgs.Type = req.IssueType
		}
		if req.MaxPriority != nil {
			readyArgs.Priority = req.MaxPriority
		}

		resp, err := client.Ready(readyArgs)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			if err := json.NewEncoder(w).Encode(FleetClaimResponse{
				Success: false,
				Error:   fmt.Sprintf("rpc error: %v", err),
			}); err != nil {
				log.Printf("Failed to encode fleet claim response: %v", err)
			}
			return
		}

		if !resp.Success {
			w.WriteHeader(http.StatusInternalServerError)
			if err := json.NewEncoder(w).Encode(FleetClaimResponse{
				Success: false,
				Error:   resp.Error,
			}); err != nil {
				log.Printf("Failed to encode fleet claim response: %v", err)
			}
			return
		}

		// Parse ready issues
		var issues []*types.Issue
		if err := json.Unmarshal(resp.Data, &issues); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			if err := json.NewEncoder(w).Encode(FleetClaimResponse{
				Success: false,
				Error:   fmt.Sprintf("failed to parse ready issues: %v", err),
			}); err != nil {
				log.Printf("Failed to encode fleet claim response: %v", err)
			}
			return
		}

		// No tasks available
		if len(issues) == 0 {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		// Try to claim each issue in order (already sorted by priority from Ready)
		for _, issue := range issues {
			claimed := tryClaimIssue(w, client, issue.ID)
			if claimed {
				return
			}
		}

		// All tasks were already claimed by other workers
		w.WriteHeader(http.StatusNoContent)
	}
}

// claimSpecificIssue attempts to claim a specific issue by ID and writes the response.
func claimSpecificIssue(w http.ResponseWriter, client fleetClaimClient, issueID string) {
	inProgress := "in_progress"
	updateArgs := &rpc.UpdateArgs{
		ID:     issueID,
		Claim:  true,
		Status: &inProgress,
	}

	resp, err := client.Update(updateArgs)
	if err != nil {
		status := http.StatusInternalServerError
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
		}
		w.WriteHeader(status)
		if err := json.NewEncoder(w).Encode(FleetClaimResponse{
			Success: false,
			Error:   err.Error(),
		}); err != nil {
			log.Printf("Failed to encode fleet claim response: %v", err)
		}
		return
	}

	if !resp.Success {
		if strings.Contains(resp.Error, "already claimed") {
			w.WriteHeader(http.StatusConflict)
			if err := json.NewEncoder(w).Encode(FleetClaimResponse{
				Success: false,
				Error:   "task already claimed by another worker",
			}); err != nil {
				log.Printf("Failed to encode fleet claim response: %v", err)
			}
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		if err := json.NewEncoder(w).Encode(FleetClaimResponse{
			Success: false,
			Error:   resp.Error,
		}); err != nil {
			log.Printf("Failed to encode fleet claim response: %v", err)
		}
		return
	}

	// Parse the updated issue from response
	var issue types.Issue
	if err := json.Unmarshal(resp.Data, &issue); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		if err := json.NewEncoder(w).Encode(FleetClaimResponse{
			Success: false,
			Error:   fmt.Sprintf("failed to parse claimed issue: %v", err),
		}); err != nil {
			log.Printf("Failed to encode fleet claim response: %v", err)
		}
		return
	}

	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(FleetClaimResponse{
		Success: true,
		Payload: &types.WorkHandoffPayload{
			Issue:  &issue,
			Labels: issue.Labels,
		},
	}); err != nil {
		log.Printf("Failed to encode fleet claim response: %v", err)
	}
}

// tryClaimIssue attempts to claim an issue and writes the response if successful.
// Returns true if the claim succeeded and a response was written.
func tryClaimIssue(w http.ResponseWriter, client fleetClaimClient, issueID string) bool {
	inProgress := "in_progress"
	updateArgs := &rpc.UpdateArgs{
		ID:     issueID,
		Claim:  true,
		Status: &inProgress,
	}

	resp, err := client.Update(updateArgs)
	if err != nil {
		log.Printf("Fleet claim attempt failed for %s: rpc error: %v", issueID, err)
		return false
	}

	if !resp.Success {
		// "already claimed" is expected during contention - log at debug level
		log.Printf("Fleet claim attempt failed for %s: %s", issueID, resp.Error)
		return false
	}

	// Parse the updated issue from response
	var issue types.Issue
	if err := json.Unmarshal(resp.Data, &issue); err != nil {
		log.Printf("Fleet claim: failed to parse response for %s: %v", issueID, err)
		return false
	}

	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(FleetClaimResponse{
		Success: true,
		Payload: &types.WorkHandoffPayload{
			Issue:  &issue,
			Labels: issue.Labels,
		},
	}); err != nil {
		log.Printf("Failed to encode fleet claim response: %v", err)
	}
	return true
}

package webui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/types"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
	"github.com/tysonthomas9/loomcli/internal/webui/fleet"
)

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
	Success bool                      `json:"success"`
	Payload *types.WorkHandoffPayload `json:"payload,omitempty"`
	Error   string                    `json:"error,omitempty"`
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
func handleFleetClaim(pool daemon.Pool, claimMetrics *fleet.ClaimMetrics) http.HandlerFunc {
	if pool == nil {
		return handleFleetClaimWithPool(nil, claimMetrics)
	}
	return handleFleetClaimWithPool(&fleetClaimPoolAdapter{pool: pool}, claimMetrics)
}

// handleFleetClaimWithPool is the internal implementation that accepts an interface for testing.
func handleFleetClaimWithPool(pool fleetClaimPoolGetter, claimMetrics *fleet.ClaimMetrics) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Check pool availability
		if pool == nil {
			respondJSON(w, http.StatusServiceUnavailable, FleetClaimResponse{
				Success: false,
				Error:   "connection pool not initialized",
			})
			return
		}

		// Parse optional request body
		var req FleetClaimRequest
		if r.Body != nil && r.ContentLength > 0 {
			r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				var maxBytesErr *http.MaxBytesError
				if errors.As(err, &maxBytesErr) {
					respondJSON(w, http.StatusRequestEntityTooLarge, FleetClaimResponse{
						Success: false,
						Error:   "request body too large (max 1MB)",
					})
					return
				}
				logger.Warn("invalid request body", "handler", "handleFleetClaim", "err", err)
				respondJSON(w, http.StatusBadRequest, FleetClaimResponse{
					Success: false,
					Error:   "invalid request body",
				})
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
			recordClaim(claimMetrics, fleet.ClaimResultTimeout)
			respondJSON(w, status, FleetClaimResponse{
				Success: false,
				Error:   "daemon not available",
			})
			return
		}
		defer pool.Put(client)

		// If a specific issue ID was requested, claim it directly
		if req.IssueID != "" {
			claimSpecificIssue(w, client, req.IssueID, claimMetrics)
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
			logger.Error("fleet RPC error", "handler", "handleFleetClaim", "op", "ready", "err", err)
			respondJSON(w, http.StatusInternalServerError, FleetClaimResponse{
				Success: false,
				Error:   "internal server error",
			})
			return
		}

		if !resp.Success {
			respondJSON(w, http.StatusInternalServerError, FleetClaimResponse{
				Success: false,
				Error:   resp.Error,
			})
			return
		}

		// Parse ready issues
		var issues []*types.Issue
		if err := json.Unmarshal(resp.Data, &issues); err != nil {
			respondJSON(w, http.StatusInternalServerError, FleetClaimResponse{
				Success: false,
				Error:   fmt.Sprintf("failed to parse ready issues: %v", err),
			})
			return
		}

		// No tasks available
		if len(issues) == 0 {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		// Try to claim each issue in order (already sorted by priority from Ready)
		for _, issue := range issues {
			claimed := tryClaimIssue(w, client, issue.ID, claimMetrics)
			if claimed {
				return
			}
		}

		// All tasks were already claimed by other workers
		w.WriteHeader(http.StatusNoContent)
	}
}

// claimSpecificIssue attempts to claim a specific issue by ID and writes the response.
func claimSpecificIssue(w http.ResponseWriter, client fleetClaimClient, issueID string, claimMetrics *fleet.ClaimMetrics) {
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
		logger.Error("fleet RPC error", "handler", "claimSpecificIssue", "issue_id", issueID, "err", err)
		respondJSON(w, status, FleetClaimResponse{
			Success: false,
			Error:   "internal server error",
		})
		return
	}

	if !resp.Success {
		if strings.Contains(resp.Error, "already claimed") {
			recordClaim(claimMetrics, fleet.ClaimResultCollision)
			respondJSON(w, http.StatusConflict, FleetClaimResponse{
				Success: false,
				Error:   "task already claimed by another worker",
			})
			return
		}
		respondJSON(w, http.StatusInternalServerError, FleetClaimResponse{
			Success: false,
			Error:   resp.Error,
		})
		return
	}

	// Parse the updated issue from response
	var issue types.Issue
	if err := json.Unmarshal(resp.Data, &issue); err != nil {
		respondJSON(w, http.StatusInternalServerError, FleetClaimResponse{
			Success: false,
			Error:   fmt.Sprintf("failed to parse claimed issue: %v", err),
		})
		return
	}

	recordClaim(claimMetrics, fleet.ClaimResultSuccess)
	respondJSON(w, http.StatusOK, FleetClaimResponse{
		Success: true,
		Payload: &types.WorkHandoffPayload{
			Issue:  &issue,
			Labels: issue.Labels,
		},
	})
}

// tryClaimIssue attempts to claim an issue and writes the response if successful.
// Returns true if the claim succeeded and a response was written.
func tryClaimIssue(w http.ResponseWriter, client fleetClaimClient, issueID string, claimMetrics *fleet.ClaimMetrics) bool {
	inProgress := "in_progress"
	updateArgs := &rpc.UpdateArgs{
		ID:     issueID,
		Claim:  true,
		Status: &inProgress,
	}

	resp, err := client.Update(updateArgs)
	if err != nil {
		logger.Warn("fleet claim attempt failed", "issue_id", issueID, "err", err)
		return false
	}

	if !resp.Success {
		// "already claimed" is expected during contention - log at debug level
		logger.Warn("fleet claim attempt failed", "issue_id", issueID, "err", resp.Error)
		recordClaim(claimMetrics, fleet.ClaimResultCollision)
		return false
	}

	// Parse the updated issue from response
	var issue types.Issue
	if err := json.Unmarshal(resp.Data, &issue); err != nil {
		logger.Error("failed to parse claim response", "issue_id", issueID, "err", err)
		return false
	}

	recordClaim(claimMetrics, fleet.ClaimResultSuccess)
	respondJSON(w, http.StatusOK, FleetClaimResponse{
		Success: true,
		Payload: &types.WorkHandoffPayload{
			Issue:  &issue,
			Labels: issue.Labels,
		},
	})
	return true
}

// recordClaim safely records a claim result, handling nil metrics.
func recordClaim(m *fleet.ClaimMetrics, result string) {
	if m != nil {
		m.RecordClaim(result)
	}
}

package webui

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
)

// issueUpdater is an internal interface for testing issue updates.
// The production code uses *rpc.Client which implements this interface.
type issueUpdater interface {
	Update(args *rpc.UpdateArgs) (*rpc.Response, error)
}

// patchConnectionGetter is an internal interface for testing PATCH handler pool operations.
type patchConnectionGetter interface {
	Get(ctx context.Context) (issueUpdater, error)
	Put(client issueUpdater)
	Discard(client issueUpdater)
}

// patchPoolAdapter wraps daemon.Pool to implement patchConnectionGetter.
type patchPoolAdapter struct {
	pool daemon.Pool
}

func (p *patchPoolAdapter) Get(ctx context.Context) (issueUpdater, error) {
	return p.pool.Get(ctx)
}

func (p *patchPoolAdapter) Put(client issueUpdater) {
	if c, ok := client.(*rpc.Client); ok {
		p.pool.Put(c)
	}
}

func (p *patchPoolAdapter) Discard(client issueUpdater) {
	if c, ok := client.(*rpc.Client); ok {
		p.pool.Discard(c)
	}
}

// handlePatchIssue returns a handler that performs partial updates on an issue.
func handlePatchIssue(pool daemon.Pool) http.HandlerFunc {
	if pool == nil {
		return handlePatchIssueWithPool(nil)
	}
	return handlePatchIssueWithPool(&patchPoolAdapter{pool: pool})
}

// validatePatchRequest extracts the issue ID and parses the JSON body from an HTTP request.
func validatePatchRequest(w http.ResponseWriter, r *http.Request) (string, *PatchIssueRequest, bool) {
	issueID := r.PathValue("id")
	if issueID == "" {
		respondJSON(w, http.StatusBadRequest, PatchIssueResponse{
			Success: false,
			Error:   "missing issue ID in path",
		})
		return "", nil, false
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)

	var req PatchIssueRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			respondJSON(w, http.StatusRequestEntityTooLarge, PatchIssueResponse{
				Success: false,
				Error:   "request body too large (max 1MB)",
			})
			return "", nil, false
		}
		slog.Warn("invalid request body in handlePatchIssue", "err", err)
		respondJSON(w, http.StatusBadRequest, PatchIssueResponse{
			Success: false,
			Error:   "invalid request body",
		})
		return "", nil, false
	}

	return issueID, &req, true
}

// patchRequestToUpdateArgs converts a PatchIssueRequest into rpc.UpdateArgs.
func patchRequestToUpdateArgs(issueID string, req *PatchIssueRequest) *rpc.UpdateArgs {
	return &rpc.UpdateArgs{
		ID:                 issueID,
		Title:              req.Title,
		Description:        req.Description,
		Status:             req.Status,
		Priority:           req.Priority,
		Assignee:           req.Assignee,
		Owner:              req.Owner,
		Design:             req.Design,
		AcceptanceCriteria: req.AcceptanceCriteria,
		Notes:              req.Notes,
		ExternalRef:        req.ExternalRef,
		EstimatedMinutes:   req.EstimatedMinutes,
		IssueType:          req.IssueType,
		AddLabels:          req.AddLabels,
		RemoveLabels:       req.RemoveLabels,
		SetLabels:          req.SetLabels,
		Pinned:             req.Pinned,
		Parent:             req.Parent,
		DueAt:              req.DueAt,
		DeferUntil:         req.DeferUntil,
	}
}

// handlePatchIssueWithPool is the internal implementation that accepts an interface for testing.
func handlePatchIssueWithPool(pool patchConnectionGetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if pool == nil {
			respondJSON(w, http.StatusServiceUnavailable, PatchIssueResponse{
				Success: false,
				Error:   "connection pool not initialized",
			})
			return
		}

		issueID, req, ok := validatePatchRequest(w, r)
		if !ok {
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()

		client, err := pool.Get(ctx)
		if err != nil {
			status := http.StatusServiceUnavailable
			if errors.Is(err, context.DeadlineExceeded) {
				status = http.StatusGatewayTimeout
			}
			slog.Error("pool error in handlePatchIssue", "err", err)
			respondJSON(w, status, PatchIssueResponse{
				Success: false,
				Error:   "daemon not available",
			})
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

		resp, err := client.Update(patchRequestToUpdateArgs(issueID, req))
		if err != nil {
			status := http.StatusInternalServerError
			if strings.Contains(err.Error(), "not found") {
				status = http.StatusNotFound
			}
			slog.Error("RPC error in handlePatchIssue", "issue_id", issueID, "err", err)
			respondJSON(w, status, PatchIssueResponse{
				Success: false,
				Error:   "internal server error",
			})
			return
		}
		rpcOK = true

		if !resp.Success {
			status := http.StatusInternalServerError
			if strings.Contains(resp.Error, "not found") {
				status = http.StatusNotFound
			} else if strings.Contains(resp.Error, "cannot update template") {
				status = http.StatusConflict
			}
			respondJSON(w, status, PatchIssueResponse{
				Success: false,
				Error:   resp.Error,
			})
			return
		}

		respondJSON(w, http.StatusOK, PatchIssueResponse{
			Success: true,
			Data:    map[string]string{"id": issueID, "status": "updated"},
		})
	}
}

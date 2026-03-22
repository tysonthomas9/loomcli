package webui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
)

// issueCloser is an internal interface for testing issue close operations.
// The production code uses *rpc.Client which implements this interface.
type issueCloser interface {
	CloseIssue(args *rpc.CloseArgs) (*rpc.Response, error)
}

// closeConnectionGetter is an internal interface for testing close handler pool operations.
type closeConnectionGetter interface {
	Get(ctx context.Context) (issueCloser, error)
	Put(client issueCloser)
}

// closePoolAdapter wraps daemon.Pool to implement closeConnectionGetter.
type closePoolAdapter struct {
	pool daemon.Pool
}

func (p *closePoolAdapter) Get(ctx context.Context) (issueCloser, error) {
	return p.pool.Get(ctx)
}

func (p *closePoolAdapter) Put(client issueCloser) {
	if c, ok := client.(*rpc.Client); ok {
		p.pool.Put(c)
	}
}

// issueCreator is an internal interface for testing issue creation.
// The production code uses *rpc.Client which implements this interface.
type issueCreator interface {
	Create(args *rpc.CreateArgs) (*rpc.Response, error)
}

// createConnectionGetter is an internal interface for testing connection pool operations for create.
type createConnectionGetter interface {
	Get(ctx context.Context) (issueCreator, error)
	Put(client issueCreator)
}

// createPoolAdapter wraps daemon.Pool to implement createConnectionGetter.
type createPoolAdapter struct {
	pool daemon.Pool
}

func (p *createPoolAdapter) Get(ctx context.Context) (issueCreator, error) {
	return p.pool.Get(ctx)
}

func (p *createPoolAdapter) Put(client issueCreator) {
	if c, ok := client.(*rpc.Client); ok {
		p.pool.Put(c)
	}
}

// handleCreateIssue returns a handler that creates a new issue.
func handleCreateIssue(pool daemon.Pool) http.HandlerFunc {
	if pool == nil {
		return func(w http.ResponseWriter, r *http.Request) {
			writeIssuesError(w, http.StatusServiceUnavailable, "connection pool not initialized", "POOL_NOT_INITIALIZED")
		}
	}
	return handleCreateIssueWithPool(&createPoolAdapter{pool: pool})
}

// handleCreateIssueWithPool is the internal implementation that accepts an interface for testing.
func handleCreateIssueWithPool(pool createConnectionGetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Limit request body size to prevent DoS attacks
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)

		// Parse request body
		var req IssueCreateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			// Check if it's a request body too large error
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				writeIssuesError(w, http.StatusRequestEntityTooLarge, "request body too large (max 1MB)", "REQUEST_TOO_LARGE")
				return
			}
			slog.Warn("invalid JSON body in handleCreateIssue", "err", err)
			writeIssuesError(w, http.StatusBadRequest, "invalid request body", "INVALID_JSON")
			return
		}

		// Validate required fields
		if err := validateCreateRequest(&req); err != nil {
			writeIssuesError(w, http.StatusBadRequest, err.Error(), "VALIDATION_ERROR")
			return
		}

		// Acquire connection with 30-second timeout for create operations
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()

		client, err := pool.Get(ctx)
		if err != nil {
			status := http.StatusServiceUnavailable
			code := "DAEMON_UNAVAILABLE"
			message := "daemon unavailable"
			if errors.Is(err, context.DeadlineExceeded) {
				status = http.StatusGatewayTimeout
				code = "CONNECTION_TIMEOUT"
				message = "timeout connecting to daemon"
			}
			slog.Error("connection pool error", "err", err)
			writeIssuesError(w, status, message, code)
			return
		}
		defer pool.Put(client)

		// Convert request to RPC args and call daemon
		createArgs := toCreateArgs(&req)
		resp, err := client.Create(createArgs)
		if err != nil {
			slog.Error("RPC error in handleCreateIssue", "err", err)
			writeIssuesError(w, http.StatusInternalServerError, "failed to create issue", "RPC_ERROR")
			return
		}

		if !resp.Success {
			writeIssuesError(w, http.StatusInternalServerError, resp.Error, "DAEMON_ERROR")
			return
		}

		// Return success with created issue
		respondJSON(w, http.StatusCreated, IssuesResponse{
			Success: true,
			Data:    resp.Data,
		})
	}
}

// handleCloseIssue returns a handler that closes an issue by ID.
func handleCloseIssue(pool daemon.Pool) http.HandlerFunc {
	if pool == nil {
		return handleCloseIssueWithPool(nil)
	}
	return handleCloseIssueWithPool(&closePoolAdapter{pool: pool})
}

// handleCloseIssueWithPool is the internal implementation that accepts an interface for testing.
func handleCloseIssueWithPool(pool closeConnectionGetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Extract issue ID from path parameter
		issueID := r.PathValue("id")
		if issueID == "" {
			respondError(w, http.StatusBadRequest, "missing issue ID")
			return
		}

		// Limit request body size to prevent DoS attacks
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)

		// Parse optional JSON body
		var req CloseRequest
		if r.Body != nil && r.ContentLength > 0 {
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				var maxBytesErr *http.MaxBytesError
				if errors.As(err, &maxBytesErr) {
					respondError(w, http.StatusRequestEntityTooLarge, "request body too large (max 1MB)")
					return
				}
				slog.Warn("invalid request body in handleCloseIssue", "err", err)
				respondError(w, http.StatusBadRequest, "invalid request body")
				return
			}
		}

		// Check pool availability
		if pool == nil {
			respondError(w, http.StatusServiceUnavailable, "connection pool not initialized")
			return
		}

		// Get connection from pool
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		client, err := pool.Get(ctx)
		if err != nil {
			status := http.StatusServiceUnavailable
			if errors.Is(err, context.DeadlineExceeded) {
				status = http.StatusGatewayTimeout
			}
			respondError(w, status, "daemon not available")
			return
		}
		defer pool.Put(client)

		// Build CloseArgs from path and body
		args := &rpc.CloseArgs{
			ID:          issueID,
			Reason:      req.Reason,
			Session:     req.Session,
			SuggestNext: req.SuggestNext,
			Force:       req.Force,
		}

		// Call CloseIssue RPC
		resp, err := client.CloseIssue(args)
		if err != nil {
			// Check if it's a "not found" error
			if strings.Contains(err.Error(), "not found") {
				respondError(w, http.StatusNotFound, fmt.Sprintf("issue not found: %s", issueID))
				return
			}
			// Check for "has open blockers" error (when force=false)
			if strings.Contains(err.Error(), "blocker") {
				respondError(w, http.StatusConflict, err.Error())
				return
			}
			slog.Error("RPC error in handleCloseIssue", "issue_id", issueID, "err", err)
			respondError(w, http.StatusInternalServerError, "internal server error")
			return
		}

		if !resp.Success {
			respondError(w, http.StatusInternalServerError, resp.Error)
			return
		}

		// Return success response with closed issue data
		respondJSON(w, http.StatusOK, CloseResponse{
			Success: true,
			Data:    resp.Data,
		})
	}
}

// issueDeleter is an internal interface for testing issue delete operations.
type issueDeleter interface {
	Delete(args *rpc.DeleteArgs) (*rpc.Response, error)
}

// deleteConnectionGetter is an internal interface for testing delete handler pool operations.
type deleteConnectionGetter interface {
	Get(ctx context.Context) (issueDeleter, error)
	Put(client issueDeleter)
}

// deletePoolAdapter wraps daemon.Pool to implement deleteConnectionGetter.
type deletePoolAdapter struct {
	pool daemon.Pool
}

func (p *deletePoolAdapter) Get(ctx context.Context) (issueDeleter, error) {
	return p.pool.Get(ctx)
}

func (p *deletePoolAdapter) Put(client issueDeleter) {
	if c, ok := client.(*rpc.Client); ok {
		p.pool.Put(c)
	}
}

// handleDeleteIssue returns a handler that permanently deletes an issue by ID.
func handleDeleteIssue(pool daemon.Pool) http.HandlerFunc {
	if pool == nil {
		return handleDeleteIssueWithPool(nil)
	}
	return handleDeleteIssueWithPool(&deletePoolAdapter{pool: pool})
}

// handleDeleteIssueWithPool is the internal implementation that accepts an interface for testing.
func handleDeleteIssueWithPool(pool deleteConnectionGetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		issueID := r.PathValue("id")
		if issueID == "" {
			respondError(w, http.StatusBadRequest, "missing issue ID")
			return
		}

		if pool == nil {
			respondError(w, http.StatusServiceUnavailable, "connection pool not initialized")
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		client, err := pool.Get(ctx)
		if err != nil {
			status := http.StatusServiceUnavailable
			if errors.Is(err, context.DeadlineExceeded) {
				status = http.StatusGatewayTimeout
			}
			respondError(w, status, "daemon not available")
			return
		}
		defer pool.Put(client)

		resp, err := client.Delete(&rpc.DeleteArgs{
			IDs:   []string{issueID},
			Force: true,
		})
		if err != nil {
			if strings.Contains(err.Error(), "not found") {
				respondError(w, http.StatusNotFound, fmt.Sprintf("issue not found: %s", issueID))
				return
			}
			slog.Error("RPC error in handleDeleteIssue", "issue_id", issueID, "err", err)
			respondError(w, http.StatusInternalServerError, "internal server error")
			return
		}

		if !resp.Success {
			respondError(w, http.StatusInternalServerError, resp.Error)
			return
		}

		respondJSON(w, http.StatusOK, map[string]any{
			"success": true,
			"data":    resp.Data,
		})
	}
}

// validateCreateRequest validates the required fields in a create request.
func validateCreateRequest(req *IssueCreateRequest) error {
	// Validate title
	if strings.TrimSpace(req.Title) == "" {
		return fmt.Errorf("title is required")
	}

	// Validate issue_type
	if req.IssueType == "" {
		return fmt.Errorf("issue_type is required")
	}
	if !validIssueTypes[req.IssueType] {
		return fmt.Errorf("invalid issue_type: %s (must be bug, feature, task, epic, or chore)", req.IssueType)
	}

	// Validate priority (0-4 are valid, where 0 is P0/critical)
	if req.Priority < 0 || req.Priority > 4 {
		return fmt.Errorf("priority must be between 0 and 4 (got %d)", req.Priority)
	}

	// Validate array lengths to prevent resource exhaustion
	if len(req.Labels) > maxLabels {
		return fmt.Errorf("too many labels (max %d, got %d)", maxLabels, len(req.Labels))
	}
	if len(req.Dependencies) > maxDependencies {
		return fmt.Errorf("too many dependencies (max %d, got %d)", maxDependencies, len(req.Dependencies))
	}

	return nil
}

// toCreateArgs converts an IssueCreateRequest to rpc.CreateArgs.
func toCreateArgs(req *IssueCreateRequest) *rpc.CreateArgs {
	return &rpc.CreateArgs{
		ID:                 req.ID,
		Parent:             req.Parent,
		Title:              req.Title,
		Description:        req.Description,
		IssueType:          req.IssueType,
		Priority:           req.Priority,
		Design:             req.Design,
		AcceptanceCriteria: req.AcceptanceCriteria,
		Notes:              req.Notes,
		Assignee:           req.Assignee,
		ExternalRef:        req.ExternalRef,
		EstimatedMinutes:   req.EstimatedMinutes,
		Labels:             req.Labels,
		Dependencies:       req.Dependencies,
		CreatedBy:          req.CreatedBy,
		Owner:              req.Owner,
		DueAt:              req.DueAt,
		DeferUntil:         req.DeferUntil,
	}
}

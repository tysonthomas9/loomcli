package webui

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
)

// AddDependencyRequest represents the POST body for adding a dependency.
type AddDependencyRequest struct {
	DependsOnID string `json:"depends_on_id"`
	DepType     string `json:"dep_type,omitempty"` // defaults to "blocks"
}

// DependencyResponse wraps the dependency operation result for JSON response.
// Follows the same structure as other API responses for consistency.
type DependencyResponse struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}

// dependencyManager is an internal interface for testing dependency operations.
// The production code uses *rpc.Client which implements this interface.
type dependencyManager interface {
	AddDependency(args *rpc.DepAddArgs) (*rpc.Response, error)
	RemoveDependency(args *rpc.DepRemoveArgs) (*rpc.Response, error)
}

// dependencyConnectionGetter is an internal interface for testing dependency handler pool operations.
type dependencyConnectionGetter interface {
	Get(ctx context.Context) (dependencyManager, error)
	Put(client dependencyManager)
}

// dependencyPoolAdapter wraps daemon.Pool to implement dependencyConnectionGetter.
type dependencyPoolAdapter struct {
	pool daemon.Pool
}

func (p *dependencyPoolAdapter) Get(ctx context.Context) (dependencyManager, error) {
	return p.pool.Get(ctx)
}

func (p *dependencyPoolAdapter) Put(client dependencyManager) {
	if c, ok := client.(*rpc.Client); ok {
		p.pool.Put(c)
	}
}

// handleAddDependency creates a dependency from the issue to another issue.
func handleAddDependency(pool daemon.Pool) http.HandlerFunc {
	if pool == nil {
		return handleAddDependencyWithPool(nil)
	}
	return handleAddDependencyWithPool(&dependencyPoolAdapter{pool: pool})
}

// handleAddDependencyWithPool is the internal implementation that accepts an interface for testing.
func handleAddDependencyWithPool(pool dependencyConnectionGetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Extract issue ID from path parameter
		issueID := r.PathValue("id")
		if issueID == "" {
			respondJSON(w, http.StatusBadRequest, DependencyResponse{
				Success: false,
				Error:   "missing issue ID",
			})
			return
		}

		// Check pool availability
		if pool == nil {
			respondJSON(w, http.StatusServiceUnavailable, DependencyResponse{
				Success: false,
				Error:   "connection pool not initialized",
			})
			return
		}

		// Limit request body size to prevent DoS attacks
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)

		// Parse JSON body
		var req AddDependencyRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				respondJSON(w, http.StatusRequestEntityTooLarge, DependencyResponse{
					Success: false,
					Error:   "request body too large (max 1MB)",
				})
				return
			}
			log.Printf("Invalid request body in handleAddDependency: %v", err)
			respondJSON(w, http.StatusBadRequest, DependencyResponse{
				Success: false,
				Error:   "invalid request body",
			})
			return
		}

		// Validate depends_on_id
		if req.DependsOnID == "" {
			respondJSON(w, http.StatusBadRequest, DependencyResponse{
				Success: false,
				Error:   "depends_on_id is required",
			})
			return
		}

		// Prevent self-dependency
		if issueID == req.DependsOnID {
			respondJSON(w, http.StatusBadRequest, DependencyResponse{
				Success: false,
				Error:   "cannot add self-dependency",
			})
			return
		}

		// Default dep_type to "blocks" if not provided
		depType := req.DepType
		if depType == "" {
			depType = "blocks"
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
			respondJSON(w, status, DependencyResponse{
				Success: false,
				Error:   "daemon not available",
			})
			return
		}
		defer pool.Put(client)

		// Call AddDependency RPC
		// FromID is the issue that depends on ToID
		resp, err := client.AddDependency(&rpc.DepAddArgs{
			FromID:  issueID,
			ToID:    req.DependsOnID,
			DepType: depType,
		})
		if err != nil {
			status := http.StatusInternalServerError
			// Check for common error cases
			if strings.Contains(err.Error(), "not found") {
				status = http.StatusNotFound
			} else if strings.Contains(err.Error(), "cycle") {
				status = http.StatusConflict
			} else if strings.Contains(err.Error(), "already exists") {
				status = http.StatusConflict
			}
			log.Printf("RPC error in handleAddDependency: %v", err)
			respondJSON(w, status, DependencyResponse{
				Success: false,
				Error:   "internal server error",
			})
			return
		}

		if !resp.Success {
			status := http.StatusInternalServerError
			if strings.Contains(resp.Error, "not found") {
				status = http.StatusNotFound
			} else if strings.Contains(resp.Error, "cycle") {
				status = http.StatusConflict
			} else if strings.Contains(resp.Error, "already exists") {
				status = http.StatusConflict
			}
			respondJSON(w, status, DependencyResponse{
				Success: false,
				Error:   resp.Error,
			})
			return
		}

		respondJSON(w, http.StatusOK, DependencyResponse{
			Success: true,
			Data:    nil,
		})
	}
}

// handleRemoveDependency removes a dependency from the issue.
func handleRemoveDependency(pool daemon.Pool) http.HandlerFunc {
	if pool == nil {
		return handleRemoveDependencyWithPool(nil)
	}
	return handleRemoveDependencyWithPool(&dependencyPoolAdapter{pool: pool})
}

// handleRemoveDependencyWithPool is the internal implementation that accepts an interface for testing.
func handleRemoveDependencyWithPool(pool dependencyConnectionGetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Extract issue ID and depId from path parameters
		issueID := r.PathValue("id")
		depID := r.PathValue("depId")

		if issueID == "" {
			respondJSON(w, http.StatusBadRequest, DependencyResponse{
				Success: false,
				Error:   "missing issue ID",
			})
			return
		}

		if depID == "" {
			respondJSON(w, http.StatusBadRequest, DependencyResponse{
				Success: false,
				Error:   "missing dependency ID",
			})
			return
		}

		// Check pool availability
		if pool == nil {
			respondJSON(w, http.StatusServiceUnavailable, DependencyResponse{
				Success: false,
				Error:   "connection pool not initialized",
			})
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
			respondJSON(w, status, DependencyResponse{
				Success: false,
				Error:   "daemon not available",
			})
			return
		}
		defer pool.Put(client)

		// Call RemoveDependency RPC
		// FromID is the issue, ToID is the issue it depends on
		resp, err := client.RemoveDependency(&rpc.DepRemoveArgs{
			FromID: issueID,
			ToID:   depID,
		})
		if err != nil {
			status := http.StatusInternalServerError
			if strings.Contains(err.Error(), "not found") {
				status = http.StatusNotFound
			}
			log.Printf("RPC error in handleRemoveDependency: %v", err)
			respondJSON(w, status, DependencyResponse{
				Success: false,
				Error:   "internal server error",
			})
			return
		}

		if !resp.Success {
			status := http.StatusInternalServerError
			if strings.Contains(resp.Error, "not found") {
				status = http.StatusNotFound
			}
			respondJSON(w, status, DependencyResponse{
				Success: false,
				Error:   resp.Error,
			})
			return
		}

		respondJSON(w, http.StatusOK, DependencyResponse{
			Success: true,
			Data:    nil,
		})
	}
}

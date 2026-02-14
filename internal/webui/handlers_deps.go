package webui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/types"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
)

// BlockedResponse wraps the blocked issues data for JSON response.
type BlockedResponse struct {
	Success bool                  `json:"success"`
	Data    []*types.BlockedIssue `json:"data,omitempty"`
	Error   string                `json:"error,omitempty"`
}

// blockedClient is an internal interface for testing blocked operations.
// The production code uses *rpc.Client which implements this interface.
type blockedClient interface {
	Blocked(args *rpc.BlockedArgs) (*rpc.Response, error)
}

// blockedConnectionGetter is an internal interface for testing blocked handler pool operations.
type blockedConnectionGetter interface {
	Get(ctx context.Context) (blockedClient, error)
	Put(client blockedClient)
}

// blockedPoolAdapter wraps daemon.Pool to implement blockedConnectionGetter.
type blockedPoolAdapter struct {
	pool daemon.Pool
}

func (p *blockedPoolAdapter) Get(ctx context.Context) (blockedClient, error) {
	return p.pool.Get(ctx)
}

func (p *blockedPoolAdapter) Put(client blockedClient) {
	if c, ok := client.(*rpc.Client); ok {
		p.pool.Put(c)
	}
}

// GraphDependency represents a dependency relationship for graph visualization.
type GraphDependency struct {
	DependsOnID string `json:"depends_on_id"`
	Type        string `json:"type"`
}

// GraphIssue represents a slim issue with dependency data for graph visualization.
type GraphIssue struct {
	ID           string             `json:"id"`
	Title        string             `json:"title"`
	Status       string             `json:"status"`
	Priority     int                `json:"priority"`
	IssueType    string             `json:"issue_type"`
	Labels       []string           `json:"labels,omitempty"`
	Dependencies []*GraphDependency `json:"dependencies,omitempty"`
	DeferUntil   string             `json:"defer_until,omitempty"`
	DueAt        string             `json:"due_at,omitempty"`
}

// GraphResponse wraps the graph data for JSON response.
type GraphResponse struct {
	Success bool          `json:"success"`
	Issues  []*GraphIssue `json:"issues,omitempty"`
	Error   string        `json:"error,omitempty"`
}

// graphClient is an internal interface for testing graph operations.
// The production code uses *rpc.Client which implements this interface.
type graphClient interface {
	GetGraphData(args *rpc.GetGraphDataArgs) (*rpc.GetGraphDataResponse, error)
}

// graphConnectionGetter is an internal interface for testing graph handler pool operations.
type graphConnectionGetter interface {
	Get(ctx context.Context) (graphClient, error)
	Put(client graphClient)
}

// graphPoolAdapter wraps daemon.Pool to implement graphConnectionGetter.
type graphPoolAdapter struct {
	pool daemon.Pool
}

func (p *graphPoolAdapter) Get(ctx context.Context) (graphClient, error) {
	return p.pool.Get(ctx)
}

func (p *graphPoolAdapter) Put(client graphClient) {
	if c, ok := client.(*rpc.Client); ok {
		p.pool.Put(c)
	}
}

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

// handleBlocked returns issues that have blocking dependencies (waiting on other issues).
func handleBlocked(pool daemon.Pool) http.HandlerFunc {
	if pool == nil {
		return handleBlockedWithPool(nil)
	}
	return handleBlockedWithPool(&blockedPoolAdapter{pool: pool})
}

// handleBlockedWithPool is the internal implementation that accepts an interface for testing.
func handleBlockedWithPool(pool blockedConnectionGetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if pool == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			if err := json.NewEncoder(w).Encode(BlockedResponse{
				Success: false,
				Error:   "connection pool not initialized",
			}); err != nil {
				log.Printf("Failed to encode blocked response: %v", err)
			}
			return
		}

		// Parse query parameters into BlockedArgs
		args, err := parseBlockedParams(r)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			if err := json.NewEncoder(w).Encode(BlockedResponse{
				Success: false,
				Error:   err.Error(),
			}); err != nil {
				log.Printf("Failed to encode blocked response: %v", err)
			}
			return
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
			log.Printf("Pool error in handleBlocked: %v", err)
			w.WriteHeader(status)
			if err := json.NewEncoder(w).Encode(BlockedResponse{
				Success: false,
				Error:   "daemon not available",
			}); err != nil {
				log.Printf("Failed to encode blocked response: %v", err)
			}
			return
		}
		defer pool.Put(client)

		// Execute Blocked RPC call
		resp, err := client.Blocked(args)
		if err != nil {
			log.Printf("RPC error in handleBlocked: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			if err := json.NewEncoder(w).Encode(BlockedResponse{
				Success: false,
				Error:   "internal server error",
			}); err != nil {
				log.Printf("Failed to encode blocked response: %v", err)
			}
			return
		}

		if !resp.Success {
			w.WriteHeader(http.StatusInternalServerError)
			if err := json.NewEncoder(w).Encode(BlockedResponse{
				Success: false,
				Error:   resp.Error,
			}); err != nil {
				log.Printf("Failed to encode blocked response: %v", err)
			}
			return
		}

		// Parse the blocked issues from RPC response
		var issues []*types.BlockedIssue
		if err := json.Unmarshal(resp.Data, &issues); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			if err := json.NewEncoder(w).Encode(BlockedResponse{
				Success: false,
				Error:   fmt.Sprintf("failed to parse blocked issues: %v", err),
			}); err != nil {
				log.Printf("Failed to encode blocked response: %v", err)
			}
			return
		}

		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(BlockedResponse{
			Success: true,
			Data:    issues,
		}); err != nil {
			log.Printf("Failed to encode blocked response: %v", err)
		}
	}
}

// handleGraph returns issues with full dependency data for graph visualization.
func handleGraph(pool daemon.Pool) http.HandlerFunc {
	if pool == nil {
		return handleGraphWithPool(nil)
	}
	return handleGraphWithPool(&graphPoolAdapter{pool: pool})
}

// handleGraphWithPool is the internal implementation that accepts an interface for testing.
func handleGraphWithPool(pool graphConnectionGetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if pool == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			if err := json.NewEncoder(w).Encode(GraphResponse{
				Success: false,
				Error:   "connection pool not initialized",
			}); err != nil {
				log.Printf("Failed to encode graph response: %v", err)
			}
			return
		}

		// Parse query parameters
		status, includeClosed := parseGraphParams(r)

		// Validate status parameter
		validStatuses := map[string]bool{"all": true, "open": true, "closed": true}
		if !validStatuses[status] {
			w.WriteHeader(http.StatusBadRequest)
			if err := json.NewEncoder(w).Encode(GraphResponse{
				Success: false,
				Error:   fmt.Sprintf("invalid status: %s (must be all, open, or closed)", status),
			}); err != nil {
				log.Printf("Failed to encode graph response: %v", err)
			}
			return
		}

		// Acquire connection with timeout
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		client, err := pool.Get(ctx)
		if err != nil {
			httpStatus := http.StatusServiceUnavailable
			if errors.Is(err, context.DeadlineExceeded) {
				httpStatus = http.StatusGatewayTimeout
			}
			log.Printf("Pool error in handleGraph: %v", err)
			w.WriteHeader(httpStatus)
			if err := json.NewEncoder(w).Encode(GraphResponse{
				Success: false,
				Error:   "daemon not available",
			}); err != nil {
				log.Printf("Failed to encode graph response: %v", err)
			}
			return
		}
		defer pool.Put(client)

		// Build GetGraphData args based on status filter
		graphArgs := &rpc.GetGraphDataArgs{}
		if status == "open" {
			graphArgs.ExcludeStatus = []string{"closed", "tombstone"}
		} else if status == "closed" {
			graphArgs.Status = "closed"
		} else {
			// "all" - exclude only tombstones
			graphArgs.ExcludeStatus = []string{"tombstone"}
		}
		// Don't include closed if explicitly disabled
		if !includeClosed && status == "all" {
			graphArgs.ExcludeStatus = append(graphArgs.ExcludeStatus, "closed")
		}
		// Single RPC call replaces List + N×Show
		result, err := client.GetGraphData(graphArgs)
		if err != nil {
			log.Printf("RPC error in handleGraph: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			if err := json.NewEncoder(w).Encode(GraphResponse{
				Success: false,
				Error:   "internal server error",
			}); err != nil {
				log.Printf("Failed to encode graph response: %v", err)
			}
			return
		}

		// Convert RPC response to HTTP response format
		graphIssues := make([]*GraphIssue, 0, len(result.Issues))
		for _, summary := range result.Issues {
			var graphDeps []*GraphDependency
			for _, dep := range summary.Dependencies {
				graphDeps = append(graphDeps, &GraphDependency{
					DependsOnID: dep.DependsOnID,
					Type:        dep.Type,
				})
			}
			graphIssues = append(graphIssues, &GraphIssue{
				ID:           summary.ID,
				Title:        summary.Title,
				Status:       summary.Status,
				Priority:     summary.Priority,
				IssueType:    summary.IssueType,
				Labels:       summary.Labels,
				Dependencies: graphDeps,
				DeferUntil:   summary.DeferUntil,
				DueAt:        summary.DueAt,
			})
		}

		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(GraphResponse{
			Success: true,
			Issues:  graphIssues,
		}); err != nil {
			log.Printf("Failed to encode graph response: %v", err)
		}
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
		w.Header().Set("Content-Type", "application/json")

		// Extract issue ID from path parameter
		issueID := r.PathValue("id")
		if issueID == "" {
			w.WriteHeader(http.StatusBadRequest)
			if err := json.NewEncoder(w).Encode(DependencyResponse{
				Success: false,
				Error:   "missing issue ID",
			}); err != nil {
				log.Printf("Failed to encode add dependency response: %v", err)
			}
			return
		}

		// Check pool availability
		if pool == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			if err := json.NewEncoder(w).Encode(DependencyResponse{
				Success: false,
				Error:   "connection pool not initialized",
			}); err != nil {
				log.Printf("Failed to encode add dependency response: %v", err)
			}
			return
		}

		// Limit request body size to prevent DoS attacks
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)

		// Parse JSON body
		var req AddDependencyRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				w.WriteHeader(http.StatusRequestEntityTooLarge)
				if err := json.NewEncoder(w).Encode(DependencyResponse{
					Success: false,
					Error:   "request body too large (max 1MB)",
				}); err != nil {
					log.Printf("Failed to encode add dependency response: %v", err)
				}
				return
			}
			log.Printf("Invalid request body in handleAddDependency: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			if err := json.NewEncoder(w).Encode(DependencyResponse{
				Success: false,
				Error:   "invalid request body",
			}); err != nil {
				log.Printf("Failed to encode add dependency response: %v", err)
			}
			return
		}

		// Validate depends_on_id
		if req.DependsOnID == "" {
			w.WriteHeader(http.StatusBadRequest)
			if err := json.NewEncoder(w).Encode(DependencyResponse{
				Success: false,
				Error:   "depends_on_id is required",
			}); err != nil {
				log.Printf("Failed to encode add dependency response: %v", err)
			}
			return
		}

		// Prevent self-dependency
		if issueID == req.DependsOnID {
			w.WriteHeader(http.StatusBadRequest)
			if err := json.NewEncoder(w).Encode(DependencyResponse{
				Success: false,
				Error:   "cannot add self-dependency",
			}); err != nil {
				log.Printf("Failed to encode add dependency response: %v", err)
			}
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
			w.WriteHeader(status)
			if err := json.NewEncoder(w).Encode(DependencyResponse{
				Success: false,
				Error:   "daemon not available",
			}); err != nil {
				log.Printf("Failed to encode add dependency response: %v", err)
			}
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
			w.WriteHeader(status)
			if err := json.NewEncoder(w).Encode(DependencyResponse{
				Success: false,
				Error:   "internal server error",
			}); err != nil {
				log.Printf("Failed to encode add dependency response: %v", err)
			}
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
			w.WriteHeader(status)
			if err := json.NewEncoder(w).Encode(DependencyResponse{
				Success: false,
				Error:   resp.Error,
			}); err != nil {
				log.Printf("Failed to encode add dependency response: %v", err)
			}
			return
		}

		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(DependencyResponse{
			Success: true,
			Data:    nil,
		}); err != nil {
			log.Printf("Failed to encode add dependency response: %v", err)
		}
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
		w.Header().Set("Content-Type", "application/json")

		// Extract issue ID and depId from path parameters
		issueID := r.PathValue("id")
		depID := r.PathValue("depId")

		if issueID == "" {
			w.WriteHeader(http.StatusBadRequest)
			if err := json.NewEncoder(w).Encode(DependencyResponse{
				Success: false,
				Error:   "missing issue ID",
			}); err != nil {
				log.Printf("Failed to encode remove dependency response: %v", err)
			}
			return
		}

		if depID == "" {
			w.WriteHeader(http.StatusBadRequest)
			if err := json.NewEncoder(w).Encode(DependencyResponse{
				Success: false,
				Error:   "missing dependency ID",
			}); err != nil {
				log.Printf("Failed to encode remove dependency response: %v", err)
			}
			return
		}

		// Check pool availability
		if pool == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			if err := json.NewEncoder(w).Encode(DependencyResponse{
				Success: false,
				Error:   "connection pool not initialized",
			}); err != nil {
				log.Printf("Failed to encode remove dependency response: %v", err)
			}
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
			w.WriteHeader(status)
			if err := json.NewEncoder(w).Encode(DependencyResponse{
				Success: false,
				Error:   "daemon not available",
			}); err != nil {
				log.Printf("Failed to encode remove dependency response: %v", err)
			}
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
			w.WriteHeader(status)
			if err := json.NewEncoder(w).Encode(DependencyResponse{
				Success: false,
				Error:   "internal server error",
			}); err != nil {
				log.Printf("Failed to encode remove dependency response: %v", err)
			}
			return
		}

		if !resp.Success {
			status := http.StatusInternalServerError
			if strings.Contains(resp.Error, "not found") {
				status = http.StatusNotFound
			}
			w.WriteHeader(status)
			if err := json.NewEncoder(w).Encode(DependencyResponse{
				Success: false,
				Error:   resp.Error,
			}); err != nil {
				log.Printf("Failed to encode remove dependency response: %v", err)
			}
			return
		}

		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(DependencyResponse{
			Success: true,
			Data:    nil,
		}); err != nil {
			log.Printf("Failed to encode remove dependency response: %v", err)
		}
	}
}

// parseBlockedParams parses query parameters into rpc.BlockedArgs.
func parseBlockedParams(r *http.Request) (*rpc.BlockedArgs, error) {
	args := &rpc.BlockedArgs{}
	q := r.URL.Query()

	// String parameters
	if v := q.Get("parent_id"); v != "" {
		args.ParentID = v
	}
	if v := q.Get("assignee"); v != "" {
		args.Assignee = v
	}
	if v := q.Get("type"); v != "" {
		args.Type = v
	}

	// Integer parameters
	if v := q.Get("priority"); v != "" {
		p, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("invalid priority value: %s (must be an integer 0-4)", v)
		}
		if p < 0 || p > 4 {
			return nil, fmt.Errorf("priority must be between 0 and 4 (got %d)", p)
		}
		args.Priority = &p
	}
	if v := q.Get("limit"); v != "" {
		l, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("invalid limit value: %s (must be a non-negative integer)", v)
		}
		if l < 0 {
			return nil, fmt.Errorf("limit must be non-negative (got %d)", l)
		}
		if l > MaxListLimit {
			l = MaxListLimit
		}
		args.Limit = l
	}

	return args, nil
}

// parseGraphParams parses query parameters for the graph endpoint.
func parseGraphParams(r *http.Request) (status string, includeClosed bool) {
	q := r.URL.Query()
	status = q.Get("status")
	if status == "" {
		status = "all"
	}
	includeClosedStr := q.Get("include_closed")
	includeClosed = includeClosedStr != "false" // Default true
	return status, includeClosed
}

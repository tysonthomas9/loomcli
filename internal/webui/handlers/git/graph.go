package git

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/types"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
)

// BlockedResponse wraps the blocked issues data for JSON response.
type BlockedResponse struct {
	Success bool                  `json:"success"`
	Data    []*types.BlockedIssue `json:"data,omitempty"`
	Error   string                `json:"error,omitempty"`
}

// BlockedClient is an interface for testing blocked operations.
// The production code uses *rpc.Client which implements this interface.
type BlockedClient interface {
	Blocked(args *rpc.BlockedArgs) (*rpc.Response, error)
}

// BlockedConnectionGetter is an interface for testing blocked handler pool operations.
type BlockedConnectionGetter interface {
	Get(ctx context.Context) (BlockedClient, error)
	Put(client BlockedClient)
	Discard(client BlockedClient)
}

// blockedPoolAdapter wraps daemon.Pool to implement BlockedConnectionGetter.
type blockedPoolAdapter struct {
	pool daemon.Pool
}

func (p *blockedPoolAdapter) Get(ctx context.Context) (BlockedClient, error) {
	return p.pool.Get(ctx)
}

func (p *blockedPoolAdapter) Put(client BlockedClient) {
	if c, ok := client.(*rpc.Client); ok {
		p.pool.Put(c)
	}
}

func (p *blockedPoolAdapter) Discard(client BlockedClient) {
	if c, ok := client.(*rpc.Client); ok {
		p.pool.Discard(c)
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
	Data    []*GraphIssue `json:"data,omitempty"`
	Error   string        `json:"error,omitempty"`
}

// GraphClient is an interface for testing graph operations.
// The production code uses *rpc.Client which implements this interface.
type GraphClient interface {
	GetGraphData(args *rpc.GetGraphDataArgs) (*rpc.GetGraphDataResponse, error)
}

// GraphConnectionGetter is an interface for testing graph handler pool operations.
type GraphConnectionGetter interface {
	Get(ctx context.Context) (GraphClient, error)
	Put(client GraphClient)
	Discard(client GraphClient)
}

// graphPoolAdapter wraps daemon.Pool to implement GraphConnectionGetter.
type graphPoolAdapter struct {
	pool daemon.Pool
}

func (p *graphPoolAdapter) Get(ctx context.Context) (GraphClient, error) {
	return p.pool.Get(ctx)
}

func (p *graphPoolAdapter) Put(client GraphClient) {
	if c, ok := client.(*rpc.Client); ok {
		p.pool.Put(c)
	}
}

func (p *graphPoolAdapter) Discard(client GraphClient) {
	if c, ok := client.(*rpc.Client); ok {
		p.pool.Discard(c)
	}
}

// HandleBlocked returns issues that have blocking dependencies (waiting on other issues).
func HandleBlocked(pool daemon.Pool) http.HandlerFunc {
	if pool == nil {
		return HandleBlockedWithPool(nil)
	}
	return HandleBlockedWithPool(&blockedPoolAdapter{pool: pool})
}

// HandleBlockedWithPool is the internal implementation that accepts an interface for testing.
func HandleBlockedWithPool(pool BlockedConnectionGetter) http.HandlerFunc { //nolint:funlen
	return func(w http.ResponseWriter, r *http.Request) {
		if pool == nil {
			handler.WriteJSON(w, http.StatusServiceUnavailable, BlockedResponse{
				Success: false,
				Error:   "connection pool not initialized",
			})
			return
		}

		// Parse query parameters into BlockedArgs
		args, err := parseBlockedParams(r)
		if err != nil {
			handler.WriteJSON(w, http.StatusBadRequest, BlockedResponse{
				Success: false,
				Error:   err.Error(),
			})
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
			slog.Error("pool error in HandleBlocked", "err", err)
			handler.WriteJSON(w, status, BlockedResponse{
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

		// Execute Blocked RPC call
		resp, err := client.Blocked(args)
		if err != nil {
			slog.Error("RPC error in HandleBlocked", "err", err)
			handler.WriteJSON(w, http.StatusInternalServerError, BlockedResponse{
				Success: false,
				Error:   "internal server error",
			})
			return
		}
		rpcOK = true

		if !resp.Success {
			handler.WriteJSON(w, http.StatusInternalServerError, BlockedResponse{
				Success: false,
				Error:   resp.Error,
			})
			return
		}

		// Parse the blocked issues from RPC response
		var issues []*types.BlockedIssue
		if err := json.Unmarshal(resp.Data, &issues); err != nil {
			handler.WriteJSON(w, http.StatusInternalServerError, BlockedResponse{
				Success: false,
				Error:   fmt.Sprintf("failed to parse blocked issues: %v", err),
			})
			return
		}

		handler.WriteJSON(w, http.StatusOK, BlockedResponse{
			Success: true,
			Data:    issues,
		})
	}
}

// HandleGraph returns issues with full dependency data for graph visualization.
func HandleGraph(pool daemon.Pool) http.HandlerFunc {
	if pool == nil {
		return HandleGraphWithPool(nil)
	}
	return HandleGraphWithPool(&graphPoolAdapter{pool: pool})
}

// HandleGraphWithPool is the internal implementation that accepts an interface for testing.
func HandleGraphWithPool(pool GraphConnectionGetter) http.HandlerFunc { //nolint:funlen
	return func(w http.ResponseWriter, r *http.Request) {
		if pool == nil {
			handler.WriteJSON(w, http.StatusServiceUnavailable, GraphResponse{
				Success: false,
				Error:   "connection pool not initialized",
			})
			return
		}

		// Parse query parameters
		status, includeClosed, sourceRepos := parseGraphParams(r)

		// Validate status parameter
		validStatuses := map[string]bool{"all": true, "open": true, "closed": true}
		if !validStatuses[status] {
			handler.WriteJSON(w, http.StatusBadRequest, GraphResponse{
				Success: false,
				Error:   fmt.Sprintf("invalid status: %s (must be all, open, or closed)", status),
			})
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
			slog.Error("pool error in HandleGraph", "err", err)
			handler.WriteJSON(w, httpStatus, GraphResponse{
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

		// Build GetGraphData args based on status filter
		graphArgs := &rpc.GetGraphDataArgs{
			SourceRepos: sourceRepos,
		}
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
			slog.Error("RPC error in HandleGraph", "err", err)
			handler.WriteJSON(w, http.StatusInternalServerError, GraphResponse{
				Success: false,
				Error:   "internal server error",
			})
			return
		}
		rpcOK = true

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

		handler.WriteJSON(w, http.StatusOK, GraphResponse{
			Success: true,
			Data:    graphIssues,
		})
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
		if l > handler.MaxListLimit {
			l = handler.MaxListLimit
		}
		args.Limit = l
	}

	return args, nil
}

// parseGraphParams parses query parameters for the graph endpoint.
func parseGraphParams(r *http.Request) (status string, includeClosed bool, sourceRepos []string) {
	q := r.URL.Query()
	status = q.Get("status")
	if status == "" {
		status = "all"
	}
	includeClosedStr := q.Get("include_closed")
	includeClosed = includeClosedStr != "false" // Default true
	sourceRepos = parseArrayParam(q, "source_repos")
	return status, includeClosed, sourceRepos
}

// parseArrayParam splits a comma-separated query parameter into trimmed strings.
// Returns nil if the key is absent.
func parseArrayParam(q interface{ Get(string) string }, key string) []string {
	v := q.Get(key)
	if v == "" {
		return nil
	}
	return splitAndTrim(v)
}

// splitAndTrim splits a comma-separated string and trims whitespace from each element.
func splitAndTrim(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

// parseIntParam parses an optional integer query parameter.
// Returns (nil, nil) if the key is absent.
func parseIntParam(q interface{ Get(string) string }, key string) (*int, error) {
	v := q.Get(key)
	if v == "" {
		return nil, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return nil, fmt.Errorf("invalid %s value: %s (must be an integer)", key, v)
	}
	return &n, nil
}

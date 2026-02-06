package main

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

	"github.com/steveyegge/beads/examples/beads-web-ui/daemon"
	"github.com/steveyegge/beads/internal/rpc"
	"github.com/steveyegge/beads/internal/types"
)

const (
	// MaxListLimit is the maximum number of issues that can be requested in a single call.
	MaxListLimit = 1000
)

// IssueWithParent extends IssueWithCounts with parent info for the /api/issues response.
// This enables the frontend to display parent-child relationships (swim lanes in Kanban)
// without requiring additional API calls for each issue.
type IssueWithParent struct {
	*types.IssueWithCounts
	Parent      *string `json:"parent,omitempty"`       // Parent issue ID (null for root-level issues)
	ParentTitle *string `json:"parent_title,omitempty"` // Parent issue title for display
}

// IssuesResponse represents the response structure for the issues endpoint.
type IssuesResponse struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data,omitempty"`
	Error   string          `json:"error,omitempty"`
	Code    string          `json:"code,omitempty"`
}

// ReadyIssueWithParent extends Issue with parent info for /api/ready.
// This enables epic swimlane grouping in the Kanban view.
type ReadyIssueWithParent struct {
	*types.Issue
	Parent      *string `json:"parent,omitempty"`       // Parent issue ID (null for root-level issues)
	ParentTitle *string `json:"parent_title,omitempty"` // Parent issue title for display
}

// ReadyResponse wraps the ready issues data for JSON response.
type ReadyResponse struct {
	Success bool                    `json:"success"`
	Data    []*ReadyIssueWithParent `json:"data,omitempty"`
	Error   string                  `json:"error,omitempty"`
}

// readyClient is an internal interface for testing ready issue operations.
// The production code uses *rpc.Client which implements this interface.
type readyClient interface {
	Ready(args *rpc.ReadyArgs) (*rpc.Response, error)
	GetParentIDs(args *rpc.GetParentIDsArgs) (*rpc.GetParentIDsResponse, error)
}

// readyConnectionGetter is an internal interface for testing ready handler pool operations.
type readyConnectionGetter interface {
	Get(ctx context.Context) (readyClient, error)
	Put(client readyClient)
}

// readyPoolAdapter wraps *daemon.ConnectionPool to implement readyConnectionGetter.
type readyPoolAdapter struct {
	pool *daemon.ConnectionPool
}

func (p *readyPoolAdapter) Get(ctx context.Context) (readyClient, error) {
	return p.pool.Get(ctx)
}

func (p *readyPoolAdapter) Put(client readyClient) {
	if c, ok := client.(*rpc.Client); ok {
		p.pool.Put(c)
	}
}

// CloseRequest represents the JSON body for the close endpoint.
type CloseRequest struct {
	Reason      string `json:"reason,omitempty"`
	Session     string `json:"session,omitempty"`
	SuggestNext bool   `json:"suggest_next,omitempty"`
	Force       bool   `json:"force,omitempty"`
}

// CloseResponse wraps the close result for JSON response.
type CloseResponse struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data,omitempty"`
	Error   string          `json:"error,omitempty"`
}

// issueGetter is an internal interface for testing issue retrieval.
// The production code uses *rpc.Client which implements this interface.
type issueGetter interface {
	Show(args *rpc.ShowArgs) (*rpc.Response, error)
}

// connectionGetter is an internal interface for testing connection pool operations.
type connectionGetter interface {
	Get(ctx context.Context) (issueGetter, error)
	Put(client issueGetter)
}

// poolAdapter wraps *daemon.ConnectionPool to implement connectionGetter.
type poolAdapter struct {
	pool *daemon.ConnectionPool
}

func (p *poolAdapter) Get(ctx context.Context) (issueGetter, error) {
	return p.pool.Get(ctx)
}

func (p *poolAdapter) Put(client issueGetter) {
	if c, ok := client.(*rpc.Client); ok {
		p.pool.Put(c)
	}
}

// issueUpdater is an internal interface for testing issue updates.
// The production code uses *rpc.Client which implements this interface.
type issueUpdater interface {
	Update(args *rpc.UpdateArgs) (*rpc.Response, error)
}

// patchConnectionGetter is an internal interface for testing PATCH handler pool operations.
type patchConnectionGetter interface {
	Get(ctx context.Context) (issueUpdater, error)
	Put(client issueUpdater)
}

// patchPoolAdapter wraps *daemon.ConnectionPool to implement patchConnectionGetter.
type patchPoolAdapter struct {
	pool *daemon.ConnectionPool
}

func (p *patchPoolAdapter) Get(ctx context.Context) (issueUpdater, error) {
	return p.pool.Get(ctx)
}

func (p *patchPoolAdapter) Put(client issueUpdater) {
	if c, ok := client.(*rpc.Client); ok {
		p.pool.Put(c)
	}
}

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

// closePoolAdapter wraps *daemon.ConnectionPool to implement closeConnectionGetter.
type closePoolAdapter struct {
	pool *daemon.ConnectionPool
}

func (p *closePoolAdapter) Get(ctx context.Context) (issueCloser, error) {
	return p.pool.Get(ctx)
}

func (p *closePoolAdapter) Put(client issueCloser) {
	if c, ok := client.(*rpc.Client); ok {
		p.pool.Put(c)
	}
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

// graphPoolAdapter wraps *daemon.ConnectionPool to implement graphConnectionGetter.
type graphPoolAdapter struct {
	pool *daemon.ConnectionPool
}

func (p *graphPoolAdapter) Get(ctx context.Context) (graphClient, error) {
	return p.pool.Get(ctx)
}

func (p *graphPoolAdapter) Put(client graphClient) {
	if c, ok := client.(*rpc.Client); ok {
		p.pool.Put(c)
	}
}

// writeErrorResponse writes a JSON error response with the given status code and message.
func writeErrorResponse(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(map[string]string{"error": message}); err != nil {
		log.Printf("Failed to encode error response: %v", err)
	}
}

// handleGetIssue returns a handler that retrieves a single issue by ID.
func handleGetIssue(pool *daemon.ConnectionPool) http.HandlerFunc {
	return handleGetIssueWithPool(&poolAdapter{pool: pool})
}

// handleGetIssueWithPool is the internal implementation that accepts an interface for testing.
func handleGetIssueWithPool(pool connectionGetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Extract issue ID from path parameter
		issueID := r.PathValue("id")
		if issueID == "" {
			writeErrorResponse(w, http.StatusBadRequest, "missing issue ID")
			return
		}

		// Get connection from pool
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		client, err := pool.Get(ctx)
		if err != nil {
			writeErrorResponse(w, http.StatusServiceUnavailable, "daemon not available")
			return
		}
		defer pool.Put(client)

		// Call Show RPC
		resp, err := client.Show(&rpc.ShowArgs{ID: issueID})
		if err != nil {
			// Check if it's a "not found" error
			if strings.Contains(err.Error(), "not found") {
				writeErrorResponse(w, http.StatusNotFound, fmt.Sprintf("issue not found: %s", issueID))
				return
			}
			writeErrorResponse(w, http.StatusInternalServerError, err.Error())
			return
		}

		// Return the issue details wrapped in standard {success, data} envelope
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(IssuesResponse{
			Success: true,
			Data:    resp.Data,
		}); err != nil {
			log.Printf("Failed to encode issue response: %v", err)
		}
	}
}

// handleListIssues returns a handler that lists issues from the daemon.
func handleListIssues(pool *daemon.ConnectionPool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if pool == nil {
			writeIssuesError(w, http.StatusServiceUnavailable, "connection pool not initialized", "POOL_NOT_INITIALIZED")
			return
		}

		// Parse query parameters into ListArgs
		args := parseListParams(r)

		// Acquire connection with timeout
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
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
			log.Printf("Connection pool error: %v", err)
			writeIssuesError(w, status, message, code)
			return
		}
		defer pool.Put(client)

		// Execute List RPC call
		resp, err := client.List(args)
		if err != nil {
			log.Printf("RPC error: %v", err)
			writeIssuesError(w, http.StatusInternalServerError, "failed to list issues", "RPC_ERROR")
			return
		}

		if !resp.Success {
			writeIssuesError(w, http.StatusInternalServerError, resp.Error, "DAEMON_ERROR")
			return
		}

		// Parse IssueWithCounts from response to extract issue IDs
		var issuesWithCounts []*types.IssueWithCounts
		if err := json.Unmarshal(resp.Data, &issuesWithCounts); err != nil {
			log.Printf("Failed to parse issues: %v", err)
			writeIssuesError(w, http.StatusInternalServerError, "failed to parse issues", "PARSE_ERROR")
			return
		}

		// If no issues, return empty response
		if len(issuesWithCounts) == 0 {
			data, _ := json.Marshal([]*IssueWithParent{})
			w.WriteHeader(http.StatusOK)
			if err := json.NewEncoder(w).Encode(IssuesResponse{
				Success: true,
				Data:    data,
			}); err != nil {
				log.Printf("Failed to encode issues response: %v", err)
			}
			return
		}

		// Extract issue IDs for parent lookup
		issueIDs := make([]string, len(issuesWithCounts))
		for i, iwc := range issuesWithCounts {
			issueIDs[i] = iwc.Issue.ID
		}

		// Get parent info for all issues
		parentResp, err := client.GetParentIDs(&rpc.GetParentIDsArgs{IssueIDs: issueIDs})
		if err != nil {
			// Non-fatal: log and continue without parent info
			log.Printf("Failed to get parent IDs: %v", err)
			parentResp = &rpc.GetParentIDsResponse{Parents: make(map[string]*rpc.ParentInfo)}
		}

		// Build response with parent info
		issuesWithParent := make([]*IssueWithParent, len(issuesWithCounts))
		for i, iwc := range issuesWithCounts {
			iwp := &IssueWithParent{
				IssueWithCounts: iwc,
			}
			if parentInfo, ok := parentResp.Parents[iwc.Issue.ID]; ok {
				iwp.Parent = &parentInfo.ParentID
				iwp.ParentTitle = &parentInfo.ParentTitle
			}
			issuesWithParent[i] = iwp
		}

		data, _ := json.Marshal(issuesWithParent)
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(IssuesResponse{
			Success: true,
			Data:    data,
		}); err != nil {
			log.Printf("Failed to encode issues response: %v", err)
		}
	}
}

// handleReady returns issues ready to work on (open/in_progress with no blockers).
func handleReady(pool *daemon.ConnectionPool) http.HandlerFunc {
	if pool == nil {
		return handleReadyWithPool(nil)
	}
	return handleReadyWithPool(&readyPoolAdapter{pool: pool})
}

// handleReadyWithPool is the internal implementation that accepts an interface for testing.
func handleReadyWithPool(pool readyConnectionGetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if pool == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			if err := json.NewEncoder(w).Encode(ReadyResponse{
				Success: false,
				Error:   "connection pool not initialized",
			}); err != nil {
				log.Printf("Failed to encode ready response: %v", err)
			}
			return
		}

		// Parse query parameters into ReadyArgs
		args, err := parseReadyParams(r)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			if err := json.NewEncoder(w).Encode(ReadyResponse{
				Success: false,
				Error:   err.Error(),
			}); err != nil {
				log.Printf("Failed to encode ready response: %v", err)
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
			w.WriteHeader(status)
			if err := json.NewEncoder(w).Encode(ReadyResponse{
				Success: false,
				Error:   err.Error(),
			}); err != nil {
				log.Printf("Failed to encode ready response: %v", err)
			}
			return
		}
		defer pool.Put(client)

		// Execute Ready RPC call
		resp, err := client.Ready(args)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			if err := json.NewEncoder(w).Encode(ReadyResponse{
				Success: false,
				Error:   fmt.Sprintf("rpc error: %v", err),
			}); err != nil {
				log.Printf("Failed to encode ready response: %v", err)
			}
			return
		}

		if !resp.Success {
			w.WriteHeader(http.StatusInternalServerError)
			if err := json.NewEncoder(w).Encode(ReadyResponse{
				Success: false,
				Error:   resp.Error,
			}); err != nil {
				log.Printf("Failed to encode ready response: %v", err)
			}
			return
		}

		// Parse the issues from RPC response
		var issues []*types.Issue
		if err := json.Unmarshal(resp.Data, &issues); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			if err := json.NewEncoder(w).Encode(ReadyResponse{
				Success: false,
				Error:   fmt.Sprintf("failed to parse ready issues: %v", err),
			}); err != nil {
				log.Printf("Failed to encode ready response: %v", err)
			}
			return
		}

		// If no issues, return empty response
		if len(issues) == 0 {
			w.WriteHeader(http.StatusOK)
			if err := json.NewEncoder(w).Encode(ReadyResponse{
				Success: true,
				Data:    []*ReadyIssueWithParent{},
			}); err != nil {
				log.Printf("Failed to encode ready response: %v", err)
			}
			return
		}

		// Extract issue IDs for parent lookup
		issueIDs := make([]string, len(issues))
		for i, issue := range issues {
			issueIDs[i] = issue.ID
		}

		// Get parent info for all issues
		parentResp, err := client.GetParentIDs(&rpc.GetParentIDsArgs{IssueIDs: issueIDs})
		if err != nil {
			// Non-fatal: log and continue without parent info
			log.Printf("Failed to get parent IDs for ready issues: %v", err)
			parentResp = &rpc.GetParentIDsResponse{Parents: make(map[string]*rpc.ParentInfo)}
		}

		// Build response with parent info
		issuesWithParent := make([]*ReadyIssueWithParent, len(issues))
		for i, issue := range issues {
			iwp := &ReadyIssueWithParent{
				Issue: issue,
			}
			if parentInfo, ok := parentResp.Parents[issue.ID]; ok {
				iwp.Parent = &parentInfo.ParentID
				iwp.ParentTitle = &parentInfo.ParentTitle
			}
			issuesWithParent[i] = iwp
		}

		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(ReadyResponse{
			Success: true,
			Data:    issuesWithParent,
		}); err != nil {
			log.Printf("Failed to encode ready response: %v", err)
		}
	}
}

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

// blockedPoolAdapter wraps *daemon.ConnectionPool to implement blockedConnectionGetter.
type blockedPoolAdapter struct {
	pool *daemon.ConnectionPool
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

// handleBlocked returns issues that have blocking dependencies (waiting on other issues).
func handleBlocked(pool *daemon.ConnectionPool) http.HandlerFunc {
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
			w.WriteHeader(status)
			if err := json.NewEncoder(w).Encode(BlockedResponse{
				Success: false,
				Error:   err.Error(),
			}); err != nil {
				log.Printf("Failed to encode blocked response: %v", err)
			}
			return
		}
		defer pool.Put(client)

		// Execute Blocked RPC call
		resp, err := client.Blocked(args)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			if err := json.NewEncoder(w).Encode(BlockedResponse{
				Success: false,
				Error:   fmt.Sprintf("rpc error: %v", err),
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
func handleGraph(pool *daemon.ConnectionPool) http.HandlerFunc {
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
			w.WriteHeader(httpStatus)
			if err := json.NewEncoder(w).Encode(GraphResponse{
				Success: false,
				Error:   err.Error(),
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
			w.WriteHeader(http.StatusInternalServerError)
			if err := json.NewEncoder(w).Encode(GraphResponse{
				Success: false,
				Error:   fmt.Sprintf("rpc error: %v", err),
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

// parseListParams extracts ListArgs from HTTP query parameters.
func parseListParams(r *http.Request) *rpc.ListArgs {
	query := r.URL.Query()
	args := &rpc.ListArgs{}

	// Basic filters
	if v := query.Get("status"); v != "" {
		args.Status = v
	}
	if v := query.Get("type"); v != "" {
		args.IssueType = v
	}
	if v := query.Get("assignee"); v != "" {
		args.Assignee = v
	}
	if v := query.Get("q"); v != "" {
		args.Query = v
	}

	// Priority (integer)
	if v := query.Get("priority"); v != "" {
		if priority, err := strconv.Atoi(v); err == nil {
			args.Priority = &priority
		}
	}

	// Labels (comma-separated)
	if v := query.Get("labels"); v != "" {
		args.Labels = splitAndTrim(v)
	}

	// Limit (capped at MaxListLimit to prevent DoS)
	if v := query.Get("limit"); v != "" {
		if limit, err := strconv.Atoi(v); err == nil && limit > 0 {
			if limit > MaxListLimit {
				limit = MaxListLimit
			}
			args.Limit = limit
		}
	}

	// Pattern matching
	if v := query.Get("title_contains"); v != "" {
		args.TitleContains = v
	}
	if v := query.Get("description_contains"); v != "" {
		args.DescriptionContains = v
	}
	if v := query.Get("notes_contains"); v != "" {
		args.NotesContains = v
	}

	// Date ranges
	if v := query.Get("created_after"); v != "" {
		args.CreatedAfter = v
	}
	if v := query.Get("created_before"); v != "" {
		args.CreatedBefore = v
	}
	if v := query.Get("updated_after"); v != "" {
		args.UpdatedAfter = v
	}
	if v := query.Get("updated_before"); v != "" {
		args.UpdatedBefore = v
	}

	// Empty/null checks
	if v := query.Get("empty_description"); v == "true" {
		args.EmptyDescription = true
	}
	if v := query.Get("no_assignee"); v == "true" {
		args.NoAssignee = true
	}
	if v := query.Get("no_labels"); v == "true" {
		args.NoLabels = true
	}

	// Pinned filtering
	if v := query.Get("pinned"); v != "" {
		pinned := v == "true"
		args.Pinned = &pinned
	}

	return args
}

// parseReadyParams parses query parameters into rpc.ReadyArgs.
func parseReadyParams(r *http.Request) (*rpc.ReadyArgs, error) {
	args := &rpc.ReadyArgs{}
	q := r.URL.Query()

	// String parameters
	if v := q.Get("assignee"); v != "" {
		args.Assignee = v
	}
	if v := q.Get("type"); v != "" {
		args.Type = v
	}
	if v := q.Get("parent_id"); v != "" {
		args.ParentID = v
	}
	if v := q.Get("mol_type"); v != "" {
		// Validate mol_type
		molType := types.MolType(v)
		if !molType.IsValid() {
			return nil, fmt.Errorf("invalid mol_type: %s (must be swarm, patrol, or work)", v)
		}
		args.MolType = v
	}

	// Sort policy
	if v := q.Get("sort"); v != "" {
		sortPolicy := types.SortPolicy(v)
		if !sortPolicy.IsValid() {
			return nil, fmt.Errorf("invalid sort policy: %s (must be hybrid, priority, or oldest)", v)
		}
		args.SortPolicy = v
	}

	// Boolean parameters
	if v := q.Get("unassigned"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return nil, fmt.Errorf("invalid unassigned value: %s (must be true or false)", v)
		}
		args.Unassigned = b
	}
	if v := q.Get("include_deferred"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return nil, fmt.Errorf("invalid include_deferred value: %s (must be true or false)", v)
		}
		args.IncludeDeferred = b
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
		args.Limit = l
	}

	// Array parameters (comma-separated)
	if v := q.Get("labels"); v != "" {
		args.Labels = splitAndTrim(v)
	}
	if v := q.Get("labels_any"); v != "" {
		args.LabelsAny = splitAndTrim(v)
	}

	return args, nil
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

// writeIssuesError writes a JSON error response for the issues endpoint.
func writeIssuesError(w http.ResponseWriter, status int, message, code string) {
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(IssuesResponse{
		Success: false,
		Error:   message,
		Code:    code,
	}); err != nil {
		log.Printf("Failed to encode error response: %v", err)
	}
}

// PatchIssueRequest represents the PATCH /api/issues/:id request body.
// All fields are optional pointers to support partial updates.
type PatchIssueRequest struct {
	Title              *string  `json:"title,omitempty"`
	Description        *string  `json:"description,omitempty"`
	Status             *string  `json:"status,omitempty"`
	Priority           *int     `json:"priority,omitempty"`
	Assignee           *string  `json:"assignee,omitempty"`
	Design             *string  `json:"design,omitempty"`
	AcceptanceCriteria *string  `json:"acceptance_criteria,omitempty"`
	Notes              *string  `json:"notes,omitempty"`
	ExternalRef        *string  `json:"external_ref,omitempty"`
	EstimatedMinutes   *int     `json:"estimated_minutes,omitempty"`
	IssueType          *string  `json:"issue_type,omitempty"`
	AddLabels          []string `json:"add_labels,omitempty"`
	RemoveLabels       []string `json:"remove_labels,omitempty"`
	SetLabels          []string `json:"set_labels,omitempty"`
	Pinned             *bool    `json:"pinned,omitempty"`
	Parent             *string  `json:"parent,omitempty"`
	DueAt              *string  `json:"due_at,omitempty"`
	DeferUntil         *string  `json:"defer_until,omitempty"`
}

// PatchIssueResponse wraps the patch response for JSON output.
type PatchIssueResponse struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}

// handlePatchIssue returns a handler that performs partial updates on an issue.
func handlePatchIssue(pool *daemon.ConnectionPool) http.HandlerFunc {
	if pool == nil {
		return handlePatchIssueWithPool(nil)
	}
	return handlePatchIssueWithPool(&patchPoolAdapter{pool: pool})
}

// handlePatchIssueWithPool is the internal implementation that accepts an interface for testing.
func handlePatchIssueWithPool(pool patchConnectionGetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		// Extract and validate issue ID from path
		issueID := r.PathValue("id")
		if issueID == "" {
			w.WriteHeader(http.StatusBadRequest)
			if err := json.NewEncoder(w).Encode(PatchIssueResponse{
				Success: false,
				Error:   "missing issue ID in path",
			}); err != nil {
				log.Printf("Failed to encode patch response: %v", err)
			}
			return
		}

		// Check pool availability
		if pool == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			if err := json.NewEncoder(w).Encode(PatchIssueResponse{
				Success: false,
				Error:   "connection pool not initialized",
			}); err != nil {
				log.Printf("Failed to encode patch response: %v", err)
			}
			return
		}

		// Limit request body size to prevent DoS attacks
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)

		// Parse request body
		var req PatchIssueRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				w.WriteHeader(http.StatusRequestEntityTooLarge)
				if err := json.NewEncoder(w).Encode(PatchIssueResponse{
					Success: false,
					Error:   "request body too large (max 1MB)",
				}); err != nil {
					log.Printf("Failed to encode patch response: %v", err)
				}
				return
			}
			w.WriteHeader(http.StatusBadRequest)
			if err := json.NewEncoder(w).Encode(PatchIssueResponse{
				Success: false,
				Error:   fmt.Sprintf("invalid request body: %v", err),
			}); err != nil {
				log.Printf("Failed to encode patch response: %v", err)
			}
			return
		}

		// Acquire connection with timeout
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()

		client, err := pool.Get(ctx)
		if err != nil {
			status := http.StatusServiceUnavailable
			if errors.Is(err, context.DeadlineExceeded) {
				status = http.StatusGatewayTimeout
			}
			w.WriteHeader(status)
			if err := json.NewEncoder(w).Encode(PatchIssueResponse{
				Success: false,
				Error:   err.Error(),
			}); err != nil {
				log.Printf("Failed to encode patch response: %v", err)
			}
			return
		}
		defer pool.Put(client)

		// Build UpdateArgs from request
		updateArgs := &rpc.UpdateArgs{
			ID:                 issueID,
			Title:              req.Title,
			Description:        req.Description,
			Status:             req.Status,
			Priority:           req.Priority,
			Assignee:           req.Assignee,
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

		// Execute update
		resp, err := client.Update(updateArgs)
		if err != nil {
			errMsg := err.Error()
			status := http.StatusInternalServerError
			if strings.Contains(errMsg, "not found") {
				status = http.StatusNotFound
			}
			w.WriteHeader(status)
			if err := json.NewEncoder(w).Encode(PatchIssueResponse{
				Success: false,
				Error:   fmt.Sprintf("rpc error: %v", err),
			}); err != nil {
				log.Printf("Failed to encode patch response: %v", err)
			}
			return
		}

		if !resp.Success {
			status := http.StatusInternalServerError
			if strings.Contains(resp.Error, "not found") {
				status = http.StatusNotFound
			} else if strings.Contains(resp.Error, "cannot update template") {
				status = http.StatusConflict
			}
			w.WriteHeader(status)
			if err := json.NewEncoder(w).Encode(PatchIssueResponse{
				Success: false,
				Error:   resp.Error,
			}); err != nil {
				log.Printf("Failed to encode patch response: %v", err)
			}
			return
		}

		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(PatchIssueResponse{
			Success: true,
			Data:    map[string]string{"id": issueID, "status": "updated"},
		}); err != nil {
			log.Printf("Failed to encode patch response: %v", err)
		}
	}
}

// IssueCreateRequest represents the JSON request body for creating an issue.
type IssueCreateRequest struct {
	// Required fields
	Title     string `json:"title"`
	IssueType string `json:"issue_type"`
	Priority  int    `json:"priority"`

	// Optional fields - match rpc.CreateArgs
	ID                 string   `json:"id,omitempty"`
	Parent             string   `json:"parent,omitempty"`
	Description        string   `json:"description,omitempty"`
	Design             string   `json:"design,omitempty"`
	AcceptanceCriteria string   `json:"acceptance_criteria,omitempty"`
	Notes              string   `json:"notes,omitempty"`
	Assignee           string   `json:"assignee,omitempty"`
	Owner              string   `json:"owner,omitempty"`
	CreatedBy          string   `json:"created_by,omitempty"`
	ExternalRef        string   `json:"external_ref,omitempty"`
	EstimatedMinutes   *int     `json:"estimated_minutes,omitempty"`
	Labels             []string `json:"labels,omitempty"`
	Dependencies       []string `json:"dependencies,omitempty"`
	DueAt              string   `json:"due_at,omitempty"`
	DeferUntil         string   `json:"defer_until,omitempty"`
}

// validIssueTypes defines the valid issue types for validation.
var validIssueTypes = map[string]bool{
	"bug":     true,
	"feature": true,
	"task":    true,
	"epic":    true,
	"chore":   true,
}

// Limits for create request validation.
const (
	maxLabels       = 50
	maxDependencies = 100
	maxRequestBody  = 1 << 20 // 1MB
)

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

// createPoolAdapter wraps *daemon.ConnectionPool to implement createConnectionGetter.
type createPoolAdapter struct {
	pool *daemon.ConnectionPool
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
func handleCreateIssue(pool *daemon.ConnectionPool) http.HandlerFunc {
	if pool == nil {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			writeIssuesError(w, http.StatusServiceUnavailable, "connection pool not initialized", "POOL_NOT_INITIALIZED")
		}
	}
	return handleCreateIssueWithPool(&createPoolAdapter{pool: pool})
}

// handleCreateIssueWithPool is the internal implementation that accepts an interface for testing.
func handleCreateIssueWithPool(pool createConnectionGetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

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
			writeIssuesError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error(), "INVALID_JSON")
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
			log.Printf("Connection pool error: %v", err)
			writeIssuesError(w, status, message, code)
			return
		}
		defer pool.Put(client)

		// Convert request to RPC args and call daemon
		createArgs := toCreateArgs(&req)
		resp, err := client.Create(createArgs)
		if err != nil {
			log.Printf("RPC error: %v", err)
			writeIssuesError(w, http.StatusInternalServerError, "failed to create issue: "+err.Error(), "RPC_ERROR")
			return
		}

		if !resp.Success {
			writeIssuesError(w, http.StatusInternalServerError, resp.Error, "DAEMON_ERROR")
			return
		}

		// Return success with created issue
		w.WriteHeader(http.StatusCreated)
		if err := json.NewEncoder(w).Encode(IssuesResponse{
			Success: true,
			Data:    resp.Data,
		}); err != nil {
			log.Printf("Failed to encode create response: %v", err)
		}
	}
}

// handleCloseIssue returns a handler that closes an issue by ID.
func handleCloseIssue(pool *daemon.ConnectionPool) http.HandlerFunc {
	if pool == nil {
		return handleCloseIssueWithPool(nil)
	}
	return handleCloseIssueWithPool(&closePoolAdapter{pool: pool})
}

// handleCloseIssueWithPool is the internal implementation that accepts an interface for testing.
func handleCloseIssueWithPool(pool closeConnectionGetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		// Extract issue ID from path parameter
		issueID := r.PathValue("id")
		if issueID == "" {
			writeErrorResponse(w, http.StatusBadRequest, "missing issue ID")
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
					writeErrorResponse(w, http.StatusRequestEntityTooLarge, "request body too large (max 1MB)")
					return
				}
				writeErrorResponse(w, http.StatusBadRequest, "invalid request body: "+err.Error())
				return
			}
		}

		// Check pool availability
		if pool == nil {
			writeErrorResponse(w, http.StatusServiceUnavailable, "connection pool not initialized")
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
			writeErrorResponse(w, status, "daemon not available")
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
				writeErrorResponse(w, http.StatusNotFound, fmt.Sprintf("issue not found: %s", issueID))
				return
			}
			// Check for "has open blockers" error (when force=false)
			if strings.Contains(err.Error(), "blocker") {
				writeErrorResponse(w, http.StatusConflict, err.Error())
				return
			}
			writeErrorResponse(w, http.StatusInternalServerError, err.Error())
			return
		}

		if !resp.Success {
			writeErrorResponse(w, http.StatusInternalServerError, resp.Error)
			return
		}

		// Return success response with closed issue data
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(CloseResponse{
			Success: true,
			Data:    resp.Data,
		}); err != nil {
			log.Printf("Failed to encode close response: %v", err)
		}
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

// dependencyPoolAdapter wraps *daemon.ConnectionPool to implement dependencyConnectionGetter.
type dependencyPoolAdapter struct {
	pool *daemon.ConnectionPool
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
func handleAddDependency(pool *daemon.ConnectionPool) http.HandlerFunc {
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
			w.WriteHeader(http.StatusBadRequest)
			if err := json.NewEncoder(w).Encode(DependencyResponse{
				Success: false,
				Error:   "invalid request body: " + err.Error(),
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
			w.WriteHeader(status)
			if err := json.NewEncoder(w).Encode(DependencyResponse{
				Success: false,
				Error:   err.Error(),
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
func handleRemoveDependency(pool *daemon.ConnectionPool) http.HandlerFunc {
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
			w.WriteHeader(status)
			if err := json.NewEncoder(w).Encode(DependencyResponse{
				Success: false,
				Error:   err.Error(),
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

// SSEMetrics represents the runtime metrics for the SSE hub.
type SSEMetrics struct {
	ConnectedClients int     `json:"connected_clients"`
	DroppedMutations int64   `json:"dropped_mutations"`
	RetryQueueDepth  int     `json:"retry_queue_depth"`
	UptimeSeconds    float64 `json:"uptime_seconds"`
}

// MetricsResponse wraps the SSE hub metrics for JSON response.
type MetricsResponse struct {
	Success bool        `json:"success"`
	Data    *SSEMetrics `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}

// handleMetrics returns a handler that exposes SSE hub runtime metrics.
func handleMetrics(hub *SSEHub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if hub == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			if err := json.NewEncoder(w).Encode(MetricsResponse{
				Success: false,
				Error:   "SSE hub not initialized",
			}); err != nil {
				log.Printf("Failed to encode metrics response: %v", err)
			}
			return
		}
		metrics := &SSEMetrics{
			ConnectedClients: hub.ClientCount(),
			DroppedMutations: hub.GetDroppedCount(),
			RetryQueueDepth:  hub.GetRetryQueueDepth(),
			UptimeSeconds:    hub.GetUptime().Seconds(),
		}
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(MetricsResponse{
			Success: true,
			Data:    metrics,
		}); err != nil {
			log.Printf("Failed to encode metrics response: %v", err)
		}
	}
}

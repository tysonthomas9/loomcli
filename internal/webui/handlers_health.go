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

	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/types"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
	"github.com/tysonthomas9/loomcli/internal/webui/fleet"
)

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
	List(args *rpc.ListArgs) (*rpc.Response, error)
}

// readyConnectionGetter is an internal interface for testing ready handler pool operations.
type readyConnectionGetter interface {
	Get(ctx context.Context) (readyClient, error)
	Put(client readyClient)
}

// readyPoolAdapter wraps daemon.Pool to implement readyConnectionGetter.
type readyPoolAdapter struct {
	pool daemon.Pool
}

func (p *readyPoolAdapter) Get(ctx context.Context) (readyClient, error) {
	return p.pool.Get(ctx)
}

func (p *readyPoolAdapter) Put(client readyClient) {
	if c, ok := client.(*rpc.Client); ok {
		p.pool.Put(c)
	}
}

// SSEMetrics represents the runtime metrics for the SSE hub.
type SSEMetrics struct {
	ConnectedClients     int     `json:"connected_clients"`
	DroppedMutations     int64   `json:"dropped_mutations"`
	RetryQueueDepth      int     `json:"retry_queue_depth"`
	UptimeSeconds        float64 `json:"uptime_seconds"`
	FleetTimeoutsTotal   int64   `json:"loom_fleet_timeouts_total,omitempty"`
	FleetClaimsSuccess   int64   `json:"loom_fleet_claims_success,omitempty"`
	FleetClaimsCollision int64   `json:"loom_fleet_claims_collision,omitempty"`
	FleetClaimsTimeout   int64   `json:"loom_fleet_claims_timeout,omitempty"`
	FleetClaimsTotal     int64   `json:"loom_fleet_claims_total,omitempty"`
}

// MetricsResponse wraps the SSE hub metrics for JSON response.
type MetricsResponse struct {
	Success bool        `json:"success"`
	Data    *SSEMetrics `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}

// handleReady returns issues ready to work on (open/in_progress with no blockers).
func handleReady(pool daemon.Pool) http.HandlerFunc {
	if pool == nil {
		return handleReadyWithPool(nil)
	}
	return handleReadyWithPool(&readyPoolAdapter{pool: pool})
}

// handleReadyWithPool is the internal implementation that accepts an interface for testing.
func handleReadyWithPool(pool readyConnectionGetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if pool == nil {
			respondJSON(w, http.StatusServiceUnavailable, ReadyResponse{
				Success: false,
				Error:   "connection pool not initialized",
			})
			return
		}

		// Parse query parameters into ReadyArgs
		args, err := parseReadyParams(r)
		if err != nil {
			respondJSON(w, http.StatusBadRequest, ReadyResponse{
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
			log.Printf("Pool error in handleReady: %v", err)
			respondJSON(w, status, ReadyResponse{
				Success: false,
				Error:   "daemon not available",
			})
			return
		}
		defer pool.Put(client)

		// Execute Ready RPC call
		resp, err := client.Ready(args)
		if err != nil {
			log.Printf("RPC error in handleReady: %v", err)
			respondJSON(w, http.StatusInternalServerError, ReadyResponse{
				Success: false,
				Error:   "internal server error",
			})
			return
		}

		if !resp.Success {
			respondJSON(w, http.StatusInternalServerError, ReadyResponse{
				Success: false,
				Error:   resp.Error,
			})
			return
		}

		// Parse the issues from RPC response
		var issues []*types.Issue
		if err := json.Unmarshal(resp.Data, &issues); err != nil {
			respondJSON(w, http.StatusInternalServerError, ReadyResponse{
				Success: false,
				Error:   fmt.Sprintf("failed to parse ready issues: %v", err),
			})
			return
		}

		// Filter out issues with unclosed blockers.
		// bd ready considers in_progress blockers as "resolved" but we require
		// blockers to be closed before dependents become available.
		issues = filterUnclosedBlockers(client, issues)

		// If no issues, return empty response
		if len(issues) == 0 {
			respondJSON(w, http.StatusOK, ReadyResponse{
				Success: true,
				Data:    []*ReadyIssueWithParent{},
			})
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

		respondJSON(w, http.StatusOK, ReadyResponse{
			Success: true,
			Data:    issuesWithParent,
		})
	}
}

// handleMetrics returns a handler that exposes SSE hub runtime metrics.
func handleMetrics(hub *SSEHub, timeoutEnforcer *fleet.TimeoutEnforcer, claimMetrics *fleet.ClaimMetrics) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if hub == nil {
			respondJSON(w, http.StatusServiceUnavailable, MetricsResponse{
				Success: false,
				Error:   "SSE hub not initialized",
			})
			return
		}
		metrics := &SSEMetrics{
			ConnectedClients: hub.ClientCount(),
			DroppedMutations: hub.GetDroppedCount(),
			RetryQueueDepth:  hub.GetRetryQueueDepth(),
			UptimeSeconds:    hub.GetUptime().Seconds(),
		}
		if timeoutEnforcer != nil {
			metrics.FleetTimeoutsTotal = timeoutEnforcer.GetTimeoutCount()
		}
		if claimMetrics != nil {
			snap := claimMetrics.Snapshot()
			metrics.FleetClaimsSuccess = snap.Success
			metrics.FleetClaimsCollision = snap.Collision
			metrics.FleetClaimsTimeout = snap.Timeout
			metrics.FleetClaimsTotal = snap.Total
		}
		respondJSON(w, http.StatusOK, MetricsResponse{
			Success: true,
			Data:    metrics,
		})
	}
}

// filterUnclosedBlockers removes issues whose blocking dependencies are not yet closed.
// It fetches all issues via client.List() to build the unclosed set.
// On error, returns the original list unfiltered (non-fatal).
func filterUnclosedBlockers(client readyClient, issues []*types.Issue) []*types.Issue {
	listResp, err := client.List(&rpc.ListArgs{Limit: 500})
	if err != nil {
		log.Printf("Failed to fetch issue list for blocker filtering: %v", err)
		return issues
	}
	if !listResp.Success {
		log.Printf("List RPC failed for blocker filtering: %s", listResp.Error)
		return issues
	}

	var allIssues []*types.Issue
	if err := json.Unmarshal(listResp.Data, &allIssues); err != nil {
		log.Printf("Failed to parse issue list for blocker filtering: %v", err)
		return issues
	}

	unclosedIDs := make(map[string]bool, len(allIssues))
	for _, issue := range allIssues {
		if issue.Status != types.StatusClosed {
			unclosedIDs[issue.ID] = true
		}
	}

	filtered := make([]*types.Issue, 0, len(issues))
	for _, issue := range issues {
		if !hasUnclosedBlockersTyped(issue.Dependencies, unclosedIDs) {
			filtered = append(filtered, issue)
		}
	}
	return filtered
}

// hasUnclosedBlockersTyped returns true if any "blocks" dependency points to
// an issue that is still unclosed. Uses types.Dependency (pointer slice).
func hasUnclosedBlockersTyped(deps []*types.Dependency, unclosedIDs map[string]bool) bool {
	for _, dep := range deps {
		if dep.Type == types.DepBlocks && unclosedIDs[dep.DependsOnID] {
			return true
		}
	}
	return false
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

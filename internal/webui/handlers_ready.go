package webui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/types"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
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

// handleReady returns issues ready to work on (open/in_progress with no blockers).
func handleReady(pool daemon.Pool) http.HandlerFunc {
	if pool == nil {
		return handleReadyWithPool(nil)
	}
	return handleReadyWithPool(&readyPoolAdapter{pool: pool})
}

// executeReadyRPC acquires a connection, calls Ready, and returns filtered issues.
func executeReadyRPC(ctx context.Context, pool readyConnectionGetter, args *rpc.ReadyArgs) (readyClient, []*types.Issue, int, error) {
	client, err := pool.Get(ctx)
	if err != nil {
		status := http.StatusServiceUnavailable
		if errors.Is(err, context.DeadlineExceeded) {
			status = http.StatusGatewayTimeout
		}
		return nil, nil, status, err
	}

	resp, err := client.Ready(args)
	if err != nil {
		pool.Put(client)
		return nil, nil, http.StatusInternalServerError, fmt.Errorf("RPC error: %w", err)
	}

	if !resp.Success {
		pool.Put(client)
		return nil, nil, http.StatusInternalServerError, fmt.Errorf("%s", resp.Error)
	}

	var issues []*types.Issue
	if err := json.Unmarshal(resp.Data, &issues); err != nil {
		pool.Put(client)
		return nil, nil, http.StatusInternalServerError, fmt.Errorf("failed to parse ready issues: %w", err)
	}

	issues = filterUnclosedBlockers(client, issues)
	return client, issues, 0, nil
}

// buildReadyResponse enriches issues with parent info for the response.
func buildReadyResponse(client readyClient, issues []*types.Issue) []*ReadyIssueWithParent {
	issueIDs := make([]string, len(issues))
	for i, issue := range issues {
		issueIDs[i] = issue.ID
	}

	parentResp, err := client.GetParentIDs(&rpc.GetParentIDsArgs{IssueIDs: issueIDs})
	if err != nil {
		log.Printf("Failed to get parent IDs for ready issues: %v", err)
		parentResp = &rpc.GetParentIDsResponse{Parents: make(map[string]*rpc.ParentInfo)}
	}

	result := make([]*ReadyIssueWithParent, len(issues))
	for i, issue := range issues {
		iwp := &ReadyIssueWithParent{Issue: issue}
		if parentInfo, ok := parentResp.Parents[issue.ID]; ok {
			iwp.Parent = &parentInfo.ParentID
			iwp.ParentTitle = &parentInfo.ParentTitle
		}
		result[i] = iwp
	}
	return result
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

		args, err := parseReadyParams(r)
		if err != nil {
			respondJSON(w, http.StatusBadRequest, ReadyResponse{
				Success: false,
				Error:   err.Error(),
			})
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		client, issues, status, err := executeReadyRPC(ctx, pool, args)
		if err != nil {
			log.Printf("handleReady error: %v", err)
			respondJSON(w, status, ReadyResponse{
				Success: false,
				Error:   err.Error(),
			})
			return
		}
		defer pool.Put(client)

		if len(issues) == 0 {
			respondJSON(w, http.StatusOK, ReadyResponse{
				Success: true,
				Data:    []*ReadyIssueWithParent{},
			})
			return
		}

		respondJSON(w, http.StatusOK, ReadyResponse{
			Success: true,
			Data:    buildReadyResponse(client, issues),
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
	args.Assignee = parseStringParam(q, "assignee")
	args.Type = parseStringParam(q, "type")
	args.ParentID = parseStringParam(q, "parent_id")

	// Validated string parameters
	if v := parseStringParam(q, "mol_type"); v != "" {
		if !types.MolType(v).IsValid() {
			return nil, fmt.Errorf("invalid mol_type: %s (must be swarm, patrol, or work)", v)
		}
		args.MolType = v
	}
	if v := parseStringParam(q, "sort"); v != "" {
		if !types.SortPolicy(v).IsValid() {
			return nil, fmt.Errorf("invalid sort policy: %s (must be hybrid, priority, or oldest)", v)
		}
		args.SortPolicy = v
	}

	// Boolean parameters
	var err error
	if args.Unassigned, err = parseBoolParam(q, "unassigned"); err != nil {
		return nil, err
	}
	if args.IncludeDeferred, err = parseBoolParam(q, "include_deferred"); err != nil {
		return nil, err
	}

	// Integer parameters
	if args.Priority, err = parseIntParam(q, "priority"); err != nil {
		return nil, err
	}
	if args.Priority != nil && (*args.Priority < 0 || *args.Priority > 4) {
		return nil, fmt.Errorf("priority must be between 0 and 4 (got %d)", *args.Priority)
	}

	limitPtr, err := parseIntParam(q, "limit")
	if err != nil {
		return nil, err
	}
	if limitPtr != nil {
		if *limitPtr < 0 {
			return nil, fmt.Errorf("limit must be non-negative (got %d)", *limitPtr)
		}
		args.Limit = *limitPtr
	}

	// Array parameters
	args.Labels = parseArrayParam(q, "labels")
	args.LabelsAny = parseArrayParam(q, "labels_any")

	return args, nil
}

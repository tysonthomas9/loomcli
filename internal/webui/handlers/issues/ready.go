package issues

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"net/url"

	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/types"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
)

// ReadyIssueWithParent extends Issue with parent info for /api/ready.
// This enables epic swimlane grouping in the Kanban view.
type ReadyIssueWithParent struct {
	*types.Issue
	Parent      *string `json:"parent,omitempty"`       // Parent issue ID (null for root-level issues)
	ParentTitle *string `json:"parent_title,omitempty"` // Parent issue title for display
	Repo        *string `json:"repo,omitempty"`         // Repository that owns this issue
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

// HandleReady returns issues ready to work on (open/in_progress with no blockers).
func HandleReady(pool daemon.Pool) http.HandlerFunc {
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
		slog.Error("failed to get parent IDs for ready issues", "err", err)
		parentResp = &rpc.GetParentIDsResponse{Parents: make(map[string]*rpc.ParentInfo)}
	}

	result := make([]*ReadyIssueWithParent, len(issues))
	for i, issue := range issues {
		iwp := &ReadyIssueWithParent{Issue: issue}
		if parentInfo, ok := parentResp.Parents[issue.ID]; ok {
			iwp.Parent = &parentInfo.ParentID
			iwp.ParentTitle = &parentInfo.ParentTitle
		}
		if issue.SourceRepo != "" {
			iwp.Repo = &issue.SourceRepo
		}
		result[i] = iwp
	}
	return result
}

// handleReadyWithPool is the internal implementation that accepts an interface for testing.
func handleReadyWithPool(pool readyConnectionGetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if pool == nil {
			handler.WriteJSON(w, http.StatusServiceUnavailable, ReadyResponse{
				Success: false,
				Error:   "connection pool not initialized",
			})
			return
		}

		args, err := parseReadyParams(r)
		if err != nil {
			handler.WriteJSON(w, http.StatusBadRequest, ReadyResponse{
				Success: false,
				Error:   err.Error(),
			})
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		client, issues, status, err := executeReadyRPC(ctx, pool, args)
		if err != nil {
			slog.Error("handleReady error", "err", err)
			handler.WriteJSON(w, status, ReadyResponse{
				Success: false,
				Error:   err.Error(),
			})
			return
		}
		defer pool.Put(client)

		if len(issues) == 0 {
			handler.WriteJSON(w, http.StatusOK, ReadyResponse{
				Success: true,
				Data:    []*ReadyIssueWithParent{},
			})
			return
		}

		handler.WriteJSON(w, http.StatusOK, ReadyResponse{
			Success: true,
			Data:    buildReadyResponse(client, issues),
		})
	}
}

// filterUnclosedBlockers removes issues whose blocking dependencies are not yet closed.
// It extracts dependency target IDs from the ready result and fetches only those
// specific issues via client.List(IDs: ...) instead of a full table scan.
// On error, returns the original list unfiltered (non-fatal).
func filterUnclosedBlockers(client readyClient, issues []*types.Issue) []*types.Issue {
	// Extract unique dependency target IDs that affect ready work
	depIDSet := make(map[string]struct{})
	for _, issue := range issues {
		for _, dep := range issue.Dependencies {
			if dep.Type.AffectsReadyWork() {
				depIDSet[dep.DependsOnID] = struct{}{}
			}
		}
	}

	// Fast path: no blocking dependencies means nothing to filter
	if len(depIDSet) == 0 {
		return issues
	}

	depIDs := make([]string, 0, len(depIDSet))
	for id := range depIDSet {
		depIDs = append(depIDs, id)
	}

	listResp, err := client.List(&rpc.ListArgs{IDs: depIDs})
	if err != nil {
		slog.Error("failed to fetch blocker issues for filtering", "err", err)
		return issues
	}
	if !listResp.Success {
		slog.Error("list RPC failed for blocker filtering", "err", listResp.Error)
		return issues
	}

	var blockerIssues []*types.Issue
	if err := json.Unmarshal(listResp.Data, &blockerIssues); err != nil {
		slog.Error("failed to parse blocker issues for filtering", "err", err)
		return issues
	}

	unclosedIDs := make(map[string]bool, len(blockerIssues))
	for _, issue := range blockerIssues {
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

// hasUnclosedBlockersTyped returns true if any blocking dependency points to
// an issue that is still unclosed. Uses types.Dependency (pointer slice).
func hasUnclosedBlockersTyped(deps []*types.Dependency, unclosedIDs map[string]bool) bool {
	for _, dep := range deps {
		if dep.Type.IsDirectBlocker() && unclosedIDs[dep.DependsOnID] {
			return true
		}
	}
	return false
}

// parseReadyParams parses query parameters into rpc.ReadyArgs.
func parseReadyParams(r *http.Request) (*rpc.ReadyArgs, error) {
	args := &rpc.ReadyArgs{}
	q := r.URL.Query()

	args.Assignee = handler.ParseStringParam(q, "assignee")
	args.Type = handler.ParseStringParam(q, "type")
	args.ParentID = handler.ParseStringParam(q, "parent_id")

	if err := parseReadyValidatedStrings(q, args); err != nil {
		return nil, err
	}

	var err error
	if args.Unassigned, err = handler.ParseBoolParam(q, "unassigned"); err != nil {
		return nil, err
	}
	if args.IncludeDeferred, err = handler.ParseBoolParam(q, "include_deferred"); err != nil {
		return nil, err
	}
	if err := parseReadyIntParams(q, args); err != nil {
		return nil, err
	}

	args.Labels = handler.ParseArrayParam(q, "labels")
	args.LabelsAny = handler.ParseArrayParam(q, "labels_any")
	args.SourceRepos = handler.ParseArrayParam(q, "source_repos")

	return args, nil
}

// parseReadyValidatedStrings parses and validates mol_type and sort parameters.
func parseReadyValidatedStrings(q url.Values, args *rpc.ReadyArgs) error {
	if v := handler.ParseStringParam(q, "mol_type"); v != "" {
		if !types.MolType(v).IsValid() {
			return fmt.Errorf("invalid mol_type: %s (must be swarm, patrol, or work)", v)
		}
		args.MolType = v
	}
	if v := handler.ParseStringParam(q, "sort"); v != "" {
		if !types.SortPolicy(v).IsValid() {
			return fmt.Errorf("invalid sort policy: %s (must be hybrid, priority, or oldest)", v)
		}
		args.SortPolicy = v
	}
	return nil
}

// parseReadyIntParams parses priority and limit integer parameters.
func parseReadyIntParams(q url.Values, args *rpc.ReadyArgs) error {
	var err error
	if args.Priority, err = handler.ParseIntParam(q, "priority"); err != nil {
		return err
	}
	if args.Priority != nil && (*args.Priority < 0 || *args.Priority > 4) {
		return fmt.Errorf("priority must be between 0 and 4 (got %d)", *args.Priority)
	}
	limitPtr, err := handler.ParseIntParam(q, "limit")
	if err != nil {
		return err
	}
	if limitPtr != nil {
		if *limitPtr < 0 {
			return fmt.Errorf("limit must be non-negative (got %d)", *limitPtr)
		}
		args.Limit = *limitPtr
	}
	return nil
}

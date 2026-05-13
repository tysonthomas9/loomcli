package service

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/types"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

func (s *issueServiceImpl) ListIssues(ctx context.Context, params ListIssuesParams) (*ListIssuesResult, error) {
	// Pool-less fleet mode: there is no daemon to connect to, so go
	// directly through the IssueBackend. The pool path below only runs
	// when multiPool/pool is wired.
	if s.pool == nil {
		if be, _ := s.resolveBackend(ctx); be != nil {
			return s.listIssuesViaBackend(ctx, be, params)
		}
		return nil, ErrUnavailable("connection pool not initialized")
	}

	// Mixed deployment: a pool exists for the local workspace, but other
	// workspaces (fleet-db-only) won't have one. When the request's
	// workspace isn't in the pool registry, route to the IssueBackend
	// instead of opening a daemon connection that would return the wrong
	// workspace's data.
	if wsID := middleware.WorkspaceFromContext(ctx); wsID != "" && s.multiPool != nil {
		if s.multiPool.PoolForWorkspace(wsID) == nil {
			if be, _ := s.resolveBackend(ctx); be != nil {
				return s.listIssuesViaBackend(ctx, be, params)
			}
		}
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	client, err := s.pool.Get(ctx)
	if err != nil {
		// Pool acquisition failed. In fleet mode the pool is intentionally
		// non-functional (no local issue daemon at all), so before bubbling
		// "issue backend unavailable" up to the FE, see whether a backend is
		// wired and serve the list from there.
		if be, _ := s.resolveBackend(ctx); be != nil {
			return s.listIssuesViaBackend(ctx, be, params)
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, ErrTimeout("timeout connecting to issue backend")
		}
		slog.Error("connection pool error", "err", err)
		return nil, ErrUnavailable("issue backend unavailable")
	}
	rpcOK := false
	defer s.releaseClient(client, &rpcOK)

	// Try the composite ListKanban RPC first — 1 round-trip instead of 3.
	kanbanArgs := &rpc.ListKanbanArgs{
		ListArgs:       *params.Args,
		IncludeBlocked: params.IncludeBlocked,
		ExcludeStatus:  params.ExcludeStatus,
	}
	kanbanResp, kanbanErr := client.ListKanban(kanbanArgs)
	if kanbanErr == nil {
		rpcOK = true
		return buildResultFromKanbanRPC(kanbanResp, params.IncludeBlocked), nil
	}
	slog.Error("ListKanban RPC error", "err", kanbanErr)
	return nil, ErrInternal("failed to list issues", kanbanErr)
}

// buildResultFromKanbanRPC converts the composite RPC response into the
// service-layer result shape. When includeBlocked is true, client-side blocker
// refinement runs against the fetched set; blockers are considered resolved
// only when closed.
func buildResultFromKanbanRPC(resp *rpc.ListKanbanResponse, includeBlocked bool) *ListIssuesResult {
	if !includeBlocked {
		issuesWithParent := make([]IssueWithParent, len(resp.Issues))
		for i, ki := range resp.Issues {
			issuesWithParent[i] = kanbanRPCToIssueWithParent(ki)
		}
		return &ListIssuesResult{Issues: issuesWithParent}
	}

	iwcSlice := make([]*types.IssueWithCounts, len(resp.Issues))
	for i, ki := range resp.Issues {
		iwcSlice[i] = ki.IssueWithCounts
	}
	unclosedIDs, issueMap := buildUnclosedSetsFromFetched(iwcSlice)

	kanbanIssues := make([]KanbanIssue, len(resp.Issues))
	for i, ki := range resp.Issues {
		kanbanIssues[i] = kanbanRPCToKanbanIssue(ki, unclosedIDs, issueMap)
	}
	return &ListIssuesResult{KanbanIssues: kanbanIssues}
}

func kanbanRPCToIssueWithParent(ki *rpc.KanbanIssueRPC) IssueWithParent {
	iwp := IssueWithParent{IssueWithCounts: ki.IssueWithCounts}
	if ki.ParentID != "" {
		pid, pt := ki.ParentID, ki.ParentTitle
		iwp.Parent = &pid
		iwp.ParentTitle = &pt
	}
	if ki.Repo != "" {
		repo := ki.Repo
		iwp.Repo = &repo
	}
	return iwp
}

func kanbanRPCToKanbanIssue(ki *rpc.KanbanIssueRPC, unclosedIDs map[string]bool, issueMap map[string]*types.IssueWithCounts) KanbanIssue {
	out := KanbanIssue{IssueWithCounts: ki.IssueWithCounts}
	if ki.ParentID != "" {
		pid, pt := ki.ParentID, ki.ParentTitle
		out.Parent = &pid
		out.ParentTitle = &pt
	}
	if ki.Repo != "" {
		repo := ki.Repo
		out.Repo = &repo
	}
	refs := getUnclosedBlockerRefs(ki.Issue.Dependencies, unclosedIDs, issueMap)
	if len(refs) > 0 {
		out.IsBlocked = true
		out.BlockedByCount = len(refs)
		out.BlockedBy = extractBlockerIDs(refs)
		out.BlockedByDetails = refs
	} else if ki.IsBlocked {
		out.IsBlocked = true
		out.BlockedByCount = ki.BlockedByCount
		out.BlockedBy = ki.BlockedBy
		out.BlockedByDetails = ki.BlockedByDetails
	}
	return out
}

// listIssuesViaBackend serves a list request through the IssueBackend
// abstraction. Used when there is no daemon connection pool to talk to —
// most importantly, in fleet mode where the local issue daemon is intentionally
// absent and every list query has to hit the fleet HTTP API instead.
//
// This port covers the standard (non-kanban) list shape and enriches kanban
// blocked state from IssueBackend.Blocked. Parent-title enrichment requires
// extra Get calls per issue and is skipped — the FE shows the parent ID
// without a title, which the kanban handles as a graceful fallback (see
// kanbanIssue.go in the FE).
//
// The composite ListKanban RPC's per-issue parent-title and full
// blocked-by-details lookups are intentionally not reproduced: matching them
// would mean N extra Gets. The blocked summary uses one backend Blocked call
// instead, which preserves kanban correctness without the p95 hit.
func (s *issueServiceImpl) listIssuesViaBackend(
	ctx context.Context, be backend.IssueBackend, params ListIssuesParams,
) (*ListIssuesResult, error) {
	opts := listArgsToBackendOpts(params.Args)
	issues, err := be.List(ctx, opts)
	if err != nil {
		return nil, translateBackendError(err)
	}
	issues = excludeBackendIssuesByStatus(issues, params.ExcludeStatus)

	if !params.IncludeBlocked {
		return &ListIssuesResult{Issues: backendIssuesWithParent(issues)}, nil
	}

	blockedByID, err := s.blockedIssueMap(ctx, be, params.Args)
	if err != nil {
		return nil, err
	}
	issues = appendMissingBlockedIssues(issues, blockedByID)

	return &ListIssuesResult{KanbanIssues: backendKanbanIssues(issues, blockedByID)}, nil
}

// Apply ExcludeStatus client-side. fleet-db's ListOpts has a single Status
// filter, not an exclude set; doing it here keeps the contract uniform with the
// pool-based path.
func excludeBackendIssuesByStatus(issues []backend.IssueData, excludeStatus []string) []backend.IssueData {
	if len(excludeStatus) == 0 {
		return issues
	}
	excluded := make(map[string]bool, len(excludeStatus))
	for _, status := range excludeStatus {
		excluded[status] = true
	}
	filtered := issues[:0]
	for _, issue := range issues {
		if !excluded[issue.Status] {
			filtered = append(filtered, issue)
		}
	}
	return filtered
}

func backendIssuesWithParent(issues []backend.IssueData) []IssueWithParent {
	out := make([]IssueWithParent, len(issues))
	for i, d := range issues {
		out[i] = backendIssueWithParent(d)
	}
	return out
}

func backendIssueWithParent(d backend.IssueData) IssueWithParent {
	iwc := backendIssueDataToWithCounts(&d)
	iwp := IssueWithParent{IssueWithCounts: iwc}
	if d.Parent != "" {
		p := d.Parent
		iwp.Parent = &p
	}
	if d.SourceRepo != "" {
		r := d.SourceRepo
		iwp.Repo = &r
	}
	return iwp
}

func appendMissingBlockedIssues(
	issues []backend.IssueData,
	blockedByID map[string]backend.IssueData,
) []backend.IssueData {
	if len(blockedByID) == 0 {
		return issues
	}
	seen := make(map[string]bool, len(issues))
	for _, d := range issues {
		seen[d.ID] = true
	}
	for _, d := range blockedByID {
		if !seen[d.ID] {
			issues = append(issues, d)
		}
	}
	return issues
}

func backendKanbanIssues(
	issues []backend.IssueData,
	blockedByID map[string]backend.IssueData,
) []KanbanIssue {
	out := make([]KanbanIssue, len(issues))
	for i, d := range issues {
		out[i] = backendKanbanIssue(d, blockedByID[d.ID])
	}
	return out
}

func backendKanbanIssue(d backend.IssueData, blocked backend.IssueData) KanbanIssue {
	if blocked.ID != "" && (d.Status == "" || d.Status == string(types.StatusOpen)) {
		d.Status = string(types.StatusBlocked)
	}

	iwc := backendIssueDataToWithCounts(&d)
	ki := KanbanIssue{IssueWithCounts: iwc}
	if d.Parent != "" {
		p := d.Parent
		ki.Parent = &p
	}
	if d.SourceRepo != "" {
		r := d.SourceRepo
		ki.Repo = &r
	}
	if blocked.ID != "" {
		applyBlockedSummary(&ki, blocked)
	}
	return ki
}

func applyBlockedSummary(ki *KanbanIssue, blocked backend.IssueData) {
	ki.IsBlocked = true
	ki.BlockedByCount = blocked.BlockedByCount
	ki.BlockedBy = append([]string(nil), blocked.BlockedBy...)
	if ki.BlockedByCount == 0 {
		ki.BlockedByCount = len(ki.BlockedBy)
	}
	if ki.BlockedByCount == 0 {
		ki.BlockedByCount = 1
	}
}

func (s *issueServiceImpl) blockedIssueMap(
	ctx context.Context,
	be backend.IssueBackend,
	args *rpc.ListArgs,
) (map[string]backend.IssueData, error) {
	blocked, err := be.Blocked(ctx, blockedOptsFromListArgs(args))
	if err != nil {
		slog.Error("backend error in ListIssues.Blocked", "err", err)
		return nil, translateBackendError(err)
	}
	out := make(map[string]backend.IssueData, len(blocked))
	for _, d := range blocked {
		out[d.ID] = d
	}
	return out, nil
}

func blockedOptsFromListArgs(args *rpc.ListArgs) backend.BlockedOpts {
	if args == nil {
		return backend.BlockedOpts{}
	}
	return backend.BlockedOpts{
		ParentID: args.ParentID,
		Assignee: args.Assignee,
		Priority: args.Priority,
		Type:     args.IssueType,
	}
}

// backendIssueDataToWithCounts adapts the slim list projection
// (backend.IssueData) into the FE-facing types.IssueWithCounts shape.
// Counts come straight off IssueData; the embedded Issue is populated
// from the same fields. Fields the slim projection doesn't carry
// (description, dependencies, comments, etc.) stay zero-valued.
func backendIssueDataToWithCounts(d *backend.IssueData) *types.IssueWithCounts {
	if d == nil {
		return nil
	}
	issue := &types.Issue{
		ID:          d.ID,
		Title:       d.Title,
		Status:      types.Status(d.Status),
		Priority:    d.Priority,
		IssueType:   types.IssueType(d.IssueType),
		Assignee:    d.Assignee,
		Owner:       d.Owner,
		Labels:      d.Labels,
		Design:      d.Design,
		CreatedAt:   d.CreatedAt,
		UpdatedAt:   d.UpdatedAt,
		DueAt:       d.DueAt,
		DeferUntil:  d.DeferUntil,
		CreatedBy:   d.CreatedBy,
		ClosedAt:    d.ClosedAt,
		CloseReason: d.CloseReason,
	}
	if d.SourceRepo != "" {
		issue.SourceRepo = d.SourceRepo
	}
	return &types.IssueWithCounts{
		Issue:           issue,
		DependencyCount: d.DependencyCount,
		DependentCount:  d.DependentCount,
	}
}

// listArgsToBackendOpts converts the rpc.ListArgs the webui handler
// composes for the daemon path into the backend.ListOpts shape. We map
// only fields that backend.IssueBackend.List accepts uniformly across
// drivers — fleet-db rejects several optional filters (see ListOpts
// "fleet-db: unsupported" comments), so passing them through would
// surface as runtime errors instead of being silently ignored.
func listArgsToBackendOpts(a *rpc.ListArgs) backend.ListOpts {
	if a == nil {
		return backend.ListOpts{}
	}
	return backend.ListOpts{
		Status:    a.Status,
		IssueType: a.IssueType,
		Assignee:  a.Assignee,
		Labels:    a.Labels,
		ParentID:  a.ParentID,
		Limit:     a.Limit,
	}
}

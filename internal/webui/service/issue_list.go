package service

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/types"
)

func (s *issueServiceImpl) ListIssues(ctx context.Context, params ListIssuesParams) (*ListIssuesResult, error) {
	// Pool-less fleet mode: there is no daemon to connect to, so go
	// directly through the IssueBackend. The pool path below only runs
	// in beads mode where multiPool/pool is wired.
	if s.pool == nil {
		if be, _ := s.resolveBackend(); be != nil {
			return s.listIssuesViaBackend(ctx, be, params)
		}
		return nil, ErrUnavailable("connection pool not initialized")
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	client, err := s.pool.Get(ctx)
	if err != nil {
		// Pool acquisition failed. In fleet mode the pool is intentionally
		// non-functional (no bd daemon at all), so before bubbling
		// "daemon unavailable" up to the FE, see whether a backend is
		// wired and serve the list from there.
		if be, _ := s.resolveBackend(); be != nil {
			return s.listIssuesViaBackend(ctx, be, params)
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, ErrTimeout("timeout connecting to daemon")
		}
		slog.Error("connection pool error", "err", err)
		return nil, ErrUnavailable("daemon unavailable")
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
	if !isUnknownOperation(kanbanErr) {
		slog.Error("ListKanban RPC error", "err", kanbanErr)
		return nil, ErrInternal("failed to list issues", kanbanErr)
	}
	return s.listIssuesLegacy(client, params, &rpcOK)
}

// listIssuesLegacy is the pre-ListKanban 3-call path for old daemons.
func (s *issueServiceImpl) listIssuesLegacy(client *rpc.Client, params ListIssuesParams, rpcOK *bool) (*ListIssuesResult, error) {
	issuesWithCounts, err := s.fetchAndFilterIssues(client, params)
	if err != nil {
		return nil, err
	}

	if len(issuesWithCounts) == 0 {
		*rpcOK = true
		if params.IncludeBlocked {
			return &ListIssuesResult{KanbanIssues: []KanbanIssue{}}, nil
		}
		return &ListIssuesResult{Issues: []IssueWithParent{}}, nil
	}

	parentResp, parentClean := s.batchGetParentIDs(client, issuesWithCounts)

	if params.IncludeBlocked {
		result, kanbanClean := s.buildKanbanResult(client, issuesWithCounts, parentResp)
		*rpcOK = parentClean && kanbanClean
		return result, nil
	}

	*rpcOK = parentClean
	return s.buildStandardResult(issuesWithCounts, parentResp), nil
}

// isUnknownOperation returns true when the daemon rejects an op it doesn't know.
// Used to detect old daemons that don't implement ListKanban yet.
func isUnknownOperation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "unknown operation") || strings.Contains(msg, "unknown:")
}

// buildResultFromKanbanRPC converts the composite RPC response into the
// service-layer result shape. When includeBlocked is true, client-side blocker
// refinement runs against the fetched set (blockers are only considered
// resolved when closed, matching the legacy path).
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

func (s *issueServiceImpl) fetchAndFilterIssues(client *rpc.Client, params ListIssuesParams) ([]*types.IssueWithCounts, error) {
	resp, err := client.List(params.Args)
	if err != nil {
		slog.Error("RPC error", "err", err)
		return nil, ErrInternal("failed to list issues", err)
	}
	if !resp.Success {
		return nil, ErrInternal(resp.Error, nil)
	}

	var issuesWithCounts []*types.IssueWithCounts
	if err := json.Unmarshal(resp.Data, &issuesWithCounts); err != nil {
		slog.Error("failed to parse issues", "err", err)
		return nil, ErrInternal("failed to parse issues", err)
	}

	if len(params.ExcludeStatus) > 0 {
		excludeSet := make(map[types.Status]bool, len(params.ExcludeStatus))
		for _, st := range params.ExcludeStatus {
			excludeSet[types.Status(st)] = true
		}
		filtered := make([]*types.IssueWithCounts, 0, len(issuesWithCounts))
		for _, iwc := range issuesWithCounts {
			if !excludeSet[iwc.Issue.Status] {
				filtered = append(filtered, iwc)
			}
		}
		issuesWithCounts = filtered
	}

	return issuesWithCounts, nil
}

// batchGetParentIDs fetches parent IDs in batches. The second return is false
// if any batch hit a transport error — callers should not return the connection
// to the pool in that case (stale bytes may remain in the read buffer).
func (s *issueServiceImpl) batchGetParentIDs(client *rpc.Client, issues []*types.IssueWithCounts) (*rpc.GetParentIDsResponse, bool) {
	issueIDs := make([]string, len(issues))
	for i, iwc := range issues {
		issueIDs[i] = iwc.Issue.ID
	}

	clean := true
	parentResp := &rpc.GetParentIDsResponse{Parents: make(map[string]*rpc.ParentInfo)}
	for i := 0; i < len(issueIDs); i += parentBatchSize {
		end := i + parentBatchSize
		if end > len(issueIDs) {
			end = len(issueIDs)
		}
		batch, err := client.GetParentIDs(&rpc.GetParentIDsArgs{IssueIDs: issueIDs[i:end]})
		if err != nil {
			slog.Error("failed to get parent IDs", "batch_start", i, "batch_end", end, "err", err)
			clean = false
			continue
		}
		for k, v := range batch.Parents {
			parentResp.Parents[k] = v
		}
	}
	return parentResp, clean
}

func (s *issueServiceImpl) buildStandardResult(issuesWithCounts []*types.IssueWithCounts, parentResp *rpc.GetParentIDsResponse) *ListIssuesResult {
	issues := make([]IssueWithParent, len(issuesWithCounts))
	for i, iwc := range issuesWithCounts {
		iwp := IssueWithParent{IssueWithCounts: iwc}
		if parentInfo, ok := parentResp.Parents[iwc.Issue.ID]; ok {
			iwp.Parent = &parentInfo.ParentID
			iwp.ParentTitle = &parentInfo.ParentTitle
		}
		if iwc.Issue.SourceRepo != "" {
			iwp.Repo = &iwc.Issue.SourceRepo
		}
		issues[i] = iwp
	}
	return &ListIssuesResult{Issues: issues}
}

// buildKanbanResult builds the kanban response. The second return is false
// if the Blocked RPC hit a transport error — callers should not return the
// connection to the pool in that case.
func (s *issueServiceImpl) buildKanbanResult(client *rpc.Client, issuesWithCounts []*types.IssueWithCounts, parentResp *rpc.GetParentIDsResponse) (*ListIssuesResult, bool) {
	clean := true
	blockedMap := make(map[string]*types.BlockedIssue)
	blockedResp, blockedErr := client.Blocked(&rpc.BlockedArgs{})
	if blockedErr != nil {
		slog.Error("failed to get blocked issues", "err", blockedErr)
		clean = false
	} else if blockedResp.Success {
		var blockedIssues []*types.BlockedIssue
		if jsonErr := json.Unmarshal(blockedResp.Data, &blockedIssues); jsonErr != nil {
			slog.Error("failed to parse blocked issues", "err", jsonErr)
		} else {
			for _, bi := range blockedIssues {
				blockedMap[bi.Issue.ID] = bi
			}
		}
	}

	// Build unclosed-ID set from already-fetched data, avoiding an extra List RPC.
	unclosedIDs, issueMap := buildUnclosedSetsFromFetched(issuesWithCounts)

	kanbanIssues := make([]KanbanIssue, len(issuesWithCounts))
	for i, iwc := range issuesWithCounts {
		ki := KanbanIssue{IssueWithCounts: iwc}
		if parentInfo, ok := parentResp.Parents[iwc.Issue.ID]; ok {
			ki.Parent = &parentInfo.ParentID
			ki.ParentTitle = &parentInfo.ParentTitle
		}
		if iwc.Issue.SourceRepo != "" {
			ki.Repo = &iwc.Issue.SourceRepo
		}
		// Client-side blocker check (considers only closed blockers as resolved).
		// For filtered views, blocker targets may be outside the result set, so
		// fall back to daemon blocked data.
		refs := getUnclosedBlockerRefs(iwc.Issue.Dependencies, unclosedIDs, issueMap)
		if len(refs) > 0 {
			ki.IsBlocked = true
			ki.BlockedByCount = len(refs)
			ki.BlockedBy = extractBlockerIDs(refs)
			ki.BlockedByDetails = refs
		} else if bi, ok := blockedMap[iwc.Issue.ID]; ok {
			ki.IsBlocked = true
			ki.BlockedByCount = bi.BlockedByCount
			ki.BlockedBy = bi.BlockedBy
			ki.BlockedByDetails = bi.BlockedByDetails
		}
		kanbanIssues[i] = ki
	}
	return &ListIssuesResult{KanbanIssues: kanbanIssues}, clean
}

// listIssuesViaBackend serves a list request through the IssueBackend
// abstraction. Used when there is no daemon connection pool to talk to —
// most importantly, in fleet mode where the bd daemon is intentionally
// absent and every list query has to hit the fleet HTTP API instead.
//
// This is the simplest viable port: it covers the standard (non-kanban)
// list shape and a basic kanban view by deriving blocked status from the
// limited info available on backend.IssueData. Parent-title enrichment
// requires extra Get calls per issue and is skipped — the FE shows the
// parent ID without a title, which the kanban handles as a graceful
// fallback (see kanbanIssue.go in the FE).
//
// The composite ListKanban RPC's per-issue parent-title and
// blocked-by-details lookups are intentionally not reproduced: matching
// them would mean N extra Gets, which fleet-db can absorb but which
// inflates p95 enough to fail the SSE realtime parity spec. When fleet-db
// adds a batch list-with-relations endpoint, swap this implementation for
// it and drop the per-issue degraded mode.
func (s *issueServiceImpl) listIssuesViaBackend(
	ctx context.Context, be backend.IssueBackend, params ListIssuesParams,
) (*ListIssuesResult, error) {
	opts := listArgsToBackendOpts(params.Args)
	issues, err := be.List(ctx, opts)
	if err != nil {
		return nil, translateBackendError(err)
	}

	// Apply ExcludeStatus client-side. fleet-db's ListOpts has a single
	// Status filter, not an exclude set; doing it here keeps the contract
	// uniform with the pool-based path.
	if len(params.ExcludeStatus) > 0 {
		excluded := make(map[string]bool, len(params.ExcludeStatus))
		for _, s := range params.ExcludeStatus {
			excluded[s] = true
		}
		filtered := issues[:0]
		for _, i := range issues {
			if !excluded[i.Status] {
				filtered = append(filtered, i)
			}
		}
		issues = filtered
	}

	if !params.IncludeBlocked {
		out := make([]IssueWithParent, len(issues))
		for i, d := range issues {
			d := d // local copy for &d.Parent below
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
			out[i] = iwp
		}
		return &ListIssuesResult{Issues: out}, nil
	}

	out := make([]KanbanIssue, len(issues))
	for i, d := range issues {
		d := d
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
		out[i] = ki
	}
	return &ListIssuesResult{KanbanIssues: out}, nil
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
		ID:        d.ID,
		Title:     d.Title,
		Status:    types.Status(d.Status),
		Priority:  d.Priority,
		IssueType: types.IssueType(d.IssueType),
		Assignee:  d.Assignee,
		Owner:     d.Owner,
		Labels:    d.Labels,
		Design:    d.Design,
		CreatedAt: d.CreatedAt,
		UpdatedAt: d.UpdatedAt,
		DueAt:     d.DueAt,
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
		Status:    string(a.Status),
		IssueType: string(a.IssueType),
		Assignee:  a.Assignee,
		Labels:    a.Labels,
		ParentID:  a.ParentID,
		Limit:     a.Limit,
	}
}

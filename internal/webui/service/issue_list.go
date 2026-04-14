package service

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/types"
)

func (s *issueServiceImpl) ListIssues(ctx context.Context, params ListIssuesParams) (*ListIssuesResult, error) {
	if s.pool == nil {
		return nil, ErrUnavailable("connection pool not initialized")
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	client, err := s.pool.Get(ctx)
	if err != nil {
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

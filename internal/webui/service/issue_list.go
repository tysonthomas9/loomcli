package service

import (
	"context"
	"errors"
	"log/slog"
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

	kanbanArgs := &rpc.ListKanbanArgs{
		ListArgs:       *params.Args,
		IncludeBlocked: params.IncludeBlocked,
		ExcludeStatus:  params.ExcludeStatus,
	}
	kanbanResp, kanbanErr := client.ListKanban(kanbanArgs)
	if kanbanErr != nil {
		slog.Error("ListKanban RPC error", "err", kanbanErr)
		return nil, ErrInternal("failed to list issues", kanbanErr)
	}
	rpcOK = true
	return buildResultFromKanbanRPC(kanbanResp, params.IncludeBlocked), nil
}

// buildResultFromKanbanRPC converts the composite RPC response into the
// service-layer result shape. When includeBlocked is true, client-side blocker
// refinement runs against the fetched set (blockers are only considered
// resolved when closed).
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

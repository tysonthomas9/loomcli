package rpc

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/util"
)

// handleListKanban is a composite RPC that replaces the 3-call sequence
// (List + GetParentIDs + Blocked) with a single server-side operation.
// This reduces RPC round-trips from 3 to 1 and pool connection hold time
// from 3× to 1×, which is critical under concurrent load.
func (s *Server) handleListKanban(req *Request) Response {
	var args ListKanbanArgs
	if err := json.Unmarshal(req.Args, &args); err != nil {
		return Response{
			Success: false,
			Error:   fmt.Sprintf("invalid list_kanban args: %v", err),
		}
	}

	store := s.storage
	if store == nil {
		return Response{
			Success: false,
			Error:   "storage not available",
		}
	}

	// Build filter from ListArgs (same logic as handleList)
	filter := buildIssueFilter(&args.ListArgs)

	ctx, cancel := s.reqCtx(req)
	defer cancel()

	// 1. Search issues
	issues, err := store.SearchIssues(ctx, args.Query, filter)
	if err != nil {
		return Response{
			Success: false,
			Error:   fmt.Sprintf("failed to list issues: %v", err),
		}
	}

	// Apply ExcludeStatus post-filter (ListArgs.Status only supports one value)
	if len(args.ExcludeStatus) > 0 {
		excludeSet := make(map[types.Status]bool, len(args.ExcludeStatus))
		for _, s := range args.ExcludeStatus {
			excludeSet[types.Status(s)] = true
		}
		filtered := make([]*types.Issue, 0, len(issues))
		for _, issue := range issues {
			if !excludeSet[issue.Status] {
				filtered = append(filtered, issue)
			}
		}
		issues = filtered
	}

	if len(issues) == 0 {
		data, _ := json.Marshal(ListKanbanResponse{Issues: []*KanbanIssueRPC{}})
		return Response{Success: true, Data: data}
	}

	// Collect issue IDs
	issueIDs := make([]string, len(issues))
	for i, issue := range issues {
		issueIDs[i] = issue.ID
	}

	// 2. Batch-fetch dependency counts
	depCounts, _ := store.GetDependencyCounts(ctx, issueIDs)

	// 3. Batch-fetch parent info
	parents, err := store.GetParentIDs(ctx, issueIDs)
	if err != nil {
		// Non-fatal: continue without parent info
		parents = make(map[string]*types.ParentInfo)
	}

	// 4. Optionally batch-fetch blocked info
	var blockedMap map[string]*types.BlockedIssue
	if args.IncludeBlocked {
		blocked, err := store.GetBlockedIssues(ctx, types.WorkFilter{})
		if err == nil {
			blockedMap = make(map[string]*types.BlockedIssue, len(blocked))
			for _, bi := range blocked {
				blockedMap[bi.Issue.ID] = bi
			}
		}
	}

	// 5. Assemble response
	result := make([]*KanbanIssueRPC, len(issues))
	for i, issue := range issues {
		counts := depCounts[issue.ID]
		if counts == nil {
			counts = &types.DependencyCounts{}
		}

		ki := &KanbanIssueRPC{
			IssueWithCounts: &types.IssueWithCounts{
				Issue:           issue,
				DependencyCount: counts.DependencyCount,
				DependentCount:  counts.DependentCount,
			},
		}

		if pi, ok := parents[issue.ID]; ok {
			ki.ParentID = pi.ParentID
			ki.ParentTitle = pi.ParentTitle
		}
		if issue.SourceRepo != "" {
			ki.Repo = issue.SourceRepo
		}

		if blockedMap != nil {
			if bi, ok := blockedMap[issue.ID]; ok {
				ki.IsBlocked = true
				ki.BlockedByCount = bi.BlockedByCount
				ki.BlockedBy = bi.BlockedBy
				ki.BlockedByDetails = bi.BlockedByDetails
			}
		}

		result[i] = ki
	}

	data, _ := json.Marshal(ListKanbanResponse{Issues: result})
	return Response{Success: true, Data: data}
}

// buildIssueFilter converts ListArgs into a types.IssueFilter.
// Extracted from handleList so both handleList and handleListKanban share the same logic.
func buildIssueFilter(listArgs *ListArgs) types.IssueFilter {
	filter := types.IssueFilter{
		Limit: listArgs.Limit,
	}

	if listArgs.Status != "" && listArgs.Status != "all" {
		status := types.Status(listArgs.Status)
		filter.Status = &status
	}
	if listArgs.IssueType != "" {
		issueType := types.IssueType(listArgs.IssueType)
		filter.IssueType = &issueType
	}
	if listArgs.Assignee != "" {
		filter.Assignee = &listArgs.Assignee
	}
	if listArgs.Priority != nil {
		filter.Priority = listArgs.Priority
	}

	labels := util.NormalizeLabels(listArgs.Labels)
	labelsAny := util.NormalizeLabels(listArgs.LabelsAny)
	if len(labels) > 0 {
		filter.Labels = labels
	} else if listArgs.Label != "" {
		filter.Labels = []string{strings.TrimSpace(listArgs.Label)}
	}
	if len(labelsAny) > 0 {
		filter.LabelsAny = labelsAny
	}
	if len(listArgs.IDs) > 0 {
		ids := util.NormalizeLabels(listArgs.IDs)
		if len(ids) > 0 {
			filter.IDs = ids
		}
	}

	filter.TitleContains = listArgs.TitleContains
	filter.DescriptionContains = listArgs.DescriptionContains
	filter.NotesContains = listArgs.NotesContains

	if listArgs.CreatedAfter != "" {
		if t, err := parseTimeRPC(listArgs.CreatedAfter); err == nil {
			filter.CreatedAfter = &t
		}
	}
	if listArgs.CreatedBefore != "" {
		if t, err := parseTimeRPC(listArgs.CreatedBefore); err == nil {
			filter.CreatedBefore = &t
		}
	}
	if listArgs.UpdatedAfter != "" {
		if t, err := parseTimeRPC(listArgs.UpdatedAfter); err == nil {
			filter.UpdatedAfter = &t
		}
	}
	if listArgs.UpdatedBefore != "" {
		if t, err := parseTimeRPC(listArgs.UpdatedBefore); err == nil {
			filter.UpdatedBefore = &t
		}
	}
	if listArgs.ClosedAfter != "" {
		if t, err := parseTimeRPC(listArgs.ClosedAfter); err == nil {
			filter.ClosedAfter = &t
		}
	}
	if listArgs.ClosedBefore != "" {
		if t, err := parseTimeRPC(listArgs.ClosedBefore); err == nil {
			filter.ClosedBefore = &t
		}
	}

	filter.EmptyDescription = listArgs.EmptyDescription
	filter.NoAssignee = listArgs.NoAssignee
	filter.NoLabels = listArgs.NoLabels
	filter.PriorityMin = listArgs.PriorityMin
	filter.PriorityMax = listArgs.PriorityMax
	filter.Pinned = listArgs.Pinned

	if !listArgs.IncludeTemplates {
		isTemplate := false
		filter.IsTemplate = &isTemplate
	}
	if listArgs.ParentID != "" {
		filter.ParentID = &listArgs.ParentID
	}
	filter.Ephemeral = listArgs.Ephemeral
	if listArgs.MolType != "" {
		molType := types.MolType(listArgs.MolType)
		filter.MolType = &molType
	}
	if len(listArgs.SourceRepos) > 0 {
		filter.SourceRepos = listArgs.SourceRepos
	}
	if len(listArgs.ExcludeStatus) > 0 {
		for _, s := range listArgs.ExcludeStatus {
			filter.ExcludeStatus = append(filter.ExcludeStatus, types.Status(s))
		}
	}
	if len(listArgs.ExcludeTypes) > 0 {
		for _, t := range listArgs.ExcludeTypes {
			filter.ExcludeTypes = append(filter.ExcludeTypes, types.IssueType(t))
		}
	}

	filter.Deferred = listArgs.Deferred
	if listArgs.DeferAfter != "" {
		if t, err := parseTimeRPC(listArgs.DeferAfter); err == nil {
			filter.DeferAfter = &t
		}
	}
	if listArgs.DeferBefore != "" {
		if t, err := parseTimeRPC(listArgs.DeferBefore); err == nil {
			filter.DeferBefore = &t
		}
	}
	if listArgs.DueAfter != "" {
		if t, err := parseTimeRPC(listArgs.DueAfter); err == nil {
			filter.DueAfter = &t
		}
	}
	if listArgs.DueBefore != "" {
		if t, err := parseTimeRPC(listArgs.DueBefore); err == nil {
			filter.DueBefore = &t
		}
	}
	filter.Overdue = listArgs.Overdue
	filter.Lightweight = listArgs.Lightweight

	return filter
}

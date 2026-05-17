package fleet

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

func (b *FleetBackend) blockedIncludingExplicitStatus(ctx context.Context, opts backend.BlockedOpts) ([]backend.IssueData, error) {
	dependencyBlocked, err := b.dependencyBlocked(ctx, opts)
	if err != nil {
		return nil, err
	}
	if opts.ParentID == "" && len(dependencyBlocked) > 0 {
		return dependencyBlocked, nil
	}
	explicitBlocked, err := b.explicitBlocked(ctx, opts)
	if err != nil {
		return nil, err
	}
	return mergeBlockedIssueData(dependencyBlocked, explicitBlocked, opts.Limit), nil
}

func (b *FleetBackend) dependencyBlocked(ctx context.Context, opts backend.BlockedOpts) ([]backend.IssueData, error) {
	path := "/issues/blocked?" + blockedOptsToQuery(opts)
	resp, err := b.exec(ctx, "Blocked", "GET", path, nil)
	if err != nil {
		return nil, err
	}
	if !hasData(resp) {
		return []backend.IssueData{}, nil
	}
	return unmarshalBlockedIssueList(resp.Data, "Blocked")
}

func (b *FleetBackend) explicitBlocked(ctx context.Context, opts backend.BlockedOpts) ([]backend.IssueData, error) {
	listOpts := backend.ListOpts{
		Status:    "blocked",
		ParentID:  opts.ParentID,
		Assignee:  opts.Assignee,
		IssueType: opts.Type,
		Limit:     opts.Limit,
	}
	if opts.Priority != nil {
		listOpts.Limit = 0
	}
	issues, err := b.List(ctx, listOpts)
	if err != nil {
		return nil, err
	}
	filtered := make([]backend.IssueData, 0, len(issues))
	for _, issue := range issues {
		if !blockedIssueMatchesOpts(issue, opts) {
			continue
		}
		if issue.Status == "" {
			issue.Status = "blocked"
		}
		filtered = append(filtered, issue)
	}
	return filtered, nil
}

func blockedIssueMatchesOpts(issue backend.IssueData, opts backend.BlockedOpts) bool {
	if opts.ParentID != "" && issue.Parent != opts.ParentID {
		return false
	}
	if opts.Assignee != "" && issue.Assignee != opts.Assignee {
		return false
	}
	if opts.Type != "" && issue.IssueType != opts.Type {
		return false
	}
	if opts.Priority != nil && issue.Priority != *opts.Priority {
		return false
	}
	return true
}

func mergeBlockedIssueData(dependencyBlocked, explicitBlocked []backend.IssueData, limit int) []backend.IssueData {
	merged := make([]backend.IssueData, 0, len(dependencyBlocked)+len(explicitBlocked))
	seen := make(map[string]struct{}, len(dependencyBlocked)+len(explicitBlocked))
	for _, issue := range dependencyBlocked {
		if issue.ID == "" {
			continue
		}
		seen[issue.ID] = struct{}{}
		merged = append(merged, issue)
	}
	for _, issue := range explicitBlocked {
		if issue.ID == "" {
			continue
		}
		if _, ok := seen[issue.ID]; ok {
			continue
		}
		seen[issue.ID] = struct{}{}
		merged = append(merged, issue)
	}
	if limit > 0 && len(merged) > limit {
		return merged[:limit]
	}
	return merged
}

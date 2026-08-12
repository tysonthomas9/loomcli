package fleet

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
)

func (b *FleetBackend) Deferred(ctx context.Context, opts backend.DeferredOpts) ([]backend.IssueData, error) {
	resp, err := b.exec(ctx, "Deferred", "GET", "/issues/deferred", nil)
	if err != nil {
		return nil, err
	}
	if !hasData(resp) {
		return []backend.IssueData{}, nil
	}
	issues, err := unmarshalListOrWrapper[*readyIssueWithParent](resp.Data, "Deferred")
	if err != nil {
		return nil, err
	}
	return filterDeferredIssues(readyIssuesToData(issues), opts), nil
}

func filterDeferredIssues(issues []backend.IssueData, opts backend.DeferredOpts) []backend.IssueData {
	return filterIssueData(issues, issueDataFilter{
		Assignee:    opts.Assignee,
		Priority:    opts.Priority,
		Type:        opts.Type,
		ParentID:    opts.ParentID,
		Labels:      opts.Labels,
		SourceRepos: opts.SourceRepos,
		Limit:       opts.Limit,
	})
}

func filterReadyIssues(issues []backend.IssueData, opts backend.ReadyOpts) []backend.IssueData {
	return filterIssueData(issues, issueDataFilter{
		Assignee:    opts.Assignee,
		Priority:    opts.Priority,
		Type:        opts.Type,
		ParentID:    opts.ParentID,
		Labels:      opts.Labels,
		LabelsAny:   opts.LabelsAny,
		SourceRepos: opts.SourceRepos,
		Limit:       opts.Limit,
	})
}

func filterBlockedSummaries(issues []workitems.IssueSummary, opts workitems.AvailabilityQuery) []workitems.IssueSummary {
	filter := issueDataFilter{
		Assignee:    opts.Assignee,
		Unassigned:  opts.Unassigned,
		Priority:    opts.Priority,
		Type:        opts.IssueType,
		ParentID:    opts.ParentID,
		Labels:      opts.Labels,
		LabelsAny:   opts.LabelsAny,
		SourceRepos: opts.SourceRepos,
		Limit:       opts.Limit,
	}
	if !filter.needsFilter() {
		return issues
	}
	out := make([]workitems.IssueSummary, 0, len(issues))
	for _, issue := range issues {
		if !filter.matches(issue.Assignee, issue.Priority, issue.IssueType, issue.Parent, issue.Labels, issue.SourceRepo) {
			continue
		}
		out = append(out, issue)
		if filter.Limit > 0 && len(out) >= filter.Limit {
			break
		}
	}
	return out
}

func filterListIssues(issues []backend.IssueData, opts backend.ListOpts) []backend.IssueData {
	return filterIssueData(issues, issueDataFilter{
		Assignee:    opts.Assignee,
		Type:        opts.IssueType,
		ParentID:    opts.ParentID,
		Labels:      opts.Labels,
		SourceRepos: opts.SourceRepos,
		Limit:       opts.Limit,
	})
}

type issueDataFilter struct {
	Assignee    string
	Unassigned  bool
	Priority    *int
	Type        string
	ParentID    string
	Labels      []string
	LabelsAny   []string
	SourceRepos []string
	Limit       int
}

func filterIssueData(issues []backend.IssueData, opts issueDataFilter) []backend.IssueData {
	if !opts.needsFilter() {
		return issues
	}
	out := make([]backend.IssueData, 0, len(issues))
	for _, issue := range issues {
		if !issueDataMatches(issue, opts) {
			continue
		}
		out = append(out, issue)
		if opts.Limit > 0 && len(out) >= opts.Limit {
			break
		}
	}
	return out
}

func (opts issueDataFilter) needsFilter() bool {
	return opts.Assignee != "" || opts.Unassigned || opts.Priority != nil || opts.Type != "" || opts.ParentID != "" ||
		len(opts.Labels) > 0 || len(opts.LabelsAny) > 0 || len(opts.SourceRepos) > 0 || opts.Limit > 0
}

func issueDataMatches(issue backend.IssueData, opts issueDataFilter) bool {
	return opts.matches(issue.Assignee, issue.Priority, issue.IssueType, issue.Parent, issue.Labels, issue.SourceRepo)
}

func (opts issueDataFilter) matches(assignee string, priority int, issueType, parent string, labels []string, sourceRepo string) bool {
	if opts.Assignee != "" && assignee != opts.Assignee {
		return false
	}
	if opts.Unassigned && assignee != "" {
		return false
	}
	if opts.Priority != nil && priority != *opts.Priority {
		return false
	}
	if opts.Type != "" && issueType != opts.Type {
		return false
	}
	if opts.ParentID != "" && parent != opts.ParentID {
		return false
	}
	if len(opts.Labels) > 0 && !hasAllStrings(labels, opts.Labels) {
		return false
	}
	if len(opts.LabelsAny) > 0 && !hasAnyOfStrings(labels, opts.LabelsAny) {
		return false
	}
	if len(opts.SourceRepos) > 0 && !hasAnyString(opts.SourceRepos, sourceRepo) {
		return false
	}
	return true
}

func hasAllStrings(values, required []string) bool {
	for _, needle := range required {
		if !hasAnyString(values, needle) {
			return false
		}
	}
	return true
}

func hasAnyOfStrings(values, candidates []string) bool {
	for _, value := range values {
		if hasAnyString(candidates, value) {
			return true
		}
	}
	return false
}

func hasAnyString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

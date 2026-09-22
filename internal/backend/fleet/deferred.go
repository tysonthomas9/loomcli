package fleet

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/backend"
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

func filterBlockedIssues(issues []backend.IssueData, opts backend.BlockedOpts) []backend.IssueData {
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
	return opts.Assignee != "" || opts.Priority != nil || opts.Type != "" || opts.ParentID != "" ||
		len(opts.Labels) > 0 || len(opts.LabelsAny) > 0 || len(opts.SourceRepos) > 0 || opts.Limit > 0
}

func issueDataMatches(issue backend.IssueData, opts issueDataFilter) bool {
	if opts.Assignee != "" && issue.Assignee != opts.Assignee {
		return false
	}
	if opts.Priority != nil && issue.Priority != *opts.Priority {
		return false
	}
	if opts.Type != "" && issue.IssueType != opts.Type {
		return false
	}
	if opts.ParentID != "" && issue.Parent != opts.ParentID {
		return false
	}
	if len(opts.Labels) > 0 && !hasAllStrings(issue.Labels, opts.Labels) {
		return false
	}
	if len(opts.LabelsAny) > 0 && !hasAnyOfStrings(issue.Labels, opts.LabelsAny) {
		return false
	}
	// An issue with no source repo is unscoped work, not a repo mismatch: it
	// stays eligible for any repo-scoped agent. Excluding it starved every
	// multi-repo agent, because `loom data create` drops --source-repo
	// so most issues carry no source repo at all.
	if len(opts.SourceRepos) > 0 && issue.SourceRepo != "" && !hasAnyString(opts.SourceRepos, issue.SourceRepo) {
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

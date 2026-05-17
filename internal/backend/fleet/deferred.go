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
	if opts.Assignee == "" && opts.Priority == nil && opts.Type == "" && opts.ParentID == "" &&
		len(opts.Labels) == 0 && len(opts.SourceRepos) == 0 && opts.Limit <= 0 {
		return issues
	}
	out := make([]backend.IssueData, 0, len(issues))
	for _, issue := range issues {
		if !deferredIssueMatches(issue, opts) {
			continue
		}
		out = append(out, issue)
		if opts.Limit > 0 && len(out) >= opts.Limit {
			break
		}
	}
	return out
}

func deferredIssueMatches(issue backend.IssueData, opts backend.DeferredOpts) bool {
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
	if len(opts.SourceRepos) > 0 && !hasAnyString(opts.SourceRepos, issue.SourceRepo) {
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

func hasAnyString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

package fleet

import (
	"context"
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
)

func (b *FleetBackend) Deferred(ctx context.Context, query workitems.AvailabilityQuery) ([]workitems.IssueSummary, error) {
	if err := checkProjectedAvailabilityQuerySupported("deferred", query); err != nil {
		return nil, err
	}
	resp, err := b.exec(ctx, "Deferred", "GET", "/issues/deferred", nil)
	if err != nil {
		return nil, err
	}
	if !hasData(resp) {
		return []workitems.IssueSummary{}, nil
	}
	issues, err := unmarshalListOrWrapper[*readyIssueWithParent](resp.Data, "Deferred")
	if err != nil {
		return nil, err
	}
	return filterAvailabilitySummaries(availabilityIssuesToSummaries(issues), query), nil
}

func filterAvailabilitySummaries(issues []workitems.IssueSummary, opts workitems.AvailabilityQuery) []workitems.IssueSummary {
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

func checkProjectedAvailabilityQuerySupported(view string, query workitems.AvailabilityQuery) error {
	var unsupported []string
	if query.SortPolicy != "" {
		unsupported = append(unsupported, "SortPolicy")
	}
	if query.MolType != "" {
		unsupported = append(unsupported, "MolType")
	}
	if len(unsupported) == 0 {
		return nil
	}
	return fmt.Errorf("fleet-db: unsupported %s filters [%s]: %w",
		view, strings.Join(unsupported, ", "), backend.ErrFilterNotSupported)
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

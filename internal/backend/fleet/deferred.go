package fleet

import (
	"context"
	"time"

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

// filterListIssues applies every List filter that fleet-db cannot evaluate
// itself. It runs over the complete paged result set, so the caller's Limit is
// applied last — after narrowing, never to the pre-filter rows.
func filterListIssues(rows []listRow, opts backend.ListOpts, dates listDateFilters) []listRow {
	return filterIssueRows(rows, issueDataFilter{
		Assignee:      opts.Assignee,
		Type:          opts.IssueType,
		ParentID:      opts.ParentID,
		Labels:        opts.Labels,
		SourceRepos:   opts.SourceRepos,
		Limit:         opts.Limit,
		TitleContains: opts.TitleContains,
		NotesContains: opts.NotesContains,
		NoAssignee:    opts.NoAssignee,
		NoLabels:      opts.NoLabels,
		Pinned:        opts.Pinned,
		CreatedAfter:  dates.createdAfter,
		CreatedBefore: dates.createdBefore,
		Text: listTextFilter{
			Query:               opts.Query,
			DescriptionContains: opts.DescriptionContains,
			EmptyDescription:    opts.EmptyDescription,
		},
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

	// List-only fields. Ready/Blocked/Deferred leave these zero — the shared
	// struct is what lets those call sites stay unchanged.
	TitleContains string
	NotesContains string
	NoAssignee    bool
	NoLabels      bool
	Pinned        *bool
	CreatedAfter  time.Time
	CreatedBefore time.Time
	Text          listTextFilter
}

// filterIssueData is the description-free entry point used by
// Ready/Blocked/Deferred, which have no wire Description to filter on.
func filterIssueData(issues []backend.IssueData, opts issueDataFilter) []backend.IssueData {
	if !opts.needsFilter() {
		return issues
	}
	return listRowsToData(filterIssueRows(dataToListRows(issues), opts))
}

func filterIssueRows(rows []listRow, opts issueDataFilter) []listRow {
	if !opts.needsFilter() {
		return rows
	}
	out := make([]listRow, 0, len(rows))
	for _, row := range rows {
		if !rowMatches(row, opts) {
			continue
		}
		out = append(out, row)
		if opts.Limit > 0 && len(out) >= opts.Limit {
			break
		}
	}
	return out
}

func rowMatches(row listRow, opts issueDataFilter) bool {
	return issueDataMatches(row.data, opts) && opts.Text.matches(row)
}

func (opts issueDataFilter) needsFilter() bool {
	return opts.Assignee != "" || opts.Priority != nil || opts.Type != "" || opts.ParentID != "" ||
		len(opts.Labels) > 0 || len(opts.LabelsAny) > 0 || len(opts.SourceRepos) > 0 || opts.Limit > 0 ||
		opts.TitleContains != "" || opts.NotesContains != "" || opts.NoAssignee || opts.NoLabels ||
		opts.Pinned != nil || !opts.CreatedAfter.IsZero() || !opts.CreatedBefore.IsZero() ||
		opts.Text.needsFilter()
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
	if len(opts.SourceRepos) > 0 && !hasAnyString(opts.SourceRepos, issue.SourceRepo) {
		return false
	}
	return issueDataMatchesList(issue, opts)
}

// issueDataMatchesList evaluates the List-only predicates that read nothing but
// backend.IssueData. Split out of issueDataMatches to keep both within funlen.
func issueDataMatchesList(issue backend.IssueData, opts issueDataFilter) bool {
	if opts.TitleContains != "" && !containsFold(issue.Title, opts.TitleContains) {
		return false
	}
	if opts.NotesContains != "" && !containsFold(issue.Notes, opts.NotesContains) {
		return false
	}
	if opts.NoAssignee && issue.Assignee != "" {
		return false
	}
	if opts.NoLabels && len(issue.Labels) > 0 {
		return false
	}
	// fleet-db has no boolean pinned column: pinned is a status. entity.Issue
	// does carry a Pinned bool, which is why this is worth saying out loud.
	if opts.Pinned != nil && (issue.Status == pinnedStatus) != *opts.Pinned {
		return false
	}
	return createdRangeMatches(issue, opts)
}

// createdRangeMatches applies the created_after/created_before bounds, which are
// exclusive, mirroring fleet-db's own updated_after/updated_before.
//
// A row whose created_at was absent from the wire has the zero time and would
// otherwise sail through a created_before bound; treat it as non-matching
// whenever a created range is active rather than inventing a timestamp for it.
func createdRangeMatches(issue backend.IssueData, opts issueDataFilter) bool {
	if opts.CreatedAfter.IsZero() && opts.CreatedBefore.IsZero() {
		return true
	}
	if issue.CreatedAt.IsZero() {
		return false
	}
	if !opts.CreatedAfter.IsZero() && !issue.CreatedAt.After(opts.CreatedAfter) {
		return false
	}
	if !opts.CreatedBefore.IsZero() && !issue.CreatedAt.Before(opts.CreatedBefore) {
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

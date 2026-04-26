package beads

import (
	"encoding/json"
	"strconv"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/types"
)

// listOptsToArgs converts backend.ListOpts to rpc.ListArgs.
func listOptsToArgs(opts backend.ListOpts) *rpc.ListArgs {
	return &rpc.ListArgs{
		Query:               opts.Query,
		Status:              opts.Status,
		Priority:            opts.Priority,
		IssueType:           opts.IssueType,
		Assignee:            opts.Assignee,
		Labels:              opts.Labels,
		LabelsAny:           opts.LabelsAny,
		IDs:                 opts.IDs,
		Limit:               opts.Limit,
		TitleContains:       opts.TitleContains,
		DescriptionContains: opts.DescriptionContains,
		NotesContains:       opts.NotesContains,
		CreatedAfter:        opts.CreatedAfter,
		CreatedBefore:       opts.CreatedBefore,
		UpdatedAfter:        opts.UpdatedAfter,
		UpdatedBefore:       opts.UpdatedBefore,
		ClosedAfter:         opts.ClosedAfter,
		ClosedBefore:        opts.ClosedBefore,
		EmptyDescription:    opts.EmptyDescription,
		NoAssignee:          opts.NoAssignee,
		NoLabels:            opts.NoLabels,
		PriorityMin:         opts.PriorityMin,
		PriorityMax:         opts.PriorityMax,
		Pinned:              opts.Pinned,
		Ephemeral:           opts.Ephemeral,
		IncludeTemplates:    opts.IncludeTemplates,
		ParentID:            opts.ParentID,
		MolType:             opts.MolType,
		ExcludeStatus:       opts.ExcludeStatus,
		ExcludeTypes:        opts.ExcludeTypes,
		Deferred:            opts.Deferred,
		DeferAfter:          opts.DeferAfter,
		DeferBefore:         opts.DeferBefore,
		DueAfter:            opts.DueAfter,
		DueBefore:           opts.DueBefore,
		Overdue:             opts.Overdue,
		AllowStale:          opts.AllowStale,
		SourceRepos:         opts.SourceRepos,
	}
}

// issueToData converts types.Issue to backend.IssueData (slim projection).
func issueToData(issue *types.Issue) backend.IssueData {
	labels := make([]string, 0)
	if len(issue.Labels) > 0 {
		labels = issue.Labels
	}
	return backend.IssueData{
		ID:          issue.ID,
		Title:       issue.Title,
		Status:      string(issue.Status),
		Priority:    issue.Priority,
		IssueType:   string(issue.IssueType),
		Assignee:    issue.Assignee,
		Owner:       issue.Owner,
		Labels:      labels,
		SourceRepo:  issue.SourceRepo,
		Design:      issue.Design,
		CreatedAt:   issue.CreatedAt,
		UpdatedAt:   issue.UpdatedAt,
		DueAt:       issue.DueAt,
		DeferUntil:  issue.DeferUntil,
		CreatedBy:   issue.CreatedBy,
		ClosedAt:    issue.ClosedAt,
		CloseReason: issue.CloseReason,
	}
}

// issueWithCountsToData converts types.IssueWithCounts to backend.IssueData,
// populating DependencyCount and DependentCount from the counts wrapper.
func issueWithCountsToData(iwc *types.IssueWithCounts) backend.IssueData {
	if iwc.Issue == nil {
		return backend.IssueData{
			DependencyCount: iwc.DependencyCount,
			DependentCount:  iwc.DependentCount,
		}
	}
	d := issueToData(iwc.Issue)
	d.DependencyCount = iwc.DependencyCount
	d.DependentCount = iwc.DependentCount
	return d
}

// issuesToData converts a slice of *types.Issue to []backend.IssueData.
func issuesToData(issues []*types.Issue) []backend.IssueData {
	result := make([]backend.IssueData, 0, len(issues))
	for _, issue := range issues {
		if issue != nil {
			result = append(result, issueToData(issue))
		}
	}
	return result
}

// issuesWithCountsToData converts a slice of *types.IssueWithCounts to []backend.IssueData.
func issuesWithCountsToData(issues []*types.IssueWithCounts) []backend.IssueData {
	result := make([]backend.IssueData, 0, len(issues))
	for _, iwc := range issues {
		if iwc != nil {
			result = append(result, issueWithCountsToData(iwc))
		}
	}
	return result
}

// detailsToDetailData converts types.IssueDetails to backend.IssueDetailData (full projection).
func detailsToDetailData(details *types.IssueDetails) backend.IssueDetailData {
	d := backend.IssueDetailData{
		IssueData: issueToData(&details.Issue),
	}

	// Content fields.
	d.Description = details.Description
	d.Design = details.Design
	d.AcceptanceCriteria = details.AcceptanceCriteria
	d.Notes = details.Notes

	// Lifecycle. CreatedBy/ClosedAt/CloseReason are populated by
	// issueToData via the embedded IssueData; no duplicate assignment.
	d.ClosedBySession = details.ClosedBySession

	// External integration.
	if details.ExternalRef != nil {
		d.ExternalRef = *details.ExternalRef
	}
	d.EstimatedMinutes = details.EstimatedMinutes

	// Parent.
	if details.Parent != nil {
		d.IssueData.Parent = *details.Parent
	}

	// Labels (override from IssueDetails which has its own Labels field).
	labels := make([]string, 0)
	if len(details.Labels) > 0 {
		labels = details.Labels
	}
	d.IssueData.Labels = labels

	// Relational data.
	detailsPopulateRelations(&d, details)

	return d
}

// detailsPopulateRelations fills in Dependencies, Dependents, and Comments on d.
func detailsPopulateRelations(d *backend.IssueDetailData, details *types.IssueDetails) {
	deps := make([]backend.DependencyData, 0, len(details.Dependencies))
	for _, iwdm := range details.Dependencies {
		if iwdm != nil {
			deps = append(deps, dependencyMetaToData(details.Issue.ID, iwdm))
		}
	}
	d.Dependencies = deps

	// Dependents: these are issues that depend ON this issue.
	// IssueID = the dependent (iwdm.Issue.ID), DependsOnID = this issue (details.Issue.ID).
	dependents := make([]backend.DependencyData, 0, len(details.Dependents))
	for _, iwdm := range details.Dependents {
		if iwdm != nil {
			dependents = append(dependents, backend.DependencyData{
				IssueID:     iwdm.Issue.ID,
				DependsOnID: details.Issue.ID,
				Type:        string(iwdm.DependencyType),
				Title:       iwdm.Issue.Title,
				Status:      string(iwdm.Issue.Status),
				Priority:    iwdm.Issue.Priority,
				IssueType:   string(iwdm.Issue.IssueType),
				CreatedAt:   iwdm.Issue.CreatedAt,
				CreatedBy:   iwdm.Issue.CreatedBy,
			})
		}
	}
	d.Dependents = dependents

	comments := make([]backend.CommentData, 0, len(details.Comments))
	for _, c := range details.Comments {
		if c != nil {
			comments = append(comments, commentToData(c))
		}
	}
	d.Comments = comments
}

// dependencyMetaToData converts types.IssueWithDependencyMetadata to backend.DependencyData.
// parentID is the issue that owns the dependency list.
func dependencyMetaToData(parentID string, iwdm *types.IssueWithDependencyMetadata) backend.DependencyData {
	return backend.DependencyData{
		IssueID:     parentID,
		DependsOnID: iwdm.Issue.ID,
		Type:        string(iwdm.DependencyType),
		Title:       iwdm.Issue.Title,
		Status:      string(iwdm.Issue.Status),
		Priority:    iwdm.Issue.Priority,
		IssueType:   string(iwdm.Issue.IssueType),
		CreatedAt:   iwdm.Issue.CreatedAt,
		CreatedBy:   iwdm.Issue.CreatedBy,
	}
}

// commentToData converts types.Comment to backend.CommentData.
func commentToData(c *types.Comment) backend.CommentData {
	return backend.CommentData{
		ID:        c.ID,
		IssueID:   c.IssueID,
		Author:    c.Author,
		Text:      c.Text,
		CreatedAt: c.CreatedAt,
	}
}

// eventToData converts types.Event to backend.EventData.
// Note: types.Event has EventType, Actor, OldValue, NewValue — not Target or Payload.
// Target and Payload in backend.EventData are forward-looking fields the current
// daemon doesn't populate. They are left empty.
func eventToData(e *types.Event) backend.EventData {
	return backend.EventData{
		ID:        strconv.FormatInt(e.ID, 10),
		IssueID:   e.IssueID,
		Kind:      string(e.EventType),
		Actor:     e.Actor,
		CreatedAt: e.CreatedAt,
	}
}

// statisticsToStatsData converts types.Statistics to backend.StatsData.
func statisticsToStatsData(stats *types.Statistics) backend.StatsData {
	return backend.StatsData{
		TotalIssues:             stats.TotalIssues,
		OpenIssues:              stats.OpenIssues,
		InProgressIssues:        stats.InProgressIssues,
		ClosedIssues:            stats.ClosedIssues,
		BlockedIssues:           stats.BlockedIssues,
		DeferredIssues:          stats.DeferredIssues,
		ReadyIssues:             stats.ReadyIssues,
		TombstoneIssues:         stats.TombstoneIssues,
		PinnedIssues:            stats.PinnedIssues,
		EpicsEligibleForClosure: stats.EpicsEligibleForClosure,
		AverageLeadTime:         stats.AverageLeadTime,
	}
}

// closeResultToData converts rpc.CloseResult to backend.CloseResult.
func closeResultToData(cr *rpc.CloseResult) *backend.CloseResult {
	result := &backend.CloseResult{
		Unblocked: make([]backend.IssueData, 0),
	}
	if cr.Closed != nil {
		closed := issueToData(cr.Closed)
		result.Closed = &closed
	}
	for _, u := range cr.Unblocked {
		if u != nil {
			result.Unblocked = append(result.Unblocked, issueToData(u))
		}
	}
	return result
}

// parseCloseResponse decodes the bd daemon's OpClose response payload, which
// comes in two shapes: either a rpc.CloseResult wrapper (when SuggestNext is
// true) or a bare types.Issue (when SuggestNext is false; see
// third_party/beads/internal/rpc/server_issues_epics.go handleClose). We
// probe for the "closed" key to pick the right decoder — a bare issue has no
// such key at the top level.
func parseCloseResponse(data []byte) (*rpc.CloseResult, error) {
	if len(data) == 0 {
		return &rpc.CloseResult{}, nil
	}
	// Cheap key-probe: unmarshal into a RawMessage map. If "closed" is
	// present, the wrapper decoder applies; otherwise the payload is a bare
	// issue.
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil {
		// Not a JSON object — fall through to wrapper decode which will
		// return its own error.
		var cr rpc.CloseResult
		if uerr := json.Unmarshal(data, &cr); uerr != nil {
			return nil, uerr
		}
		return &cr, nil
	}
	if _, hasClosed := probe["closed"]; hasClosed {
		var cr rpc.CloseResult
		if err := json.Unmarshal(data, &cr); err != nil {
			return nil, err
		}
		return &cr, nil
	}
	// Bare issue shape — promote into CloseResult.Closed with no unblocked.
	var issue types.Issue
	if err := json.Unmarshal(data, &issue); err != nil {
		return nil, err
	}
	return &rpc.CloseResult{Closed: &issue}, nil
}

// mutationToData converts rpc.MutationEvent to backend.MutationData.
func mutationToData(m *rpc.MutationEvent) backend.MutationData {
	return backend.MutationData{
		Type:       m.Type,
		IssueID:    m.IssueID,
		Title:      m.Title,
		Assignee:   m.Assignee,
		Actor:      m.Actor,
		Timestamp:  m.Timestamp,
		OldStatus:  m.OldStatus,
		NewStatus:  m.NewStatus,
		ParentID:   m.ParentID,
		SourceRepo: m.SourceRepo,
		StepCount:  m.StepCount,
	}
}

// mutationsToData converts a slice of rpc.MutationEvent to []backend.MutationData.
func mutationsToData(mutations []rpc.MutationEvent) []backend.MutationData {
	result := make([]backend.MutationData, 0, len(mutations))
	for i := range mutations {
		result = append(result, mutationToData(&mutations[i]))
	}
	return result
}

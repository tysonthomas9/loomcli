package fleet

import (
	"strconv"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/types"
)

// readyIssueWithParent mirrors webui.ReadyIssueWithParent for JSON parsing.
type readyIssueWithParent struct {
	*types.Issue
	Parent      *string `json:"parent,omitempty"`
	ParentTitle *string `json:"parent_title,omitempty"`
	Repo        *string `json:"repo,omitempty"`
}

// countIssuesResponse is the JSON structure returned by the fleet server's
// /issues/count?group_by=status endpoint.
type countIssuesResponse struct {
	Total  int64            `json:"total"`
	Groups map[string]int64 `json:"groups"`
}

// closeResultJSON is the JSON structure returned by the close endpoint.
type closeResultJSON struct {
	Closed    *types.Issue   `json:"closed,omitempty"`
	Unblocked []*types.Issue `json:"unblocked,omitempty"`
}

// issueToData converts types.Issue to backend.IssueData (slim projection).
func issueToData(issue *types.Issue) backend.IssueData {
	labels := make([]string, 0)
	if len(issue.Labels) > 0 {
		labels = issue.Labels
	}
	return backend.IssueData{
		ID:         issue.ID,
		Title:      issue.Title,
		Status:     string(issue.Status),
		Priority:   issue.Priority,
		IssueType:  string(issue.IssueType),
		Assignee:   issue.Assignee,
		Owner:      issue.Owner,
		Labels:     labels,
		SourceRepo: issue.SourceRepo,
		Design:     issue.Design,
		CreatedAt:  issue.CreatedAt,
		UpdatedAt:  issue.UpdatedAt,
		DueAt:      issue.DueAt,
		DeferUntil: issue.DeferUntil,
	}
}

// issueWithCountsToData converts types.IssueWithCounts to backend.IssueData.
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

// issuesWithCountsToData converts a slice of *types.IssueWithCounts.
func issuesWithCountsToData(issues []*types.IssueWithCounts) []backend.IssueData {
	result := make([]backend.IssueData, 0, len(issues))
	for _, iwc := range issues {
		if iwc != nil {
			result = append(result, issueWithCountsToData(iwc))
		}
	}
	return result
}

// detailsToDetailData converts types.IssueDetails to backend.IssueDetailData.
func detailsToDetailData(details *types.IssueDetails) backend.IssueDetailData {
	d := backend.IssueDetailData{
		IssueData: issueToData(&details.Issue),
	}

	// Content fields.
	d.Description = details.Description
	d.Design = details.Design
	d.AcceptanceCriteria = details.AcceptanceCriteria
	d.Notes = details.Notes

	// Lifecycle.
	d.CreatedBy = details.CreatedBy
	d.ClosedAt = details.ClosedAt
	d.CloseReason = details.CloseReason
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

// detailsPopulateRelations fills in Dependencies, Dependents, and Comments.
func detailsPopulateRelations(d *backend.IssueDetailData, details *types.IssueDetails) {
	deps := make([]backend.DependencyData, 0, len(details.Dependencies))
	for _, iwdm := range details.Dependencies {
		if iwdm != nil {
			deps = append(deps, dependencyMetaToData(details.Issue.ID, iwdm))
		}
	}
	d.Dependencies = deps

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
func eventToData(e *types.Event) backend.EventData {
	return backend.EventData{
		ID:        strconv.FormatInt(e.ID, 10),
		IssueID:   e.IssueID,
		Kind:      string(e.EventType),
		Actor:     e.Actor,
		CreatedAt: e.CreatedAt,
	}
}

// readyIssuesToData converts the ready endpoint's response to []backend.IssueData.
func readyIssuesToData(issues []*readyIssueWithParent) []backend.IssueData {
	result := make([]backend.IssueData, 0, len(issues))
	for _, riwp := range issues {
		if riwp == nil || riwp.Issue == nil {
			continue
		}
		d := issueToData(riwp.Issue)
		if riwp.Parent != nil {
			d.Parent = *riwp.Parent
		}
		result = append(result, d)
	}
	return result
}

// blockedIssuesToData converts blocked issues to []backend.IssueData.
func blockedIssuesToData(issues []*types.BlockedIssue) []backend.IssueData {
	result := make([]backend.IssueData, 0, len(issues))
	for _, bi := range issues {
		if bi != nil {
			result = append(result, issueToData(&bi.Issue))
		}
	}
	return result
}

// closeResultJSONToData converts the close endpoint's JSON response to backend.CloseResult.
func closeResultJSONToData(cr *closeResultJSON) *backend.CloseResult {
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

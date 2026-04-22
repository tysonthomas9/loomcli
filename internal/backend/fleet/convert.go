package fleet

import (
	"encoding/json"
	"strconv"
	"time"

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

// fleetMutationEvent mirrors fleet-db's Event shape (see openapi.yaml
// components.schemas.Event). It is distinct from types.Event, which models an
// audit-trail row keyed by a numeric issue-scoped ID; fleet's Event is a
// Redis Stream entry keyed by a timestamped string ID with action/entity
// dimensions and before/after JSON snapshots.
type fleetMutationEvent struct {
	ID          string            `json:"id"`
	Timestamp   time.Time         `json:"timestamp"`
	Actor       string            `json:"actor"`
	Action      string            `json:"action"`
	EntityType  string            `json:"entity_type"`
	EntityID    string            `json:"entity_id"`
	WorkspaceID string            `json:"workspace_id"`
	Before      string            `json:"before,omitempty"`
	After       string            `json:"after,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// fleetMutationsResponse mirrors fleet-db's MutationsResponse envelope.
type fleetMutationsResponse struct {
	Events  []fleetMutationEvent `json:"events"`
	Cursor  string               `json:"cursor"`
	HasMore bool                 `json:"has_more"`
}

// actionToMutationType maps fleet-db Action values (issue.create, ...) to the
// backend.Mutation* string constants. Fleet emits fine-grained actions; loom's
// mutation-type space is coarser, so several actions fold into MutationUpdate.
func actionToMutationType(action, entityType string) string {
	switch action {
	case "issue.create":
		return backend.MutationCreate
	case "issue.delete":
		return backend.MutationDelete
	case "issue.close", "issue.reopen", "issue.update", "issue.claim",
		"issue.release", "issue.assign", "issue.defer", "issue.undefer":
		// All status / metadata transitions on an issue collapse into "update"
		// from the subscriber's point of view — callers that care about the
		// specific transition read OldStatus/NewStatus.
		if action == "issue.close" || action == "issue.reopen" {
			return backend.MutationStatus
		}
		return backend.MutationUpdate
	case "comment.add":
		return backend.MutationComment
	case "dep.add", "dep.remove", "label.add", "label.remove":
		return backend.MutationUpdate
	}
	// Workspace-level actions and any future additions fall back to
	// MutationRefresh so SSE consumers invalidate their caches.
	if entityType == "workspace" {
		return backend.MutationRefresh
	}
	return backend.MutationUpdate
}

// fleetEventToMutationData converts a single fleet mutation event into
// backend.MutationData. Title/status/parent fields come from the event's
// after-snapshot JSON when present; fields absent from fleet's event model
// (StepCount, Assignee for non-assign actions) remain zero.
func fleetEventToMutationData(e *fleetMutationEvent) backend.MutationData {
	md := backend.MutationData{
		Type:      actionToMutationType(e.Action, e.EntityType),
		IssueID:   e.EntityID,
		Actor:     e.Actor,
		Timestamp: e.Timestamp,
	}
	// Best-effort extraction from before/after snapshots. Errors are ignored —
	// the minimum viable mutation already has Type/IssueID/Timestamp.
	if e.After != "" {
		var after struct {
			Title    string `json:"title"`
			Status   string `json:"status"`
			Assignee string `json:"assignee"`
			ParentID string `json:"parent_id"`
			Parent   string `json:"parent"`
			Repo     string `json:"repo"`
		}
		if err := json.Unmarshal([]byte(e.After), &after); err == nil {
			md.Title = after.Title
			md.Assignee = after.Assignee
			md.NewStatus = after.Status
			if after.ParentID != "" {
				md.ParentID = after.ParentID
			} else {
				md.ParentID = after.Parent
			}
			md.SourceRepo = after.Repo
		}
	}
	if e.Before != "" {
		var before struct {
			Status string `json:"status"`
		}
		if err := json.Unmarshal([]byte(e.Before), &before); err == nil {
			md.OldStatus = before.Status
		}
	}
	return md
}

// fleetEventsToMutationData converts a slice of fleetMutationEvent to
// []backend.MutationData. Always returns a non-nil slice.
func fleetEventsToMutationData(events []fleetMutationEvent) []backend.MutationData {
	result := make([]backend.MutationData, 0, len(events))
	for i := range events {
		result = append(result, fleetEventToMutationData(&events[i]))
	}
	return result
}

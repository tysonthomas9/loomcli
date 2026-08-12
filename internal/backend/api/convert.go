package api

import (
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend/api/gen"
	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
)

func issueToSummary(issue gen.Issue) workitems.IssueSummary {
	d := workitems.IssueSummary{
		ID: issue.Id, Title: issue.Title, Priority: issue.Priority,
		CreatedAt: issue.CreatedAt, UpdatedAt: issue.UpdatedAt,
	}
	if issue.Status != nil {
		d.Status = string(*issue.Status)
	}
	if issue.IssueType != nil {
		d.IssueType = string(*issue.IssueType)
	}
	if issue.Assignee != nil {
		d.Assignee = *issue.Assignee
	}
	if issue.Owner != nil {
		d.Owner = *issue.Owner
	}
	if issue.Labels != nil {
		d.Labels = append([]string(nil), *issue.Labels...)
	} else {
		d.Labels = []string{}
	}
	if issue.SourceRepo != nil {
		d.SourceRepo = *issue.SourceRepo
		d.Repo = *issue.SourceRepo
	}
	if issue.Parent != nil {
		d.Parent = *issue.Parent
	}
	if issue.Design != nil {
		d.Design = *issue.Design
	}
	if issue.DesignArtifactId != nil {
		d.DesignArtifactID = *issue.DesignArtifactId
	}
	if issue.DesignFormat != nil {
		d.DesignFormat = string(*issue.DesignFormat)
	}
	if issue.HasDesign != nil {
		d.HasDesign = *issue.HasDesign
	}
	d.HasDesign = d.HasDesign || d.Design != ""
	if issue.ExternalRef != nil {
		d.ExternalRef = *issue.ExternalRef
	}
	d.DueAt = cloneTimePtr(issue.DueAt)
	d.DeferUntil = cloneTimePtr(issue.DeferUntil)
	return d
}

// issueResponseToSummary converts a generated detail response to the owner summary.
func issueResponseToSummary(r gen.IssueResponse) workitems.IssueSummary {
	d := workitems.IssueSummary{
		ID:              r.Id,
		Title:           r.Title,
		Status:          string(r.Status),
		Priority:        r.Priority,
		IssueType:       string(r.IssueType),
		CreatedAt:       r.CreatedAt,
		UpdatedAt:       r.UpdatedAt,
		Labels:          append([]string(nil), r.Labels...),
		DependencyCount: r.DependencyCount,
		DependentCount:  r.DependentCount,
	}
	if r.Assignee != nil {
		d.Assignee = *r.Assignee
	}
	if r.Owner != nil {
		d.Owner = *r.Owner
	}
	if r.SourceRepo != nil {
		d.SourceRepo = *r.SourceRepo
		d.Repo = *r.SourceRepo
	}
	if r.Parent != nil {
		d.Parent = *r.Parent
	}
	if r.Design != nil {
		d.Design = *r.Design
	}
	if r.DesignArtifactId != nil {
		d.DesignArtifactID = *r.DesignArtifactId
	}
	if r.DesignFormat != nil {
		d.DesignFormat = string(*r.DesignFormat)
	}
	if r.HasDesign != nil {
		d.HasDesign = *r.HasDesign
	}
	d.HasDesign = d.HasDesign || d.Design != ""
	if r.ExternalRef != nil {
		d.ExternalRef = *r.ExternalRef
	}
	d.DueAt = cloneTimePtr(r.DueAt)
	d.DeferUntil = cloneTimePtr(r.DeferUntil)
	if d.Labels == nil {
		d.Labels = []string{}
	}
	return d
}

// issueResponseToDetail converts a generated response to the owner detail.
func issueResponseToDetail(r gen.IssueResponse) workitems.IssueDetail {
	summary := issueResponseToSummary(r)
	detail := workitems.IssueDetail{
		ID: summary.ID, Title: summary.Title, Status: summary.Status, Priority: summary.Priority,
		IssueType: summary.IssueType, Assignee: summary.Assignee, Owner: summary.Owner,
		Labels: append([]string(nil), summary.Labels...), SourceRepo: summary.SourceRepo, Repo: summary.Repo,
		Parent: summary.Parent, Design: summary.Design, DesignArtifactID: summary.DesignArtifactID,
		DesignFormat: summary.DesignFormat, HasDesign: summary.HasDesign,
		CreatedAt: summary.CreatedAt, UpdatedAt: summary.UpdatedAt,
		ExternalRef: summary.ExternalRef, DueAt: summary.DueAt, DeferUntil: summary.DeferUntil,
	}
	if r.Description != nil {
		detail.Description = *r.Description
	}
	if r.AcceptanceCriteria != nil {
		detail.AcceptanceCriteria = *r.AcceptanceCriteria
	}
	if r.Notes != nil {
		detail.Notes = *r.Notes
	}
	detail.ClosedAt = cloneTimePtr(r.ClosedAt)
	if r.CloseReason != nil {
		detail.CloseReason = *r.CloseReason
	}
	if r.ExternalRef != nil {
		detail.ExternalRef = *r.ExternalRef
	}
	if r.EstimatedMinutes != nil {
		v := *r.EstimatedMinutes
		detail.EstimatedMinutes = &v
	}

	detail.Dependencies = dependencyRefs(r.Dependencies)
	detail.Dependents = dependencyRefs(r.Dependents)

	comments := make([]*workitems.Comment, 0, len(r.Comments))
	for _, c := range r.Comments {
		comments = append(comments, commentResponse(c, r.Id))
	}
	detail.Comments = comments

	return detail
}

func dependencyRefs(refs []gen.DependencyRef) []workitems.Dependency {
	out := make([]workitems.Dependency, 0, len(refs))
	for _, ref := range refs {
		d := workitems.Dependency{
			ID:             ref.Id,
			DependencyType: ref.Type,
			Title:          ref.Title,
			Status:         ref.Status,
			Priority:       ref.Priority,
			IssueType:      ref.IssueType,
		}
		out = append(out, d)
	}
	return out
}

func commentResponse(c gen.CommentResponse, issueID string) *workitems.Comment {
	return &workitems.Comment{
		ID:        c.Id,
		IssueID:   issueID,
		Author:    c.Author,
		Text:      c.Text,
		CreatedAt: c.CreatedAt,
	}
}

// commentToWorkItem converts a basic generated comment into the Work Items
// owner projection.
func commentToWorkItem(c gen.Comment) *workitems.Comment {
	return &workitems.Comment{
		ID:        c.Id,
		IssueID:   c.IssueId,
		Author:    c.Author,
		Text:      c.Text,
		CreatedAt: c.CreatedAt,
	}
}

// eventToWorkItem converts a generated event into the Work Items owner
// projection.
func eventToWorkItem(e gen.IssueEvent) *workitems.Event {
	return &workitems.Event{
		ID:        e.Id,
		IssueID:   e.IssueId,
		EventType: workitems.EventType(e.EventType),
		Actor:     e.Actor,
		CreatedAt: e.CreatedAt,
	}
}

// statisticsToStats converts the generated wire projection to the Work Items
// owner projection.
func statisticsToStats(s gen.Statistics) workitems.Stats {
	return workitems.Stats{
		TotalIssues:             s.TotalIssues,
		OpenIssues:              s.OpenIssues,
		InProgressIssues:        s.InProgressIssues,
		ClosedIssues:            s.ClosedIssues,
		BlockedIssues:           s.BlockedIssues,
		DeferredIssues:          s.DeferredIssues,
		ReadyIssues:             s.ReadyIssues,
		TombstoneIssues:         s.TombstoneIssues,
		PinnedIssues:            s.PinnedIssues,
		EpicsEligibleForClosure: s.EpicsEligibleForClosure,
		AverageLeadTime:         s.AverageLeadTimeHours,
	}
}

// blockedIssueToSummary converts the generated blocked wire projection to the
// Work Items owner projection.
func blockedIssueToSummary(b gen.BlockedIssue) workitems.IssueSummary {
	d := workitems.IssueSummary{
		ID:             b.Id,
		Title:          b.Title,
		Priority:       b.Priority,
		BlockedByCount: b.BlockedByCount,
		BlockedBy:      append([]string(nil), b.BlockedBy...),
		CreatedAt:      b.CreatedAt,
		UpdatedAt:      b.UpdatedAt,
	}
	if b.Status != nil {
		d.Status = string(*b.Status)
	}
	if b.IssueType != nil {
		d.IssueType = string(*b.IssueType)
	}
	if b.Assignee != nil {
		d.Assignee = *b.Assignee
	}
	if b.Owner != nil {
		d.Owner = *b.Owner
	}
	if b.Labels != nil {
		d.Labels = append([]string(nil), *b.Labels...)
	} else {
		d.Labels = []string{}
	}
	if b.SourceRepo != nil {
		d.SourceRepo = *b.SourceRepo
		d.Repo = *b.SourceRepo
	}
	if b.Parent != nil {
		d.Parent = *b.Parent
	}
	copyBlockedDesign(&d, b)
	if b.ExternalRef != nil {
		d.ExternalRef = *b.ExternalRef
	}
	d.DueAt = cloneTimePtr(b.DueAt)
	d.DeferUntil = cloneTimePtr(b.DeferUntil)
	return d
}

// --- Helpers ---

func copyBlockedDesign(d *workitems.IssueSummary, b gen.BlockedIssue) {
	if b.Design != nil {
		d.Design = *b.Design
	}
	if b.DesignArtifactId != nil {
		d.DesignArtifactID = *b.DesignArtifactId
	}
	if b.DesignFormat != nil {
		d.DesignFormat = string(*b.DesignFormat)
	}
	if b.HasDesign != nil {
		d.HasDesign = *b.HasDesign
	}
	d.HasDesign = d.HasDesign || d.Design != "" || d.DesignArtifactID != ""
}

func cloneTimePtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	v := *t
	return &v
}

package api

import (
	"strconv"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/backend/api/gen"
)

// issueToData converts a generated Issue (slim list projection) to
// backend.IssueData. Handles the pointer-heavy shape of the generated type.
func issueToData(issue gen.Issue) backend.IssueData {
	d := backend.IssueData{
		ID:        issue.Id,
		Title:     issue.Title,
		Priority:  issue.Priority,
		CreatedAt: issue.CreatedAt,
		UpdatedAt: issue.UpdatedAt,
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
	}
	if issue.Parent != nil {
		d.Parent = *issue.Parent
	}
	if issue.Design != nil {
		d.Design = *issue.Design
	}
	d.DueAt = cloneTimePtr(issue.DueAt)
	d.DeferUntil = cloneTimePtr(issue.DeferUntil)
	return d
}

// issueResponseToData converts a generated IssueResponse (rich single-issue
// projection) into a backend.IssueData (slim projection). This is used when
// mutation endpoints return an IssueResponse but the caller only needs the
// slim shape (e.g., Create).
func issueResponseToData(r gen.IssueResponse) backend.IssueData {
	d := backend.IssueData{
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
	}
	if r.Parent != nil {
		d.Parent = *r.Parent
	}
	if r.Design != nil {
		d.Design = *r.Design
	}
	d.DueAt = cloneTimePtr(r.DueAt)
	d.DeferUntil = cloneTimePtr(r.DeferUntil)
	if d.Labels == nil {
		d.Labels = []string{}
	}
	return d
}

// issueResponseToDetailData converts a generated IssueResponse to the richer
// backend.IssueDetailData. Used for the Get endpoint.
func issueResponseToDetailData(r gen.IssueResponse) backend.IssueDetailData {
	detail := backend.IssueDetailData{
		IssueData: issueResponseToData(r),
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

	detail.Dependencies = dependencyRefsToData(r.Id, r.Dependencies, true)
	detail.Dependents = dependencyRefsToData(r.Id, r.Dependents, false)

	comments := make([]backend.CommentData, 0, len(r.Comments))
	for _, c := range r.Comments {
		comments = append(comments, commentResponseToData(c, r.Id))
	}
	detail.Comments = comments

	return detail
}

// dependencyRefsToData converts a slice of generated DependencyRef into
// backend.DependencyData. The asOutgoing flag determines which side of the
// relation the parent issue sits on: for Dependencies (outgoing), the parent
// issue depends on each ref; for Dependents (incoming), each ref depends on
// the parent.
func dependencyRefsToData(parentID string, refs []gen.DependencyRef, asOutgoing bool) []backend.DependencyData {
	out := make([]backend.DependencyData, 0, len(refs))
	for _, ref := range refs {
		d := backend.DependencyData{
			Type:      ref.Type,
			Title:     ref.Title,
			Status:    ref.Status,
			Priority:  ref.Priority,
			IssueType: ref.IssueType,
		}
		if asOutgoing {
			d.IssueID = parentID
			d.DependsOnID = ref.Id
		} else {
			d.IssueID = ref.Id
			d.DependsOnID = parentID
		}
		out = append(out, d)
	}
	return out
}

// commentResponseToData converts gen.CommentResponse to backend.CommentData.
func commentResponseToData(c gen.CommentResponse, issueID string) backend.CommentData {
	d := backend.CommentData{
		ID:        c.Id,
		IssueID:   issueID,
		Author:    c.Author,
		Text:      c.Text,
		CreatedAt: c.CreatedAt,
	}
	if c.ParentId != nil {
		v := *c.ParentId
		d.ParentID = &v
	}
	d.EditedAt = cloneTimePtr(c.EditedAt)
	return d
}

// commentToData converts a basic gen.Comment to backend.CommentData.
func commentToData(c gen.Comment) backend.CommentData {
	return backend.CommentData{
		ID:        c.Id,
		IssueID:   c.IssueId,
		Author:    c.Author,
		Text:      c.Text,
		CreatedAt: c.CreatedAt,
	}
}

// eventToData converts gen.IssueEvent to backend.EventData.
func eventToData(e gen.IssueEvent) backend.EventData {
	return backend.EventData{
		ID:        strconv.FormatInt(e.Id, 10),
		IssueID:   e.IssueId,
		Kind:      e.EventType,
		Actor:     e.Actor,
		CreatedAt: e.CreatedAt,
	}
}

// statisticsToData converts gen.Statistics to backend.StatsData.
func statisticsToData(s gen.Statistics) backend.StatsData {
	return backend.StatsData{
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

// blockedIssueToData converts gen.BlockedIssue to backend.IssueData.
func blockedIssueToData(b gen.BlockedIssue) backend.IssueData {
	d := backend.IssueData{
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
	}
	if b.Parent != nil {
		d.Parent = *b.Parent
	}
	if b.Design != nil {
		d.Design = *b.Design
	}
	d.DueAt = cloneTimePtr(b.DueAt)
	d.DeferUntil = cloneTimePtr(b.DeferUntil)
	return d
}

// --- Helpers ---

func cloneTimePtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	v := *t
	return &v
}

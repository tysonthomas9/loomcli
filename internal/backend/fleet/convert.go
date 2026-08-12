package fleet

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
)

// fleetIssueWire mirrors fleet-db's wire shape and projects directly to the
// canonical backend representation consumed by the Work Items adapter.
type fleetIssueWire struct {
	ID               string     `json:"id,omitempty"`
	Title            string     `json:"title,omitempty"`
	Status           string     `json:"status,omitempty"`
	Priority         int        `json:"priority,omitempty"`
	Type             string     `json:"type,omitempty"`
	Assignee         string     `json:"assignee,omitempty"`
	Owner            string     `json:"owner,omitempty"`
	Labels           []string   `json:"labels,omitempty"`
	Repo             string     `json:"repo,omitempty"`
	SourceRepo       string     `json:"source_repo,omitempty"`
	ParentID         string     `json:"parent_id,omitempty"`
	Parent           string     `json:"parent,omitempty"`
	Design           string     `json:"design,omitempty"`
	DesignArtifactID string     `json:"design_artifact_id,omitempty"`
	DesignFormat     string     `json:"design_format,omitempty"`
	HasDesign        bool       `json:"has_design"`
	Notes            string     `json:"notes,omitempty"`
	Description      string     `json:"description,omitempty"`
	Acceptance       string     `json:"acceptance_criteria,omitempty"`
	ExternalRef      string     `json:"external_ref,omitempty"`
	CreatedAt        time.Time  `json:"created_at,omitempty"`
	CreatedBy        string     `json:"created_by,omitempty"`
	UpdatedAt        time.Time  `json:"updated_at,omitempty"`
	DueAt            *time.Time `json:"due_at,omitempty"`
	DeferUntil       *time.Time `json:"defer_until,omitempty"`
	ClosedAt         *time.Time `json:"closed_at,omitempty"`
	CloseReason      string     `json:"close_reason,omitempty"`
}

func (w fleetIssueWire) toIssueData() backend.IssueData {
	summary := w.toIssueSummary()
	labels := make([]string, len(summary.Labels))
	copy(labels, summary.Labels)
	return backend.IssueData{
		ID: summary.ID, Title: summary.Title, Status: summary.Status, Priority: summary.Priority,
		IssueType: summary.IssueType, Assignee: summary.Assignee, Owner: summary.Owner,
		Labels: labels, SourceRepo: summary.SourceRepo,
		Parent: summary.Parent, Design: summary.Design, DesignArtifactID: summary.DesignArtifactID,
		DesignFormat: summary.DesignFormat, HasDesign: summary.HasDesign, Notes: summary.Notes,
		CreatedBy: summary.CreatedBy, CreatedAt: summary.CreatedAt, UpdatedAt: summary.UpdatedAt,
		ClosedAt: summary.ClosedAt, CloseReason: summary.CloseReason, ExternalRef: summary.ExternalRef,
		DueAt: summary.DueAt, DeferUntil: summary.DeferUntil,
	}
}

func (w fleetIssueWire) toIssueSummary() workitems.IssueSummary {
	labels := append([]string(nil), w.Labels...)
	if labels == nil {
		labels = []string{}
	}
	sourceRepo := w.sourceRepo()
	return workitems.IssueSummary{
		ID: w.ID, Title: w.Title, Status: w.Status, Priority: w.Priority,
		IssueType: w.Type, Assignee: w.Assignee, Owner: w.Owner, Labels: labels,
		SourceRepo: sourceRepo, Repo: sourceRepo, Parent: w.parent(), Design: w.Design,
		DesignArtifactID: w.DesignArtifactID, DesignFormat: w.DesignFormat,
		HasDesign: w.HasDesign || w.Design != "", Notes: w.Notes,
		CreatedBy: w.CreatedBy, CreatedAt: w.CreatedAt, UpdatedAt: w.UpdatedAt,
		ClosedAt: w.ClosedAt, CloseReason: w.CloseReason, ExternalRef: w.ExternalRef,
		DueAt: w.DueAt, DeferUntil: w.DeferUntil,
	}
}

func (w fleetIssueWire) toIssueDetailData() backend.IssueDetailData {
	return backend.IssueDetailData{
		IssueData:          w.toIssueData(),
		Description:        w.Description,
		AcceptanceCriteria: w.Acceptance,
		Dependencies:       []backend.DependencyData{},
		Dependents:         []backend.DependencyData{},
		Comments:           []backend.CommentData{},
	}
}

func (w fleetIssueWire) parent() string {
	if w.ParentID != "" {
		return w.ParentID
	}
	return w.Parent
}

func (w fleetIssueWire) sourceRepo() string {
	if w.Repo != "" {
		return w.Repo
	}
	return w.SourceRepo
}

// fleetIssueWithCountsWire mirrors fleet-db's IssueWithCounts wrapper.
type fleetIssueWithCountsWire struct {
	fleetIssueWire
	DependencyCount int `json:"dependency_count,omitempty"`
	DependentCount  int `json:"dependent_count,omitempty"`
}

// toIssueData projects the fleet wire shape directly to the backend adapter
// representation with counts populated.
func (w fleetIssueWithCountsWire) toIssueData() backend.IssueData {
	d := w.fleetIssueWire.toIssueData()
	d.DependencyCount = w.DependencyCount
	d.DependentCount = w.DependentCount
	return d
}

func (w fleetIssueWithCountsWire) toIssueSummary() workitems.IssueSummary {
	summary := w.fleetIssueWire.toIssueSummary()
	summary.DependencyCount = w.DependencyCount
	summary.DependentCount = w.DependentCount
	return summary
}

// readyIssueWithParent mirrors webui.ReadyIssueWithParent for JSON parsing.
// Embeds fleetIssueWire so fleet-db's `type` field is captured during
// unmarshal.
type readyIssueWithParent struct {
	fleetIssueWire
	Parent      *string `json:"parent,omitempty"`
	ParentTitle *string `json:"parent_title,omitempty"`
	Repo        *string `json:"repo,omitempty"`
}

// blockedIssueResponseWire mirrors fleet-db's native blocked response shape:
// {"issue": {...}, "blockers": [{...}]}.
type blockedIssueResponseWire struct {
	Issue    fleetIssueWire       `json:"issue"`
	Blockers []blockedBlockerWire `json:"blockers,omitempty"`
}

type blockedBlockerWire struct {
	ID       string `json:"id"`
	Title    string `json:"title,omitempty"`
	Priority int    `json:"priority,omitempty"`
	Status   string `json:"status,omitempty"`
	DepType  string `json:"dep_type,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// countIssuesResponse is the JSON structure returned by the fleet server's
// /issues/count?group_by=status endpoint.
type countIssuesResponse struct {
	Total  int64            `json:"total"`
	Groups map[string]int64 `json:"groups"`
}

// closeResultJSON is the JSON structure returned by the close endpoint.
type closeResultJSON struct {
	Closed    *fleetIssueWire   `json:"closed,omitempty"`
	Unblocked []*fleetIssueWire `json:"unblocked,omitempty"`
}

// commentToData converts workitems.Comment to backend.CommentData.
func commentToData(c *workitems.Comment) backend.CommentData {
	return backend.CommentData{
		ID:        c.ID,
		IssueID:   c.IssueID,
		Author:    c.Author,
		Text:      c.Text,
		CreatedAt: c.CreatedAt,
	}
}

// commentsToData is the temporary issue-detail translation. It disappears
// with IssueDetailData; comment operations already use Work Items models.
func commentsToData(comments []*workitems.Comment) []backend.CommentData {
	result := make([]backend.CommentData, 0, len(comments))
	for _, comment := range comments {
		if comment != nil {
			result = append(result, commentToData(comment))
		}
	}
	return result
}

// availabilityIssuesToSummaries converts a FleetDB availability response to
// the Work Items owner projection.
func availabilityIssuesToSummaries(issues []*readyIssueWithParent) []workitems.IssueSummary {
	result := make([]workitems.IssueSummary, 0, len(issues))
	for _, riwp := range issues {
		if riwp == nil {
			continue
		}
		d := riwp.fleetIssueWire.toIssueSummary()
		if parent := riwp.fleetIssueWire.parent(); parent != "" {
			d.Parent = parent
		}
		if riwp.Parent != nil {
			d.Parent = *riwp.Parent
		}
		if riwp.Repo != nil {
			d.SourceRepo = *riwp.Repo
			d.Repo = *riwp.Repo
		}
		result = append(result, d)
	}
	return result
}

// unmarshalBlockedIssueList accepts only FleetDB's canonical native blocked
// response. The former flat Loom bridge dialect was a cross-version fallback
// and is intentionally rejected.
func unmarshalBlockedIssueList(data []byte, op string) ([]workitems.IssueSummary, error) {
	nested, ok := unmarshalNativeBlockedIssues(data)
	if !ok {
		return nil, backend.ErrInternal(op, "unmarshal canonical blocked response", nil)
	}
	return blockedIssueResponsesToSummaries(nested), nil
}

func unmarshalNativeBlockedIssues(data []byte) ([]blockedIssueResponseWire, bool) {
	var bare []blockedIssueResponseWire
	if err := json.Unmarshal(data, &bare); err == nil {
		if len(bare) == 0 || blockedResponsesHaveIssue(bare) {
			return bare, true
		}
		return nil, false
	}

	var wrapper struct {
		Issues []blockedIssueResponseWire `json:"issues"`
	}
	if err := json.Unmarshal(data, &wrapper); err == nil && wrapper.Issues != nil {
		if len(wrapper.Issues) == 0 || blockedResponsesHaveIssue(wrapper.Issues) {
			return wrapper.Issues, true
		}
	}
	return nil, false
}

func blockedResponsesHaveIssue(issues []blockedIssueResponseWire) bool {
	for _, issue := range issues {
		if issue.Issue.ID != "" {
			return true
		}
	}
	return false
}

func blockedIssueResponsesToSummaries(issues []blockedIssueResponseWire) []workitems.IssueSummary {
	result := make([]workitems.IssueSummary, 0, len(issues))
	for _, entry := range issues {
		if entry.Issue.ID == "" {
			continue
		}
		d := entry.Issue.toIssueSummary()
		if parent := entry.Issue.parent(); parent != "" {
			d.Parent = parent
		}
		for _, blocker := range entry.Blockers {
			if blocker.ID != "" {
				d.BlockedBy = append(d.BlockedBy, blocker.ID)
			}
		}
		d.BlockedByCount = len(d.BlockedBy)
		result = append(result, d)
	}
	return result
}

// closeResultJSONToData converts the close endpoint's JSON response to backend.CloseResult.
func closeResultJSONToData(cr *closeResultJSON) *backend.CloseResult {
	result := &backend.CloseResult{
		Unblocked: make([]backend.IssueData, 0),
	}
	if cr.Closed != nil {
		closed := cr.Closed.toIssueData()
		result.Closed = &closed
	}
	for _, u := range cr.Unblocked {
		if u != nil {
			result.Unblocked = append(result.Unblocked, u.toIssueData())
		}
	}
	return result
}

// fleetMutationEvent mirrors fleet-db's Event shape (see openapi.yaml
// components.schemas.Event). It is distinct from workitems.Event, which models an
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
// Work Items mutation constants. Fleet emits fine-grained actions; loom's
// mutation-type space is coarser, so several actions fold into MutationUpdate.
func actionToMutationType(action, entityType string) string {
	switch action {
	case "issue.create":
		return workitems.MutationCreate
	case "issue.delete":
		return workitems.MutationDelete
	case "issue.close", "issue.reopen", "issue.update", "issue.claim",
		"issue.release", "issue.assign", "issue.defer", "issue.undefer":
		// All status / metadata transitions on an issue collapse into "update"
		// from the subscriber's point of view — callers that care about the
		// specific transition read OldStatus/NewStatus.
		if action == "issue.close" || action == "issue.reopen" {
			return workitems.MutationStatus
		}
		return workitems.MutationUpdate
	case "comment.add":
		return workitems.MutationComment
	case "dep.add", "dep.remove", "label.add", "label.remove":
		return workitems.MutationUpdate
	}
	// Workspace-level actions and any future additions fall back to
	// MutationRefresh so SSE consumers invalidate their caches.
	if entityType == "workspace" {
		return workitems.MutationRefresh
	}
	return workitems.MutationUpdate
}

// fleetEventToMutation converts a single fleet mutation event into the Work
// Items-owned durable stream projection. Title/status/parent fields come from the event's
// after-snapshot JSON when present; fields absent from fleet's event model
// (StepCount, Assignee for non-assign actions) remain zero.
func fleetEventToMutation(e *fleetMutationEvent) workitems.Mutation {
	md := workitems.Mutation{
		Cursor:     e.ID,
		Type:       actionToMutationType(e.Action, e.EntityType),
		EntityType: e.EntityType,
		EntityID:   e.EntityID,
		Action:     e.Action,
		Actor:      e.Actor,
		Timestamp:  e.Timestamp,
	}
	if e.EntityType == "issue" || (e.EntityType == "" && strings.HasPrefix(e.Action, "issue.")) {
		md.IssueID = e.EntityID
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

// fleetEventsToMutations converts FleetDB events to the Work Items-owned
// projection. It always returns a non-nil slice.
func fleetEventsToMutations(events []fleetMutationEvent) []workitems.Mutation {
	result := make([]workitems.Mutation, 0, len(events))
	for i := range events {
		result = append(result, fleetEventToMutation(&events[i]))
	}
	return result
}

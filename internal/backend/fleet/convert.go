package fleet

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/types"
)

// fleetIssueWire mirrors fleet-db's wire shape so unmarshal captures
// `type` into a struct field. types.Issue tags the same field as
// `issue_type`, so fleet responses need this projection step.
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

// toIssue projects the wire shape to the canonical types.Issue.
func (w fleetIssueWire) toIssue() types.Issue {
	return types.Issue{
		ID:                 w.ID,
		Title:              w.Title,
		Description:        w.Description,
		AcceptanceCriteria: w.Acceptance,
		Notes:              w.Notes,
		Status:             types.Status(w.Status),
		Priority:           w.Priority,
		IssueType:          types.IssueType(w.Type),
		Assignee:           w.Assignee,
		Owner:              w.Owner,
		Labels:             w.Labels,
		SourceRepo:         w.sourceRepo(),
		Design:             w.Design,
		DesignArtifactID:   w.DesignArtifactID,
		DesignFormat:       w.DesignFormat,
		HasDesign:          w.HasDesign || w.Design != "",
		ExternalRef:        strOrNil(w.ExternalRef),
		CreatedAt:          w.CreatedAt,
		CreatedBy:          w.CreatedBy,
		UpdatedAt:          w.UpdatedAt,
		DueAt:              w.DueAt,
		DeferUntil:         w.DeferUntil,
		ClosedAt:           w.ClosedAt,
		CloseReason:        w.CloseReason,
	}
}

// strOrNil maps a fleet-db wire string to the optional *string the canonical
// types.Issue uses (empty -> nil). derefStr is its inverse for the slim
// backend.IssueData projection (nil -> "").
func strOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
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

// toIssueData projects the wire shape directly to backend.IssueData with
// counts populated. Used by the list / Get paths instead of going through
// types.IssueWithCounts so the type-tag mismatch (`type` vs `issue_type`)
// is resolved at the wire boundary.
func (w fleetIssueWithCountsWire) toIssueData() backend.IssueData {
	issue := w.toIssue()
	d := issueToData(&issue)
	if parent := w.parent(); parent != "" {
		d.Parent = parent
	}
	d.DependencyCount = w.DependencyCount
	d.DependentCount = w.DependentCount
	return d
}

// readyIssueWithParent mirrors webui.ReadyIssueWithParent for JSON parsing.
// Embeds fleetIssueWire (not types.Issue) so fleet-db's `type` field is
// captured during unmarshal — see fleetIssueWire docstring for the
// type-vs-issue_type rename rationale.
type readyIssueWithParent struct {
	fleetIssueWire
	Parent      *string `json:"parent,omitempty"`
	ParentTitle *string `json:"parent_title,omitempty"`
	Repo        *string `json:"repo,omitempty"`
}

// blockedIssueWire mirrors types.BlockedIssue but embeds fleetIssueWire so
// fleet-db's `type` field survives unmarshal. types.BlockedIssue embeds
// types.Issue (json tag `issue_type`) and would silently drop the type
// field on every fleet response.
//
// The BlockedByDetails field is captured for unmarshal completeness only;
// IssueData carries the summary blocker fields used by kanban/list views.
type blockedIssueWire struct {
	fleetIssueWire
	BlockedByCount   int                `json:"blocked_by_count,omitempty"`
	BlockedBy        []string           `json:"blocked_by,omitempty"`
	BlockedByDetails []types.BlockerRef `json:"blocked_by_details,omitempty"`
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
		ID:               issue.ID,
		Title:            issue.Title,
		Status:           string(issue.Status),
		Priority:         issue.Priority,
		IssueType:        string(issue.IssueType),
		Assignee:         issue.Assignee,
		Owner:            issue.Owner,
		Labels:           labels,
		SourceRepo:       issue.SourceRepo,
		Design:           issue.Design,
		DesignArtifactID: issue.DesignArtifactID,
		DesignFormat:     issue.DesignFormat,
		HasDesign:        issue.HasDesign || issue.Design != "",
		Notes:            issue.Notes,
		ExternalRef:      derefStr(issue.ExternalRef),
		CreatedAt:        issue.CreatedAt,
		UpdatedAt:        issue.UpdatedAt,
		DueAt:            issue.DueAt,
		DeferUntil:       issue.DeferUntil,
		CreatedBy:        issue.CreatedBy,
		ClosedAt:         issue.ClosedAt,
		CloseReason:      issue.CloseReason,
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

	// Lifecycle. CreatedBy/ClosedAt/CloseReason are populated by
	// issueToData via the embedded IssueData; no duplicate assignment.
	d.ClosedBySession = details.ClosedBySession

	// External integration.
	d.ExternalRef = derefStr(details.ExternalRef)
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
	changes := make([]backend.FieldChange, 0, len(e.Changes))
	for _, change := range e.Changes {
		changes = append(changes, backend.FieldChange{
			Field:  change.Field,
			Before: change.Before,
			After:  change.After,
		})
	}

	return backend.EventData{
		ID:         e.ID,
		IssueID:    e.IssueID,
		Kind:       string(e.EventType),
		Actor:      e.Actor,
		Target:     e.Target,
		Payload:    e.Payload,
		Category:   e.Category,
		Summary:    e.Summary,
		Changes:    changes,
		Metadata:   e.Metadata,
		Field:      e.Field,
		Fields:     append([]string(nil), e.Fields...),
		FieldCount: e.FieldCount,
		OldValue:   e.OldValue,
		NewValue:   e.NewValue,
		Comment:    e.Comment,
		CreatedAt:  e.CreatedAt,
	}
}

// readyIssuesToData converts the ready endpoint's response to []backend.IssueData.
func readyIssuesToData(issues []*readyIssueWithParent) []backend.IssueData {
	result := make([]backend.IssueData, 0, len(issues))
	for _, riwp := range issues {
		if riwp == nil {
			continue
		}
		issue := riwp.fleetIssueWire.toIssue()
		d := issueToData(&issue)
		if parent := riwp.fleetIssueWire.parent(); parent != "" {
			d.Parent = parent
		}
		if riwp.Parent != nil {
			d.Parent = *riwp.Parent
		}
		if riwp.Repo != nil {
			d.SourceRepo = *riwp.Repo
		}
		result = append(result, d)
	}
	return result
}

// blockedIssuesToData converts blocked issues to []backend.IssueData.
func blockedIssuesToData(issues []*blockedIssueWire) []backend.IssueData {
	result := make([]backend.IssueData, 0, len(issues))
	for _, bi := range issues {
		if bi == nil {
			continue
		}
		issue := bi.fleetIssueWire.toIssue()
		d := issueToData(&issue)
		if parent := bi.fleetIssueWire.parent(); parent != "" {
			d.Parent = parent
		}
		d.BlockedByCount = bi.BlockedByCount
		d.BlockedBy = append([]string(nil), bi.BlockedBy...)
		result = append(result, d)
	}
	return result
}

// unmarshalBlockedIssueList accepts both blocked response dialects:
// fleet-db native {"issues":[{"issue":...,"blockers":[...]}]} and the older
// flat BlockedIssue shape used by loom's legacy API bridge.
func unmarshalBlockedIssueList(data []byte, op string) ([]backend.IssueData, error) {
	if nested, ok := unmarshalNativeBlockedIssues(data); ok {
		return blockedIssueResponsesToData(nested), nil
	}
	issues, err := unmarshalListOrWrapper[*blockedIssueWire](data, op)
	if err != nil {
		return nil, err
	}
	return blockedIssuesToData(issues), nil
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

func blockedIssueResponsesToData(issues []blockedIssueResponseWire) []backend.IssueData {
	result := make([]backend.IssueData, 0, len(issues))
	for _, entry := range issues {
		if entry.Issue.ID == "" {
			continue
		}
		issue := entry.Issue.toIssue()
		d := issueToData(&issue)
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

// fleetEventsToMutationData converts a slice of fleetMutationEvent to
// []backend.MutationData. Always returns a non-nil slice.
func fleetEventsToMutationData(events []fleetMutationEvent) []backend.MutationData {
	result := make([]backend.MutationData, 0, len(events))
	for i := range events {
		result = append(result, fleetEventToMutationData(&events[i]))
	}
	return result
}

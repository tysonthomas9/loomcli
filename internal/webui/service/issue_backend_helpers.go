package service

import (
	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/types"
)

// This file holds pure-data translators between webui service-layer params /
// types and the backend-layer wire types. Helpers live in their own file so
// that issue_impl.go stays focused on orchestration.
//
// Wire-shape contract: the return shapes here must match the JSON the
// frontend already consumes. The webui frontend types (IssueDetails, Issue,
// Comment, Event) are the source of truth; helpers here map
// backend.IssueDetailData -> those wire shapes verbatim so the migration is a
// no-op for FE consumers.

// --- Param translators (webui → backend) ---

// createParamsToBackend maps the webui-level CreateIssueParams onto the
// backend-level CreateParams. SourceRepo must flow through for multi-repo
// workspaces so FleetDB can persist the issue against the selected repo.
func createParamsToBackend(p *CreateIssueParams) backend.CreateParams {
	return backend.CreateParams{
		ID:                 p.ID,
		Parent:             p.Parent,
		Title:              p.Title,
		Description:        p.Description,
		Status:             p.Status,
		IssueType:          p.IssueType,
		Priority:           p.Priority,
		Design:             p.Design,
		AcceptanceCriteria: p.AcceptanceCriteria,
		Notes:              p.Notes,
		Assignee:           p.Assignee,
		Owner:              p.Owner,
		CreatedBy:          p.CreatedBy,
		ExternalRef:        p.ExternalRef,
		EstimatedMinutes:   p.EstimatedMinutes,
		Labels:             p.Labels,
		Dependencies:       p.Dependencies,
		SourceRepo:         p.SourceRepo,
		DueAt:              p.DueAt,
		DeferUntil:         p.DeferUntil,
		IdempotencyKey:     p.IdempotencyKey,
		Force:              p.Force,
	}
}

// patchParamsToBackendUpdate maps PatchIssueParams onto backend.UpdateParams.
// Note: backend.UpdateParams does not currently expose a Pinned field — the
// pin/unpin operation is not yet exposed by IssueBackend. When the IssueBackend
// interface gains explicit pin support, wire it through here. For now we
// drop p.Pinned and surface a clear log warning so behavior changes are
// noticed in operation.
func patchParamsToBackendUpdate(p *PatchIssueParams) backend.UpdateParams {
	return backend.UpdateParams{
		Actor:              p.Actor,
		Title:              p.Title,
		Description:        p.Description,
		Status:             p.Status,
		Priority:           p.Priority,
		Assignee:           p.Assignee,
		Owner:              p.Owner,
		Design:             p.Design,
		DesignFormat:       p.DesignFormat,
		AcceptanceCriteria: p.AcceptanceCriteria,
		Notes:              p.Notes,
		ExternalRef:        p.ExternalRef,
		EstimatedMinutes:   p.EstimatedMinutes,
		IssueType:          p.IssueType,
		Repo:               p.Repo,
		AddLabels:          p.AddLabels,
		RemoveLabels:       p.RemoveLabels,
		SetLabels:          p.SetLabels,
		Parent:             p.Parent,
		AgentState:         p.AgentState,
		DueAt:              p.DueAt,
		DeferUntil:         p.DeferUntil,
	}
}

// --- Wire-shape converters (backend → JSON shape FE expects) ---

// issueDataToWire converts a backend.IssueData to a wire-format value whose
// JSON matches what *rpc.Client previously returned for create/claim
// responses (a bare types.Issue body).
//
// The function returns map[string]any rather than *types.Issue because
// types.Issue carries fields (e.g. CreatedBy) the slim IssueData does not
// populate, and we want to omit fields we don't know rather than emit
// zero-value sentinels (e.g. empty strings) the FE may misinterpret.
func issueDataToWire(d *backend.IssueData) map[string]any {
	if d == nil {
		return nil
	}
	out := map[string]any{
		"id":         d.ID,
		"title":      d.Title,
		"status":     d.Status,
		"priority":   d.Priority,
		"created_at": d.CreatedAt,
		"updated_at": d.UpdatedAt,
	}
	addOptionalIssueFields(out, d)
	return out
}

// addOptionalIssueFields adds the omitempty-style fields from IssueData onto
// the wire map, matching the JSON tags on types.Issue.
func addOptionalIssueFields(out map[string]any, d *backend.IssueData) {
	if d.IssueType != "" {
		out["issue_type"] = d.IssueType
	}
	if d.Assignee != "" {
		out["assignee"] = d.Assignee
	}
	if d.Owner != "" {
		out["owner"] = d.Owner
	}
	if len(d.Labels) > 0 {
		out["labels"] = d.Labels
	}
	if d.SourceRepo != "" {
		out["source_repo"] = d.SourceRepo
		out["repo"] = d.SourceRepo
	}
	if d.Parent != "" {
		out["parent"] = d.Parent
	}
	if d.Design != "" {
		out["design"] = d.Design
	}
	out["has_design"] = d.HasDesign || d.Design != ""
	if d.DesignArtifactID != "" {
		out["design_artifact_id"] = d.DesignArtifactID
	}
	if d.DesignFormat != "" {
		out["design_format"] = d.DesignFormat
	}
	if d.DueAt != nil {
		out["due_at"] = d.DueAt
	}
	if d.DeferUntil != nil {
		out["defer_until"] = d.DeferUntil
	}
}

// issueDetailDataToWire converts a backend.IssueDetailData into the wire
// JSON shape the FE expects for GET /api/issues/:id (matches
// types.IssueDetails).
//
// Notable shape conversions:
//   - backend.DependencyData uses field name "type"; the FE expects the
//     embedded-Issue + "dependency_type" shape from
//     types.IssueWithDependencyMetadata.
//   - backend.CommentData carries optional ParentID/EditedAt; the FE's
//     Comment type does not currently surface them, so they're dropped.
//   - Dependencies/Dependents/Comments/Labels are emitted as empty slices
//     when nil to match types.IssueDetails (which uses non-omitempty tags).
func issueDetailDataToWire(d *backend.IssueDetailData) map[string]any {
	if d == nil {
		return nil
	}
	out := map[string]any{
		"id":         d.ID,
		"title":      d.Title,
		"status":     d.Status,
		"priority":   d.Priority,
		"created_at": d.CreatedAt,
		"updated_at": d.UpdatedAt,
	}
	addOptionalIssueFields(out, &d.IssueData)

	// Detail-only fields (omitempty unless explicitly required by FE shape).
	if d.Description != "" {
		out["description"] = d.Description
	}
	if d.AcceptanceCriteria != "" {
		out["acceptance_criteria"] = d.AcceptanceCriteria
	}
	if d.Notes != "" {
		out["notes"] = d.Notes
	}
	if d.CreatedBy != "" {
		out["created_by"] = d.CreatedBy
	}
	if d.ClosedAt != nil {
		out["closed_at"] = d.ClosedAt
	}
	if d.CloseReason != "" {
		out["close_reason"] = d.CloseReason
	}
	if d.ClosedBySession != "" {
		out["closed_by_session"] = d.ClosedBySession
	}
	if d.ExternalRef != "" {
		out["external_ref"] = d.ExternalRef
	}
	if d.EstimatedMinutes != nil {
		out["estimated_minutes"] = *d.EstimatedMinutes
	}

	// Labels / Dependencies / Dependents / Comments are non-omitempty in
	// types.IssueDetails to give the FE a stable shape.
	out["labels"] = stringsOrEmpty(d.Labels)
	out["dependencies"] = depsToWire(d.Dependencies, d.ID)
	out["dependents"] = depsToWire(d.Dependents, d.ID)
	out["comments"] = commentsToWire(d.Comments)

	return out
}

// stringsOrEmpty returns []string{} when in is nil so that JSON encoding
// emits `[]` instead of `null`.
func stringsOrEmpty(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

// depsToWire maps backend.DependencyData to the FE's
// IssueWithDependencyMetadata shape (embedded Issue + dependency_type).
//
// selfID is the issue being viewed. The wire "id" must be the *related* issue
// (the other side of the relationship), not selfID. For a dependency the
// related issue is DependsOnID; for a dependent (e.g. an epic's child, where
// DependsOnID == selfID) it is IssueID. Emitting DependsOnID unconditionally
// is what made every dependent render as a self-reference to the epic.
func depsToWire(in []backend.DependencyData, selfID string) []map[string]any {
	if len(in) == 0 {
		return []map[string]any{}
	}
	out := make([]map[string]any, 0, len(in))
	for _, d := range in {
		relatedID := d.DependsOnID
		if d.DependsOnID == selfID && d.IssueID != "" {
			relatedID = d.IssueID
		}
		entry := map[string]any{
			"id":              relatedID,
			"title":           d.Title,
			"status":          d.Status,
			"priority":        d.Priority,
			"created_at":      d.CreatedAt,
			"dependency_type": d.Type,
		}
		if d.IssueType != "" {
			entry["issue_type"] = d.IssueType
		}
		if d.CreatedBy != "" {
			entry["created_by"] = d.CreatedBy
		}
		out = append(out, entry)
	}
	return out
}

// commentsToWire maps backend.CommentData to the FE's Comment shape.
func commentsToWire(in []backend.CommentData) []map[string]any {
	if len(in) == 0 {
		return []map[string]any{}
	}
	out := make([]map[string]any, 0, len(in))
	for _, c := range in {
		out = append(out, map[string]any{
			"id":         c.ID,
			"issue_id":   c.IssueID,
			"author":     c.Author,
			"text":       c.Text,
			"created_at": c.CreatedAt,
		})
	}
	return out
}

// closeResultToWire converts a backend.CloseResult into a wire-format value.
//
// The previous direct-RPC payload was rpc.CloseResult with
// {closed: <Issue>, unblocked: [<Issue>...]} when SuggestNext=true and a
// bare <Issue> when SuggestNext=false. The migrated path always returns the
// wrapped shape because CloseResult is the typed return; FE consumers
// already handle either shape (loomcli-7w9tc.14 fixed the bare-issue parse
// in the backend, and the wrapper is the canonical response).
func closeResultToWire(cr *backend.CloseResult) map[string]any {
	if cr == nil {
		return nil
	}
	out := map[string]any{}
	if cr.Closed != nil {
		out["closed"] = issueDataToWire(cr.Closed)
	} else {
		out["closed"] = nil
	}
	unblocked := make([]map[string]any, 0, len(cr.Unblocked))
	for i := range cr.Unblocked {
		unblocked = append(unblocked, issueDataToWire(&cr.Unblocked[i]))
	}
	out["unblocked"] = unblocked
	return out
}

// commentDataToTypesComment maps backend.CommentData onto the strongly
// typed types.Comment used by the AddComment handler response shape.
func commentDataToTypesComment(d *backend.CommentData) *types.Comment {
	if d == nil {
		return nil
	}
	return &types.Comment{
		ID:        d.ID,
		IssueID:   d.IssueID,
		Author:    d.Author,
		Text:      d.Text,
		CreatedAt: d.CreatedAt,
	}
}

// eventDataToTypesEvent maps backend.EventData onto the strongly typed
// types.Event used by the ListEvents handler response shape.
func eventDataToTypesEvent(d backend.EventData) *types.Event {
	changes := make([]types.FieldChange, 0, len(d.Changes))
	for _, change := range d.Changes {
		changes = append(changes, types.FieldChange{
			Field:  change.Field,
			Before: change.Before,
			After:  change.After,
		})
	}

	return &types.Event{
		ID:        d.ID,
		IssueID:   d.IssueID,
		EventType: types.EventType(d.Kind),
		Actor:     d.Actor,
		Target:    d.Target,
		Payload:   d.Payload,
		Category:  d.Category,
		Summary:   d.Summary,
		Changes:   changes,
		Metadata:  d.Metadata,
		CreatedAt: d.CreatedAt,
	}
}

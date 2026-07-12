// Package mapping converts between backend wire types and entity domain types.
//
// The mapping layer sits between the data access layer (IssueBackend) and the
// business logic layer (service). It enables the service layer to work with
// rich, validated domain types while the backend layer deals with flat,
// transport-oriented wire types.
//
// Mapping functions are pure structural converters — they do NOT validate
// enum values or check field invariants. Validation is the caller's
// responsibility (typically the service layer or entity.Issue.Validate()).
//
// Round-trip through IssueData is intentionally lossy: IssueData is a slim
// projection carrying ~16 fields; the ~35+ entity fields not present in
// IssueData are zeroed on the inbound trip. Round-trip through IssueDetailData
// is near-lossless for the fields it carries, but still drops storage-internal
// and entity-only fields (tombstone, HOP, gate, bonding, messaging, etc.).
package mapping

import (
	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/entity"
)

// IssueFromData converts a slim backend.IssueData wire type to entity.Issue.
// Fields not present in IssueData (Description, Design, Notes, HOP fields, etc.)
// will be zero-valued. See the field survival table in the task design.
// The Parent, DependencyCount, and DependentCount fields are dropped.
func IssueFromData(d backend.IssueData) entity.Issue {
	labels := d.Labels
	if labels == nil {
		labels = []string{}
	}
	return entity.Issue{
		ID:               d.ID,
		Title:            d.Title,
		Status:           entity.IssueStatus(d.Status),
		Priority:         d.Priority,
		IssueType:        entity.IssueType(d.IssueType),
		Assignee:         d.Assignee,
		Owner:            d.Owner,
		Labels:           labels,
		SourceRepo:       d.SourceRepo,
		DesignArtifactID: d.DesignArtifactID,
		HasDesign:        d.HasDesign || d.Design != "",
		CreatedAt:        d.CreatedAt,
		UpdatedAt:        d.UpdatedAt,
		DueAt:            d.DueAt,
		DeferUntil:       d.DeferUntil,
		Dependencies:     make([]*entity.Dependency, 0),
		Comments:         make([]*entity.Comment, 0),
	}
}

// IssueFromDetailData converts a full backend.IssueDetailData wire type to entity.Issue.
// Populates all content fields, dependencies, and comments.
// Dependencies and Dependents from IssueDetailData are combined into a single
// entity.Issue.Dependencies slice — the direction is encoded in IssueID/DependsOnID.
// ExternalRef conversion: empty string → nil, non-empty → &val.
func IssueFromDetailData(d backend.IssueDetailData) entity.Issue {
	e := IssueFromData(d.IssueData)

	// Content fields.
	e.Description = d.Description
	e.Design = d.Design
	e.AcceptanceCriteria = d.AcceptanceCriteria
	e.Notes = d.Notes

	// Lifecycle fields.
	e.CreatedBy = d.CreatedBy
	e.ClosedAt = d.ClosedAt
	e.CloseReason = d.CloseReason
	e.ClosedBySession = d.ClosedBySession

	// ExternalRef: empty string → nil, non-empty → &val.
	if d.ExternalRef != "" {
		val := d.ExternalRef
		e.ExternalRef = &val
	}

	e.EstimatedMinutes = d.EstimatedMinutes

	// Combine Dependencies and Dependents into a single slice.
	deps := make([]*entity.Dependency, 0, len(d.Dependencies)+len(d.Dependents))
	for i := range d.Dependencies {
		deps = append(deps, DependencyFromData(d.Dependencies[i]))
	}
	for i := range d.Dependents {
		deps = append(deps, DependencyFromData(d.Dependents[i]))
	}
	e.Dependencies = deps

	e.Comments = CommentsFromData(d.Comments)

	return e
}

// IssuesFromData converts a slice of backend.IssueData to []entity.Issue.
// Returns a non-nil empty slice for nil or empty input.
func IssuesFromData(ds []backend.IssueData) []entity.Issue {
	out := make([]entity.Issue, 0, len(ds))
	for i := range ds {
		out = append(out, IssueFromData(ds[i]))
	}
	return out
}

// IssueToData converts entity.Issue to a slim backend.IssueData wire type.
// Content fields (Description, Design, etc.), relational data (Dependencies,
// Comments), and all entity-only fields (HOP, gate, bonding, tombstone, messaging,
// context markers, slot, source tracing, agent identity, molecule type, work type,
// event fields) are silently dropped.
// Parent is left empty (entity.Issue has no Parent field).
// DependencyCount is set to len(e.Dependencies). DependentCount is set to 0.
func IssueToData(e entity.Issue) backend.IssueData {
	labels := e.Labels
	if labels == nil {
		labels = []string{}
	}
	return backend.IssueData{
		ID:               e.ID,
		Title:            e.Title,
		Status:           string(e.Status),
		Priority:         e.Priority,
		IssueType:        string(e.IssueType),
		Assignee:         e.Assignee,
		Owner:            e.Owner,
		Labels:           labels,
		SourceRepo:       e.SourceRepo,
		DesignArtifactID: e.DesignArtifactID,
		HasDesign:        e.HasDesign || e.Design != "",
		CreatedAt:        e.CreatedAt,
		UpdatedAt:        e.UpdatedAt,
		DueAt:            e.DueAt,
		DeferUntil:       e.DeferUntil,
		DependencyCount:  len(e.Dependencies),
		DependentCount:   0,
	}
}

// IssuesToData converts a slice of entity.Issue to []backend.IssueData.
// Returns a non-nil empty slice for nil or empty input.
func IssuesToData(es []entity.Issue) []backend.IssueData {
	out := make([]backend.IssueData, 0, len(es))
	for i := range es {
		out = append(out, IssueToData(es[i]))
	}
	return out
}

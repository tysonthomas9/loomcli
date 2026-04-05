package fleet

import (
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/entity"
	"github.com/tysonthomas9/loomcli/internal/types"
)

// IssueToEntity converts a types.Issue to an entity.Issue with full field fidelity.
// Unlike the issueToData path (types → backend.IssueData), this preserves all ~50
// entity fields including HOP, gate, bonding, agent identity, molecule, and tombstone.
// Nil input returns a zero-value entity.Issue with normalized empty slices.
func IssueToEntity(issue *types.Issue) entity.Issue {
	if issue == nil {
		return entity.Issue{Labels: []string{}}
	}
	e := issueCoreToEntity(issue)
	issuePopulateRelational(&e, issue)
	issuePopulateExtended(&e, issue)
	return e
}

// issueCoreToEntity maps core, content, status, assignment, timestamp,
// scheduling, external, tombstone, and messaging fields.
func issueCoreToEntity(issue *types.Issue) entity.Issue {
	return entity.Issue{
		ID:                 issue.ID,
		Title:              issue.Title,
		Description:        issue.Description,
		Design:             issue.Design,
		AcceptanceCriteria: issue.AcceptanceCriteria,
		Notes:              issue.Notes,
		Status:             entity.IssueStatus(issue.Status),
		Priority:           issue.Priority,
		IssueType:          entity.IssueType(issue.IssueType),
		Assignee:           issue.Assignee,
		Owner:              issue.Owner,
		EstimatedMinutes:   issue.EstimatedMinutes,
		CreatedAt:          issue.CreatedAt,
		CreatedBy:          issue.CreatedBy,
		UpdatedAt:          issue.UpdatedAt,
		ClosedAt:           issue.ClosedAt,
		CloseReason:        issue.CloseReason,
		ClosedBySession:    issue.ClosedBySession,
		DueAt:              issue.DueAt,
		DeferUntil:         issue.DeferUntil,
		ExternalRef:        issue.ExternalRef,
		SourceSystem:       issue.SourceSystem,
		SourceRepo:         issue.SourceRepo,
		DeletedAt:          issue.DeletedAt,
		DeletedBy:          issue.DeletedBy,
		DeleteReason:       issue.DeleteReason,
		OriginalType:       issue.OriginalType,
		Sender:             issue.Sender,
		Ephemeral:          issue.Ephemeral,
		Pinned:             issue.Pinned,
		IsTemplate:         issue.IsTemplate,
	}
}

// issuePopulateRelational fills labels, dependencies, and comments.
func issuePopulateRelational(e *entity.Issue, issue *types.Issue) {
	e.Labels = make([]string, 0)
	if len(issue.Labels) > 0 {
		e.Labels = issue.Labels
	}
	if issue.Dependencies != nil {
		e.Dependencies = DependenciesToEntities(issue.Dependencies)
	} else {
		e.Dependencies = make([]*entity.Dependency, 0)
	}
	if issue.Comments != nil {
		e.Comments = CommentsToEntities(issue.Comments)
	} else {
		e.Comments = make([]*entity.Comment, 0)
	}
}

// issuePopulateExtended fills bonding, HOP, gate, slot, source tracing,
// agent identity, molecule, work type, and event fields.
func issuePopulateExtended(e *entity.Issue, issue *types.Issue) {
	e.BondedFrom = bondRefsToEntities(issue.BondedFrom)
	e.Creator = entityRefToEntity(issue.Creator)
	e.Validations = validationsToEntities(issue.Validations)
	e.QualityScore = issue.QualityScore
	e.Crystallizes = issue.Crystallizes
	e.AwaitType = issue.AwaitType
	e.AwaitID = issue.AwaitID
	e.Timeout = issue.Timeout
	e.Waiters = issue.Waiters
	e.Holder = issue.Holder
	e.SourceFormula = issue.SourceFormula
	e.SourceLocation = issue.SourceLocation
	e.HookBead = issue.HookBead
	e.RoleBead = issue.RoleBead
	e.AgentState = entity.AgentState(issue.AgentState)
	e.LastActivity = issue.LastActivity
	e.RoleType = issue.RoleType
	e.Rig = issue.Rig
	e.MolType = entity.MolType(issue.MolType)
	e.WorkType = entity.WorkType(issue.WorkType)
	e.EventKind = issue.EventKind
	e.Actor = issue.Actor
	e.Target = issue.Target
	e.Payload = issue.Payload
}

// IssuesToEntities converts a slice of *types.Issue to []entity.Issue.
// Nil entries are filtered out. Nil or empty input returns a non-nil empty slice.
func IssuesToEntities(issues []*types.Issue) []entity.Issue {
	result := make([]entity.Issue, 0, len(issues))
	for _, issue := range issues {
		if issue != nil {
			result = append(result, IssueToEntity(issue))
		}
	}
	return result
}

// DetailsToEntity converts types.IssueDetails to entity.Issue.
// Populates relational data (labels, dependencies, dependents, comments, parent)
// from the details structure. Nil input returns a zero-value entity.Issue.
func DetailsToEntity(details *types.IssueDetails) entity.Issue {
	if details == nil {
		return entity.Issue{
			Labels: []string{},
		}
	}

	e := IssueToEntity(&details.Issue)

	// Override labels from details (details.Labels is the authoritative source).
	labels := make([]string, 0)
	if len(details.Labels) > 0 {
		labels = details.Labels
	}
	e.Labels = labels

	// Combine dependencies and dependents into a single Dependencies slice.
	deps := dependencyMetasToEntities(details.Issue.ID, details.Dependencies)
	dependents := make([]*entity.Dependency, 0, len(details.Dependents))
	for _, iwdm := range details.Dependents {
		if iwdm == nil {
			continue
		}
		dependents = append(dependents, &entity.Dependency{
			IssueID:     iwdm.Issue.ID,
			DependsOnID: details.Issue.ID,
			Type:        entity.DependencyType(iwdm.DependencyType),
			CreatedAt:   iwdm.Issue.CreatedAt,
			CreatedBy:   iwdm.Issue.CreatedBy,
		})
	}
	combined := make([]*entity.Dependency, 0, len(deps)+len(dependents))
	combined = append(combined, deps...)
	combined = append(combined, dependents...)
	e.Dependencies = combined

	// Comments.
	e.Comments = CommentsToEntities(details.Comments)

	// Parent.
	if details.Parent != nil {
		// entity.Issue has no Parent field; parent is tracked via parent-child
		// dependencies. The parent ID is available in the Dependencies slice
		// for callers that need it.
		_ = details.Parent
	}

	return e
}

// DependencyToEntity converts a types.Dependency to an entity.Dependency.
// Preserves all fields including Metadata and ThreadID (which the backend.*
// path drops). Returns nil for nil input.
func DependencyToEntity(dep *types.Dependency) *entity.Dependency {
	if dep == nil {
		return nil
	}
	return &entity.Dependency{
		IssueID:     dep.IssueID,
		DependsOnID: dep.DependsOnID,
		Type:        entity.DependencyType(dep.Type),
		CreatedAt:   dep.CreatedAt,
		CreatedBy:   dep.CreatedBy,
		Metadata:    dep.Metadata,
		ThreadID:    dep.ThreadID,
	}
}

// DependenciesToEntities converts a slice of *types.Dependency to []*entity.Dependency.
// Nil entries are filtered. Nil or empty input returns a non-nil empty slice.
func DependenciesToEntities(deps []*types.Dependency) []*entity.Dependency {
	result := make([]*entity.Dependency, 0, len(deps))
	for _, dep := range deps {
		if dep != nil {
			result = append(result, DependencyToEntity(dep))
		}
	}
	return result
}

// dependencyMetaToEntity converts an IssueWithDependencyMetadata to an entity.Dependency.
// parentID is the owning issue's ID — used as IssueID (this issue depends on the iwdm issue).
func dependencyMetaToEntity(parentID string, iwdm *types.IssueWithDependencyMetadata) *entity.Dependency {
	if iwdm == nil {
		return nil
	}
	return &entity.Dependency{
		IssueID:     parentID,
		DependsOnID: iwdm.Issue.ID,
		Type:        entity.DependencyType(iwdm.DependencyType),
		CreatedAt:   iwdm.Issue.CreatedAt,
		CreatedBy:   iwdm.Issue.CreatedBy,
	}
}

// dependencyMetasToEntities converts a slice of IssueWithDependencyMetadata.
func dependencyMetasToEntities(parentID string, iwdms []*types.IssueWithDependencyMetadata) []*entity.Dependency {
	result := make([]*entity.Dependency, 0, len(iwdms))
	for _, iwdm := range iwdms {
		if iwdm != nil {
			result = append(result, dependencyMetaToEntity(parentID, iwdm))
		}
	}
	return result
}

// CommentToEntity converts a types.Comment to an entity.Comment.
// Returns nil for nil input.
func CommentToEntity(c *types.Comment) *entity.Comment {
	if c == nil {
		return nil
	}
	return &entity.Comment{
		ID:        c.ID,
		IssueID:   c.IssueID,
		Author:    c.Author,
		Text:      c.Text,
		CreatedAt: c.CreatedAt,
	}
}

// CommentsToEntities converts a slice of *types.Comment to []*entity.Comment.
// Nil entries are filtered. Nil or empty input returns a non-nil empty slice.
func CommentsToEntities(cs []*types.Comment) []*entity.Comment {
	result := make([]*entity.Comment, 0, len(cs))
	for _, c := range cs {
		if c != nil {
			result = append(result, CommentToEntity(c))
		}
	}
	return result
}

// entityRefToEntity converts types.EntityRef to entity.EntityRef.
// Nil input returns nil.
func entityRefToEntity(ref *types.EntityRef) *entity.EntityRef {
	if ref == nil {
		return nil
	}
	return &entity.EntityRef{
		Name:     ref.Name,
		Platform: ref.Platform,
		Org:      ref.Org,
		ID:       ref.ID,
	}
}

// validationsToEntities converts []types.Validation to []entity.Validation.
func validationsToEntities(vs []types.Validation) []entity.Validation {
	if vs == nil {
		return nil
	}
	result := make([]entity.Validation, len(vs))
	for i, v := range vs {
		result[i] = entity.Validation{
			Validator: entityRefToEntity(v.Validator),
			Outcome:   v.Outcome,
			Timestamp: v.Timestamp,
			Score:     v.Score,
		}
	}
	return result
}

// bondRefsToEntities converts []types.BondRef to []entity.BondRef.
func bondRefsToEntities(refs []types.BondRef) []entity.BondRef {
	if refs == nil {
		return nil
	}
	result := make([]entity.BondRef, len(refs))
	for i, ref := range refs {
		result[i] = entity.BondRef{
			SourceID:  ref.SourceID,
			BondType:  ref.BondType,
			BondPoint: ref.BondPoint,
		}
	}
	return result
}

// ClaimResultToEntity converts a ClaimResult to an entity.Issue.
// Returns error if the payload or payload issue is nil (protocol error).
// Applies PriorityOverride and Deadline from the payload.
func ClaimResultToEntity(cr *ClaimResult) (*entity.Issue, error) {
	if cr == nil || cr.Payload == nil {
		return nil, fmt.Errorf("fleet: claim result has no payload")
	}
	if cr.Payload.Issue == nil {
		return nil, fmt.Errorf("fleet: claim payload has no issue")
	}

	e := IssueToEntity(cr.Payload.Issue)

	// Override labels from payload.
	labels := make([]string, 0)
	if len(cr.Payload.Labels) > 0 {
		labels = cr.Payload.Labels
	}
	e.Labels = labels

	// Convert dependencies from payload.
	e.Dependencies = DependenciesToEntities(cr.Payload.Dependencies)

	// Apply priority override.
	if cr.Payload.PriorityOverride != nil {
		e.Priority = *cr.Payload.PriorityOverride
	}

	// Apply deadline as DeferUntil (scheduling constraint on the entity).
	if cr.Payload.Deadline != nil {
		e.DeferUntil = cr.Payload.Deadline
	}

	return &e, nil
}

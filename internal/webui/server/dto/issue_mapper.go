package dto

import "github.com/tysonthomas9/loomcli/internal/entity"

// IssueMapOption is a functional option for enriching IssueFromEntity output.
type IssueMapOption func(*issueMapState)

type issueMapState struct {
	labels       []string
	dependencies []DependencyRef
	dependents   []DependencyRef
	comments     []CommentResponse
	parent       *string
	parentTitle  *string
	depCount     int
	depCountSet  bool
	deptCount    int
	deptCountSet bool
}

// WithLabels sets the labels slice on the mapped IssueResponse.
// Nil input results in an empty slice (not nil).
func WithLabels(labels []string) IssueMapOption {
	return func(s *issueMapState) {
		s.labels = labels
	}
}

// WithDependencies sets pre-mapped dependency refs for the detail view.
func WithDependencies(deps []DependencyRef) IssueMapOption {
	return func(s *issueMapState) {
		s.dependencies = deps
	}
}

// WithDependents sets pre-mapped dependent refs for the detail view.
func WithDependents(deps []DependencyRef) IssueMapOption {
	return func(s *issueMapState) {
		s.dependents = deps
	}
}

// WithComments sets pre-mapped comments for the detail view.
func WithComments(comments []CommentResponse) IssueMapOption {
	return func(s *issueMapState) {
		s.comments = comments
	}
}

// WithParent sets the parent issue ID and title.
// If id is empty, both parent and parentTitle are left nil.
func WithParent(id, title string) IssueMapOption {
	return func(s *issueMapState) {
		if id != "" {
			idCopy := id
			titleCopy := title
			s.parent = &idCopy
			s.parentTitle = &titleCopy
		}
	}
}

// WithCounts sets explicit dependency and dependent counts for the list view.
// Without this option, counts default to len(Dependencies) and len(Dependents).
func WithCounts(depCount, deptCount int) IssueMapOption {
	return func(s *issueMapState) {
		s.depCount = depCount
		s.depCountSet = true
		s.deptCount = deptCount
		s.deptCountSet = true
	}
}

// IssueFromEntity maps an entity.Issue to an IssueResponse DTO.
// Returns a zero-value IssueResponse with empty slices if issue is nil.
func IssueFromEntity(issue *entity.Issue, opts ...IssueMapOption) IssueResponse {
	resp := applyIssueOptions(opts)

	if issue == nil {
		return resp
	}

	resp.ID = issue.ID
	resp.Title = issue.Title
	resp.Description = issue.Description
	resp.Design = issue.Design
	resp.DesignArtifactID = issue.DesignArtifactID
	resp.DesignFormat = issue.DesignFormat
	resp.HasDesign = issue.HasDesign || issue.Design != ""
	resp.AcceptanceCriteria = issue.AcceptanceCriteria
	resp.Notes = issue.Notes
	resp.Status = string(issue.Status)
	resp.Priority = issue.Priority
	resp.IssueType = string(issue.IssueType)
	resp.Assignee = issue.Assignee
	resp.Owner = issue.Owner
	resp.EstimatedMinutes = issue.EstimatedMinutes
	resp.CreatedAt = issue.CreatedAt
	resp.UpdatedAt = issue.UpdatedAt
	resp.ClosedAt = issue.ClosedAt
	resp.CloseReason = issue.CloseReason
	resp.DueAt = issue.DueAt
	resp.DeferUntil = issue.DeferUntil
	resp.ExternalRef = issue.ExternalRef
	resp.SourceRepo = issue.SourceRepo
	resp.Pinned = issue.Pinned

	return resp
}

// applyIssueOptions builds an IssueResponse with enrichment data from options.
// All slice fields are initialized to empty slices (never nil).
func applyIssueOptions(opts []IssueMapOption) IssueResponse {
	var state issueMapState
	for _, opt := range opts {
		opt(&state)
	}

	resp := IssueResponse{
		Labels:       emptyStringSlice(state.labels),
		Dependencies: emptyDepSlice(state.dependencies),
		Dependents:   emptyDepSlice(state.dependents),
		Comments:     emptyCommentSlice(state.comments),
		Parent:       state.parent,
		ParentTitle:  state.parentTitle,
	}

	if state.depCountSet {
		resp.DependencyCount = state.depCount
	} else {
		resp.DependencyCount = len(resp.Dependencies)
	}
	if state.deptCountSet {
		resp.DependentCount = state.deptCount
	} else {
		resp.DependentCount = len(resp.Dependents)
	}

	return resp
}

// DependencyRefFromEntity maps an entity.Dependency and its related issue into a DependencyRef.
// If dep is nil, returns a zero-value DependencyRef.
// If relatedIssue is nil, only Type is populated from dep.
func DependencyRefFromEntity(dep *entity.Dependency, relatedIssue *entity.Issue) DependencyRef {
	if dep == nil {
		return DependencyRef{}
	}
	ref := DependencyRef{
		Type: string(dep.Type),
	}
	if relatedIssue != nil {
		ref.ID = relatedIssue.ID
		ref.Title = relatedIssue.Title
		ref.Status = string(relatedIssue.Status)
		ref.Priority = relatedIssue.Priority
		ref.IssueType = string(relatedIssue.IssueType)
	}
	return ref
}

// CommentFromEntity maps an entity.Comment to a CommentResponse.
// If c is nil, returns a zero-value CommentResponse.
func CommentFromEntity(c *entity.Comment) CommentResponse {
	if c == nil {
		return CommentResponse{}
	}
	return CommentResponse{
		ID:        c.ID,
		Author:    c.Author,
		Text:      c.Text,
		CreatedAt: c.CreatedAt,
		ParentID:  c.ParentID,
		EditedAt:  c.EditedAt,
	}
}

// CommentsFromEntities maps a slice of entity comments, filtering out nil entries
// and soft-deleted comments (DeletedAt != nil). Returns an empty slice (not nil)
// when input is nil or all entries are filtered.
func CommentsFromEntities(comments []*entity.Comment) []CommentResponse {
	result := []CommentResponse{}
	for _, c := range comments {
		if c == nil || c.DeletedAt != nil {
			continue
		}
		result = append(result, CommentFromEntity(c))
	}
	return result
}

// IssuesFromEntities maps a slice of issues using the same options for each.
// Nil entries are filtered out. Returns an empty slice (not nil) when input
// is nil or empty. For per-issue options, use IssueFromEntity in a loop instead.
func IssuesFromEntities(issues []*entity.Issue, opts ...IssueMapOption) []IssueResponse {
	result := []IssueResponse{}
	for _, issue := range issues {
		if issue == nil {
			continue
		}
		result = append(result, IssueFromEntity(issue, opts...))
	}
	return result
}

// emptyStringSlice returns the input if non-nil, otherwise an empty slice.
func emptyStringSlice(s []string) []string {
	if s != nil {
		return s
	}
	return []string{}
}

// emptyDepSlice returns the input if non-nil, otherwise an empty slice.
func emptyDepSlice(s []DependencyRef) []DependencyRef {
	if s != nil {
		return s
	}
	return []DependencyRef{}
}

// emptyCommentSlice returns the input if non-nil, otherwise an empty slice.
func emptyCommentSlice(s []CommentResponse) []CommentResponse {
	if s != nil {
		return s
	}
	return []CommentResponse{}
}

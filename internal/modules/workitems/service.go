package workitems

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
)

const (
	maxCommentBytes = 64 * 1024
)

// Service owns Work Items lifecycle, availability, comment, and dependency
// policy over a narrow durable port.
type Service struct {
	store           Store
	labelMutationMu sync.Mutex
}

var _ API = (*Service)(nil)
var _ StatsQueries = (*Service)(nil)
var _ BlockedQueries = (*Service)(nil)

func New(store Store) (*Service, error) {
	if store == nil {
		return nil, fmt.Errorf("compose Work Items: durable port is required: %w", ErrUnavailable)
	}
	return &Service{store: store}, nil
}

func (s *Service) Create(ctx context.Context, command CreateCommand) (*CreatedIssue, error) {
	command.Title = CanonicalTitle(command.Title)
	if err := validateCreate(command); err != nil {
		return nil, err
	}
	command.Labels = append([]string(nil), command.Labels...)
	command.Dependencies = append([]string(nil), command.Dependencies...)
	needsAdmission := createNeedsRepositoryAdmission(command)
	if needsAdmission {
		if err := s.store.RequireRepositoryAdmission(ctx); err != nil {
			return nil, err
		}
	}
	created, err := s.store.Create(ctx, command)
	if err != nil {
		return nil, err
	}
	if created == nil || strings.TrimSpace(created.ID) == "" {
		return nil, fmt.Errorf("create returned an invalid issue: %w", ErrInvalidPersistedState)
	}
	canonical := cloneIssueSummary(*created)
	if needsAdmission {
		admission, err := s.blockRepositoryRequired(ctx, created.ID)
		if err != nil {
			return nil, err
		}
		if admission == nil || admission.Issue == nil || admission.Issue.ID != created.ID {
			return nil, fmt.Errorf("repository admission for %q returned an invalid issue: %w", created.ID, ErrInvalidPersistedState)
		}
		canonical = cloneIssueSummary(*admission.Issue)
	}
	detail, getErr := s.store.Get(ctx, GetQuery{IssueID: created.ID})
	if getErr == nil && detail != nil && detail.ID == created.ID {
		mergeCanonicalSummary(detail, canonical)
		return &CreatedIssue{Detail: cloneIssueDetail(detail)}, nil
	}
	return &CreatedIssue{Summary: &canonical}, nil
}

//nolint:funlen // Kanban projection joins ready, blocked, deferred, and canonical issue state in one read workflow.
func (s *Service) List(ctx context.Context, query ListQuery) (*ListResult, error) {
	if query.Filter.Limit < 0 {
		return nil, fmt.Errorf("limit must be non-negative: %w", ErrInvalid)
	}
	query.Filter.Labels = append([]string(nil), query.Filter.Labels...)
	query.Filter.LabelsAny = append([]string(nil), query.Filter.LabelsAny...)
	query.Filter.SourceRepos = append([]string(nil), query.Filter.SourceRepos...)
	query.ExcludeStatus = append([]string(nil), query.ExcludeStatus...)
	issues, err := s.store.List(ctx, query.Filter)
	if err != nil {
		return nil, err
	}
	issues, err = validIssueSummaries(issues)
	if err != nil {
		return nil, err
	}
	issues = excludeIssuesByStatus(issues, query.ExcludeStatus)
	if !query.IncludeBlocked {
		out := make([]ListItem, len(issues))
		for index := range issues {
			out[index] = ListItem{IssueSummary: cloneIssueSummary(issues[index])}
		}
		return &ListResult{Issues: out}, nil
	}
	availability := availabilityFromFilter(query.Filter)
	blocked, err := s.store.Blocked(ctx, availability)
	if err != nil {
		return nil, err
	}
	ready, err := s.store.Ready(ctx, availability)
	if err != nil {
		return nil, err
	}
	deferred, err := s.store.Deferred(ctx, availability)
	if err != nil {
		return nil, err
	}
	blocked, err = validIssueSummaries(blocked)
	if err != nil {
		return nil, err
	}
	ready, err = validIssueSummaries(ready)
	if err != nil {
		return nil, err
	}
	deferred, err = validIssueSummaries(deferred)
	if err != nil {
		return nil, err
	}
	blockedByID := summaryMap(blocked)
	readyByID := summaryIDSet(ready)
	deferredByID := summaryMap(deferred)
	issues = appendMissingSummaries(issues, blockedByID)
	issues = appendMissingSummaries(issues, deferredByID)
	out := make([]KanbanItem, len(issues))
	for index := range issues {
		issue := cloneIssueSummary(issues[index])
		blockedIssue, isBlocked := blockedByID[issue.ID]
		_, isDeferred := deferredByID[issue.ID]
		blockedBy := append([]string(nil), blockedIssue.BlockedBy...)
		blockedCount := blockedIssue.BlockedByCount
		deferredState := isDeferred || issue.Status == "deferred"
		if isBlocked && blockedCount == 0 {
			blockedCount = len(blockedBy)
		}
		out[index] = KanbanItem{
			IssueSummary: issue, IsBlocked: isBlocked,
			IsReady:        readyByID[issue.ID] && !isBlocked && !deferredState,
			IsDeferred:     deferredState,
			BlockedByCount: blockedCount, BlockedBy: blockedBy,
		}
	}
	return &ListResult{KanbanIssues: out}, nil
}

// Stats returns the owner projection without exposing the durable adapter.
func (s *Service) Stats(ctx context.Context) (*Stats, error) {
	stats, err := s.store.Stats(ctx)
	if err != nil || stats == nil {
		return stats, err
	}
	cloned := *stats
	return &cloned, nil
}

// Blocked returns the owner projection without exposing the durable adapter.
func (s *Service) Blocked(ctx context.Context, query AvailabilityQuery) ([]IssueSummary, error) {
	if query.Limit < 0 {
		return nil, fmt.Errorf("limit must be non-negative: %w", ErrInvalid)
	}
	query.Labels = append([]string(nil), query.Labels...)
	query.LabelsAny = append([]string(nil), query.LabelsAny...)
	query.SourceRepos = append([]string(nil), query.SourceRepos...)
	values, err := s.store.Blocked(ctx, query)
	if err != nil {
		return nil, err
	}
	return validIssueSummaries(values)
}

func (s *Service) Ready(ctx context.Context, query AvailabilityQuery) ([]IssueSummary, error) {
	if query.Limit < 0 {
		return nil, fmt.Errorf("limit must be non-negative: %w", ErrInvalid)
	}
	query.Labels = append([]string(nil), query.Labels...)
	query.LabelsAny = append([]string(nil), query.LabelsAny...)
	query.SourceRepos = append([]string(nil), query.SourceRepos...)
	values, err := s.store.Ready(ctx, query)
	if err != nil {
		return nil, err
	}
	return validIssueSummaries(values)
}

func validIssueSummaries(values []IssueSummary) ([]IssueSummary, error) {
	out := make([]IssueSummary, len(values))
	for index := range values {
		if strings.TrimSpace(values[index].ID) == "" {
			return nil, fmt.Errorf("list returned an issue without an id: %w", ErrInvalidPersistedState)
		}
		out[index] = cloneIssueSummary(values[index])
	}
	return out, nil
}

func excludeIssuesByStatus(values []IssueSummary, excluded []string) []IssueSummary {
	if len(excluded) == 0 {
		return values
	}
	set := make(map[string]bool, len(excluded))
	for _, status := range excluded {
		set[status] = true
	}
	out := values[:0]
	for _, value := range values {
		if !set[value.Status] {
			out = append(out, value)
		}
	}
	return out
}

func availabilityFromFilter(filter ListFilter) AvailabilityQuery {
	return AvailabilityQuery{
		ParentID: filter.ParentID, Assignee: filter.Assignee, Priority: filter.Priority,
		IssueType: filter.IssueType, Labels: append([]string(nil), filter.Labels...), LabelsAny: append([]string(nil), filter.LabelsAny...),
		SourceRepos: append([]string(nil), filter.SourceRepos...), Limit: filter.Limit,
	}
}

func summaryMap(values []IssueSummary) map[string]IssueSummary {
	out := make(map[string]IssueSummary, len(values))
	for _, value := range values {
		out[value.ID] = value
	}
	return out
}

func summaryIDSet(values []IssueSummary) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, value := range values {
		out[value.ID] = true
	}
	return out
}

func appendMissingSummaries(values []IssueSummary, byID map[string]IssueSummary) []IssueSummary {
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		seen[value.ID] = true
	}
	for id, value := range byID {
		if !seen[id] {
			values = append(values, value)
		}
	}
	return values
}

func (s *Service) blockRepositoryRequired(ctx context.Context, issueID string) (*RepositoryAdmissionResult, error) {
	result, err := s.store.BlockRepositoryRequired(ctx, issueID)
	if err == nil || ctx.Err() != nil || (!errors.Is(err, ErrUnavailable) && !errors.Is(err, ErrTimeout)) {
		return result, err
	}
	return s.store.BlockRepositoryRequired(ctx, issueID)
}

func validateCreate(command CreateCommand) error {
	if err := ValidateTitle(command.Title); err != nil {
		return err
	}
	if command.IssueType == "" {
		return fmt.Errorf("issue_type is required: %w", ErrInvalid)
	}
	if !IssueType(command.IssueType).IsBuiltIn() {
		return fmt.Errorf("invalid issue_type: %s (must be bug, feature, task, epic, or chore): %w", command.IssueType, ErrInvalid)
	}
	if command.Priority < 0 || command.Priority > 4 {
		return fmt.Errorf("priority must be between 0 and 4 (got %d): %w", command.Priority, ErrInvalid)
	}
	if !Status(command.Status).IsCreateStatus() {
		return fmt.Errorf("status must be open or deferred: %w", ErrInvalid)
	}
	if len(command.Labels) > MaxLabels {
		return fmt.Errorf("too many labels (max %d, got %d): %w", MaxLabels, len(command.Labels), ErrInvalid)
	}
	if len(command.Dependencies) > MaxDependencies {
		return fmt.Errorf("too many dependencies (max %d, got %d): %w", MaxDependencies, len(command.Dependencies), ErrInvalid)
	}
	return nil
}

func createNeedsRepositoryAdmission(command CreateCommand) bool {
	status := strings.ToLower(strings.TrimSpace(command.Status))
	return (status == "" || status == string(StatusOpen)) &&
		!strings.EqualFold(strings.TrimSpace(command.IssueType), string(TypeEpic)) &&
		strings.TrimSpace(command.SourceRepo) == ""
}

func (s *Service) Search(ctx context.Context, query SearchQuery) ([]IssueSummary, error) {
	query.Query = strings.TrimSpace(query.Query)
	if query.Query == "" {
		return nil, fmt.Errorf("search query is required: %w", ErrInvalid)
	}
	if query.Limit < 0 {
		return nil, fmt.Errorf("limit must be non-negative: %w", ErrInvalid)
	}
	values, err := s.store.Search(ctx, query)
	if err != nil {
		return nil, err
	}
	out := make([]IssueSummary, len(values))
	for index := range values {
		if strings.TrimSpace(values[index].ID) == "" {
			return nil, fmt.Errorf("search returned an issue without an id: %w", ErrInvalidPersistedState)
		}
		out[index] = cloneIssueSummary(values[index])
	}
	return out, nil
}

func (s *Service) Get(ctx context.Context, query GetQuery) (*IssueDetail, error) {
	var err error
	query.IssueID, err = required("issue id", query.IssueID)
	if err != nil {
		return nil, err
	}
	value, err := s.store.Get(ctx, query)
	if err != nil {
		return nil, err
	}
	if value == nil || strings.TrimSpace(value.ID) != query.IssueID {
		return nil, fmt.Errorf("get %q returned an invalid issue: %w", query.IssueID, ErrInvalidPersistedState)
	}
	return cloneIssueDetail(value), nil
}

func (s *Service) Patch(ctx context.Context, command PatchCommand) (*IssueDetail, error) {
	var err error
	command.IssueID, err = required("issue id", command.IssueID)
	if err != nil {
		return nil, err
	}
	if patchHasLabelMutation(command) {
		s.labelMutationMu.Lock()
		defer s.labelMutationMu.Unlock()
	}
	if err := s.store.Patch(ctx, clonePatchCommand(command)); err != nil {
		return nil, err
	}
	return s.Get(ctx, GetQuery{IssueID: command.IssueID})
}

func (s *Service) Close(ctx context.Context, command CloseCommand) (*CloseResult, error) {
	var err error
	command.IssueID, err = required("issue id", command.IssueID)
	if err != nil {
		return nil, err
	}
	result, err := s.store.Close(ctx, command)
	if err != nil {
		if errors.Is(err, ErrAlreadyClosed) {
			return &CloseResult{Closed: &IssueSummary{ID: command.IssueID, Status: "closed"}, Unblocked: []IssueSummary{}}, nil
		}
		return nil, err
	}
	if result == nil || result.Closed == nil || strings.TrimSpace(result.Closed.ID) != command.IssueID {
		return nil, fmt.Errorf("close %q returned an invalid result: %w", command.IssueID, ErrInvalidPersistedState)
	}
	return cloneCloseResult(result), nil
}

func (s *Service) AssignRepository(ctx context.Context, command AssignRepositoryCommand) (*IssueSummary, error) {
	var err error
	command.IssueID, err = required("issue id", command.IssueID)
	if err != nil {
		return nil, err
	}
	command.Repository, err = required("repository", command.Repository)
	if err != nil {
		return nil, err
	}
	value, err := s.store.AssignRepository(ctx, command)
	if err != nil {
		return nil, err
	}
	if value == nil || strings.TrimSpace(value.ID) != command.IssueID || strings.TrimSpace(value.SourceRepo) != command.Repository {
		return nil, fmt.Errorf("repository assignment for %q returned an invalid issue: %w", command.IssueID, ErrInvalidPersistedState)
	}
	copy := cloneIssueSummary(*value)
	return &copy, nil
}

func (s *Service) Claim(ctx context.Context, command ClaimCommand) (*IssueDetail, error) {
	var err error
	command.IssueID, err = required("issue id", command.IssueID)
	if err != nil {
		return nil, err
	}
	value, err := s.store.Claim(ctx, command)
	if err != nil {
		return nil, err
	}
	if value == nil || strings.TrimSpace(value.ID) != command.IssueID || value.Status != "in_progress" {
		return nil, fmt.Errorf("claim %q returned an invalid issue: %w", command.IssueID, ErrInvalidPersistedState)
	}
	return cloneIssueDetail(value), nil
}

func (s *Service) Reopen(ctx context.Context, command ReopenCommand) error {
	var err error
	command.IssueID, err = required("issue id", command.IssueID)
	if err != nil {
		return err
	}
	if err := s.store.Reopen(ctx, command); err != nil {
		return err
	}
	return nil
}

func (s *Service) Delete(ctx context.Context, command DeleteCommand) (DeleteResult, error) {
	var err error
	command.IssueID, err = required("issue id", command.IssueID)
	if err != nil {
		return DeleteResult{}, err
	}
	result, err := s.store.Delete(ctx, command)
	if err != nil {
		return DeleteResult{}, err
	}
	if result.DeletedCount != 1 || len(result.DeletedIDs) != 1 || result.DeletedIDs[0] != command.IssueID {
		return DeleteResult{}, fmt.Errorf("delete %q returned an invalid result: %w", command.IssueID, ErrInvalidPersistedState)
	}
	result.DeletedIDs = append([]string(nil), result.DeletedIDs...)
	return result, nil
}

func (s *Service) ListEvents(ctx context.Context, query ListEventsQuery) ([]*Event, error) {
	var err error
	query.IssueID, err = required("issue id", query.IssueID)
	if err != nil {
		return nil, err
	}
	if query.Limit < 0 {
		return nil, fmt.Errorf("event limit must be non-negative: %w", ErrInvalid)
	}
	values, err := s.store.ListEvents(ctx, query)
	if err != nil {
		return nil, err
	}
	out := make([]*Event, 0, len(values))
	for _, value := range values {
		if value == nil {
			return nil, fmt.Errorf("list events for %q returned a nil event: %w", query.IssueID, ErrInvalidPersistedState)
		}
		copy := *value
		out = append(out, &copy)
	}
	return out, nil
}

func (s *Service) AddComment(ctx context.Context, command AddCommentCommand) (*Comment, error) {
	var err error
	command.IssueID, err = required("issue id", command.IssueID)
	if err != nil {
		return nil, err
	}
	command.Text = strings.TrimSpace(command.Text)
	if command.Text == "" {
		return nil, fmt.Errorf("comment text is required: %w", ErrInvalid)
	}
	if len(command.Text) > maxCommentBytes {
		return nil, fmt.Errorf("comment text too long (%d bytes, max %d): %w", len(command.Text), maxCommentBytes, ErrInvalid)
	}
	command.Author = strings.TrimSpace(command.Author)
	if command.Author == "" {
		command.Author = "web-ui"
	}
	comment, err := s.store.AddComment(ctx, command)
	if err != nil {
		return nil, err
	}
	if comment == nil || strings.TrimSpace(comment.IssueID) != command.IssueID || strings.TrimSpace(comment.Text) == "" {
		return nil, fmt.Errorf("add comment to %q returned an invalid comment: %w", command.IssueID, ErrInvalidPersistedState)
	}
	value := *comment
	return &value, nil
}

func (s *Service) ListComments(ctx context.Context, query ListCommentsQuery) ([]*Comment, error) {
	var err error
	query.IssueID, err = required("issue id", query.IssueID)
	if err != nil {
		return nil, err
	}
	comments, err := s.store.ListComments(ctx, query)
	if err != nil {
		return nil, err
	}
	out := make([]*Comment, 0, len(comments))
	for _, comment := range comments {
		if comment == nil || strings.TrimSpace(comment.IssueID) != query.IssueID {
			return nil, fmt.Errorf("list comments for %q returned an invalid comment: %w", query.IssueID, ErrInvalidPersistedState)
		}
		value := *comment
		out = append(out, &value)
	}
	return out, nil
}

func (s *Service) AddDependency(ctx context.Context, command AddDependencyCommand) error {
	var err error
	command.IssueID, err = required("issue id", command.IssueID)
	if err != nil {
		return err
	}
	command.DependsOnID, err = required("depends_on_id", command.DependsOnID)
	if err != nil {
		return err
	}
	if command.IssueID == command.DependsOnID {
		return fmt.Errorf("cannot add self-dependency: %w", ErrInvalid)
	}
	command.Type = strings.TrimSpace(command.Type)
	if command.Type == "" {
		command.Type = "blocks"
	}
	if err := s.store.AddDependency(ctx, command); err != nil {
		return err
	}
	return nil
}

func (s *Service) RemoveDependency(ctx context.Context, command RemoveDependencyCommand) error {
	var err error
	command.IssueID, err = required("issue id", command.IssueID)
	if err != nil {
		return err
	}
	command.DependsOnID, err = required("dependency id", command.DependsOnID)
	if err != nil {
		return err
	}
	command.Type = strings.TrimSpace(command.Type)
	if command.Type == "" {
		command.Type = "blocks"
	}
	if err := s.store.RemoveDependency(ctx, command); err != nil {
		return err
	}
	return nil
}

func (s *Service) ListDependencies(ctx context.Context, query ListDependenciesQuery) ([]Dependency, error) {
	var err error
	query.IssueID, err = required("issue id", query.IssueID)
	if err != nil {
		return nil, err
	}
	dependencies, err := s.store.ListDependencies(ctx, query)
	if err != nil {
		return nil, err
	}
	return append([]Dependency(nil), dependencies...), nil
}

func required(name, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%s is required: %w", name, ErrInvalid)
	}
	return value, nil
}

func cloneIssueSummary(value IssueSummary) IssueSummary {
	value.Labels = append([]string(nil), value.Labels...)
	value.BlockedBy = append([]string(nil), value.BlockedBy...)
	if value.DueAt != nil {
		copy := *value.DueAt
		value.DueAt = &copy
	}
	if value.DeferUntil != nil {
		copy := *value.DeferUntil
		value.DeferUntil = &copy
	}
	if value.ClosedAt != nil {
		copy := *value.ClosedAt
		value.ClosedAt = &copy
	}
	return value
}

func patchHasLabelMutation(command PatchCommand) bool {
	return len(command.AddLabels) > 0 || len(command.RemoveLabels) > 0 || len(command.SetLabels) > 0
}

func clonePatchCommand(command PatchCommand) PatchCommand {
	command.AddLabels = append([]string(nil), command.AddLabels...)
	command.RemoveLabels = append([]string(nil), command.RemoveLabels...)
	command.SetLabels = append([]string(nil), command.SetLabels...)
	return command
}

func cloneCloseResult(value *CloseResult) *CloseResult {
	if value == nil {
		return nil
	}
	out := &CloseResult{Unblocked: make([]IssueSummary, len(value.Unblocked))}
	if value.Closed != nil {
		closed := cloneIssueSummary(*value.Closed)
		out.Closed = &closed
	}
	for index := range value.Unblocked {
		out.Unblocked[index] = cloneIssueSummary(value.Unblocked[index])
	}
	return out
}

func cloneIssueDetail(value *IssueDetail) *IssueDetail {
	if value == nil {
		return nil
	}
	copy := *value
	copy.Labels = append([]string(nil), value.Labels...)
	copy.Dependencies = append([]Dependency(nil), value.Dependencies...)
	copy.Dependents = append([]Dependency(nil), value.Dependents...)
	copy.Comments = make([]*Comment, 0, len(value.Comments))
	for _, comment := range value.Comments {
		if comment == nil {
			copy.Comments = append(copy.Comments, nil)
			continue
		}
		value := *comment
		copy.Comments = append(copy.Comments, &value)
	}
	if value.ClosedAt != nil {
		closedAt := *value.ClosedAt
		copy.ClosedAt = &closedAt
	}
	if value.EstimatedMinutes != nil {
		estimate := *value.EstimatedMinutes
		copy.EstimatedMinutes = &estimate
	}
	if value.DueAt != nil {
		dueAt := *value.DueAt
		copy.DueAt = &dueAt
	}
	if value.DeferUntil != nil {
		deferUntil := *value.DeferUntil
		copy.DeferUntil = &deferUntil
	}
	return &copy
}

func mergeCanonicalSummary(detail *IssueDetail, summary IssueSummary) {
	if detail == nil {
		return
	}
	detail.ID = summary.ID
	detail.Title = summary.Title
	detail.Status = summary.Status
	detail.Priority = summary.Priority
	detail.IssueType = summary.IssueType
	detail.Assignee = summary.Assignee
	detail.Owner = summary.Owner
	detail.Labels = append([]string(nil), summary.Labels...)
	detail.SourceRepo = summary.SourceRepo
	detail.Repo = summary.Repo
	detail.Parent = summary.Parent
	detail.Design = summary.Design
	detail.DesignArtifactID = summary.DesignArtifactID
	detail.DesignFormat = summary.DesignFormat
	detail.HasDesign = summary.HasDesign
	detail.Notes = summary.Notes
	detail.CreatedBy = summary.CreatedBy
	detail.CreatedAt = summary.CreatedAt
	detail.UpdatedAt = summary.UpdatedAt
	detail.ClosedAt = summary.ClosedAt
	detail.CloseReason = summary.CloseReason
	detail.ExternalRef = summary.ExternalRef
	detail.DueAt = summary.DueAt
	detail.DeferUntil = summary.DeferUntil
}

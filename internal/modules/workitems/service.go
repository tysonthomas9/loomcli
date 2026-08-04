package workitems

import (
	"context"
	"fmt"
	"strings"
)

const maxCommentBytes = 64 * 1024

// Service owns comment and dependency policy over a narrow durable port.
type Service struct {
	store Store
}

var _ API = (*Service)(nil)

func New(store Store) (*Service, error) {
	if store == nil {
		return nil, fmt.Errorf("compose Work Items: durable port is required: %w", ErrUnavailable)
	}
	return &Service{store: store}, nil
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
	if value.DueAt != nil {
		copy := *value.DueAt
		value.DueAt = &copy
	}
	if value.DeferUntil != nil {
		copy := *value.DeferUntil
		value.DeferUntil = &copy
	}
	return value
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

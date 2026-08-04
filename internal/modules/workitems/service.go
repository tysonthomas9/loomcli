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

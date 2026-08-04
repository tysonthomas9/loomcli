package capabilitycomposition

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
)

const workItemOperationTimeout = 5 * time.Second

// NewWorkItems wraps the existing workspace-aware FleetDB backend provider in
// the narrow Work Items durable port. Persistence translation stays at
// composition; policy lives in the capability service.
func NewWorkItems(provider func(context.Context) backend.IssueBackend) (workitems.API, error) {
	if provider == nil {
		return nil, nil
	}
	return workitems.New(&workItemsBackendStore{provider: provider})
}

type workItemsBackendStore struct {
	provider func(context.Context) backend.IssueBackend
}

func (s *workItemsBackendStore) AddComment(ctx context.Context, command workitems.AddCommentCommand) (*workitems.Comment, error) {
	be, err := s.backend(ctx)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, workItemOperationTimeout)
	defer cancel()
	value, err := be.AddComment(ctx, backend.CommentAddParams{IssueID: command.IssueID, Author: command.Author, Text: command.Text})
	if err != nil {
		return nil, translateWorkItemsBackendError(err)
	}
	if value == nil {
		return nil, workitems.ErrInvalidPersistedState
	}
	return &workitems.Comment{ID: value.ID, IssueID: value.IssueID, Author: value.Author, Text: value.Text, CreatedAt: value.CreatedAt}, nil
}

func (s *workItemsBackendStore) ListComments(ctx context.Context, query workitems.ListCommentsQuery) ([]*workitems.Comment, error) {
	be, err := s.backend(ctx)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, workItemOperationTimeout)
	defer cancel()
	values, err := be.ListComments(ctx, query.IssueID)
	if err != nil {
		return nil, translateWorkItemsBackendError(err)
	}
	out := make([]*workitems.Comment, 0, len(values))
	for _, value := range values {
		out = append(out, &workitems.Comment{ID: value.ID, IssueID: value.IssueID, Author: value.Author, Text: value.Text, CreatedAt: value.CreatedAt})
	}
	return out, nil
}

func (s *workItemsBackendStore) AddDependency(ctx context.Context, command workitems.AddDependencyCommand) error {
	be, err := s.backend(ctx)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, workItemOperationTimeout)
	defer cancel()
	return translateWorkItemsBackendError(be.AddDependency(ctx, backend.DepAddParams{FromID: command.IssueID, ToID: command.DependsOnID, DepType: command.Type}))
}

func (s *workItemsBackendStore) RemoveDependency(ctx context.Context, command workitems.RemoveDependencyCommand) error {
	be, err := s.backend(ctx)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, workItemOperationTimeout)
	defer cancel()
	return translateWorkItemsBackendError(be.RemoveDependency(ctx, backend.DepRemoveParams{FromID: command.IssueID, ToID: command.DependsOnID, DepType: command.Type}))
}

func (s *workItemsBackendStore) ListDependencies(ctx context.Context, query workitems.ListDependenciesQuery) ([]workitems.Dependency, error) {
	be, err := s.backend(ctx)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, workItemOperationTimeout)
	defer cancel()
	detail, err := be.Get(ctx, query.IssueID)
	if err != nil {
		return nil, translateWorkItemsBackendError(err)
	}
	if detail == nil {
		return nil, workitems.ErrNotFound
	}
	out := make([]workitems.Dependency, 0, len(detail.Dependencies))
	for _, value := range detail.Dependencies {
		relatedID := value.DependsOnID
		if value.DependsOnID == query.IssueID && value.IssueID != "" {
			relatedID = value.IssueID
		}
		out = append(out, workitems.Dependency{
			ID: relatedID, Title: value.Title, Status: value.Status,
			Priority: value.Priority, CreatedAt: value.CreatedAt,
			DependencyType: value.Type, IssueType: value.IssueType,
			CreatedBy: value.CreatedBy,
		})
	}
	return out, nil
}

func (s *workItemsBackendStore) backend(ctx context.Context) (backend.IssueBackend, error) {
	if s == nil || s.provider == nil {
		return nil, workitems.ErrUnavailable
	}
	be := s.provider(ctx)
	if be == nil {
		return nil, workitems.ErrUnavailable
	}
	return be, nil
}

func translateWorkItemsBackendError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("work item backend timed out: %w", workitems.ErrTimeout)
	}
	var value *backend.BackendError
	if !errors.As(err, &value) {
		return err
	}
	switch value.Kind {
	case backend.KindNotFound:
		return fmt.Errorf("%s: %w", value.Message, workitems.ErrNotFound)
	case backend.KindValidation:
		return fmt.Errorf("%s: %w", value.Message, workitems.ErrInvalid)
	case backend.KindConflict:
		return fmt.Errorf("%s: %w", value.Message, workitems.ErrConflict)
	case backend.KindUnavailable:
		return fmt.Errorf("%s: %w", value.Message, workitems.ErrUnavailable)
	case backend.KindTimeout, backend.KindCanceled:
		return fmt.Errorf("%s: %w", value.Message, workitems.ErrTimeout)
	default:
		return err
	}
}

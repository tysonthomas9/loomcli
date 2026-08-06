package capabilitycomposition

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
)

const (
	workItemOperationTimeout = 5 * time.Second
	workItemCreateTimeout    = 30 * time.Second
)

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

func (s *workItemsBackendStore) RequireRepositoryAdmission(ctx context.Context) error {
	be, err := s.backend(ctx)
	if err != nil {
		return err
	}
	if _, ok := be.(backend.RepositoryRequirementBackend); !ok {
		return fmt.Errorf("issue backend does not support atomic repository-required admission: %w", workitems.ErrNotImplemented)
	}
	return nil
}

func (s *workItemsBackendStore) Create(ctx context.Context, command workitems.CreateCommand) (*workitems.IssueSummary, error) {
	be, err := s.backend(ctx)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, workItemCreateTimeout)
	defer cancel()
	value, err := be.Create(ctx, backend.CreateParams{
		ID: command.ID, Parent: command.Parent, Title: command.Title,
		Description: command.Description, Status: command.Status, IssueType: command.IssueType,
		Priority: command.Priority, Design: command.Design,
		AcceptanceCriteria: command.AcceptanceCriteria, Notes: command.Notes,
		Assignee: command.Assignee, Owner: command.Owner, CreatedBy: command.CreatedBy,
		ExternalRef: command.ExternalRef, EstimatedMinutes: command.EstimatedMinutes,
		Labels: command.Labels, Dependencies: command.Dependencies, SourceRepo: command.SourceRepo,
		DueAt: command.DueAt, DeferUntil: command.DeferUntil,
		IdempotencyKey: command.IdempotencyKey, Force: command.Force,
	})
	if err != nil {
		return nil, translateWorkItemsBackendError(err)
	}
	if value == nil {
		return nil, workitems.ErrInvalidPersistedState
	}
	out := workItemSummary(*value)
	return &out, nil
}

func (s *workItemsBackendStore) BlockRepositoryRequired(ctx context.Context, issueID string) (*workitems.RepositoryAdmissionResult, error) {
	be, err := s.backend(ctx)
	if err != nil {
		return nil, err
	}
	repositories, ok := be.(backend.RepositoryRequirementBackend)
	if !ok {
		return nil, fmt.Errorf("issue backend does not support atomic repository-required admission: %w", workitems.ErrNotImplemented)
	}
	ctx, cancel := context.WithTimeout(ctx, workItemOperationTimeout)
	defer cancel()
	value, err := repositories.BlockRepositoryRequired(ctx, issueID)
	if err != nil {
		return nil, translateWorkItemsBackendError(err)
	}
	if value == nil {
		return nil, workitems.ErrInvalidPersistedState
	}
	out := &workitems.RepositoryAdmissionResult{
		Changed: value.Changed, Replayed: value.Replayed, DispatchReady: value.DispatchReady,
		Blocked: value.Blocked, Reopened: value.Reopened, Outcome: value.Outcome,
	}
	if value.Issue != nil {
		issue := workItemSummary(*value.Issue)
		out.Issue = &issue
	}
	return out, nil
}

func (s *workItemsBackendStore) List(ctx context.Context, filter workitems.ListFilter) ([]workitems.IssueSummary, error) {
	be, err := s.backend(ctx)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, workItemOperationTimeout)
	defer cancel()
	values, err := be.List(ctx, backend.ListOpts{
		Status: filter.Status, IssueType: filter.IssueType, Assignee: filter.Assignee,
		Labels: filter.Labels, ParentID: filter.ParentID, Limit: filter.Limit,
		SourceRepos: filter.SourceRepos,
	})
	if err != nil {
		return nil, translateWorkItemsBackendError(err)
	}
	return workItemSummaries(values), nil
}

func (s *workItemsBackendStore) Blocked(ctx context.Context, query workitems.AvailabilityQuery) ([]workitems.IssueSummary, error) {
	be, err := s.backend(ctx)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, workItemOperationTimeout)
	defer cancel()
	values, err := be.Blocked(ctx, backend.BlockedOpts{
		ParentID: query.ParentID, Assignee: query.Assignee, Priority: query.Priority,
		Type: query.IssueType, Labels: query.Labels, SourceRepos: query.SourceRepos,
		Limit: query.Limit,
	})
	if err != nil {
		return nil, translateWorkItemsBackendError(err)
	}
	return workItemSummaries(values), nil
}

func (s *workItemsBackendStore) Ready(ctx context.Context, query workitems.AvailabilityQuery) ([]workitems.IssueSummary, error) {
	be, err := s.backend(ctx)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, workItemOperationTimeout)
	defer cancel()
	values, err := be.Ready(ctx, backend.ReadyOpts{
		ParentID: query.ParentID, Assignee: query.Assignee, Unassigned: query.Unassigned,
		Priority: query.Priority, Type: query.IssueType, Labels: query.Labels,
		LabelsAny: query.LabelsAny, SourceRepos: query.SourceRepos, Limit: query.Limit,
		SortPolicy: query.SortPolicy, MolType: query.MolType,
	})
	if err != nil {
		return nil, translateWorkItemsBackendError(err)
	}
	return workItemSummaries(values), nil
}

func (s *workItemsBackendStore) Deferred(ctx context.Context, query workitems.AvailabilityQuery) ([]workitems.IssueSummary, error) {
	be, err := s.backend(ctx)
	if err != nil {
		return nil, err
	}
	deferred, ok := be.(backend.DeferredIssueBackend)
	if !ok {
		return []workitems.IssueSummary{}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, workItemOperationTimeout)
	defer cancel()
	values, err := deferred.Deferred(ctx, backend.DeferredOpts{
		ParentID: query.ParentID, Assignee: query.Assignee, Priority: query.Priority,
		Type: query.IssueType, Labels: query.Labels, SourceRepos: query.SourceRepos,
		Limit: query.Limit,
	})
	if err != nil {
		return nil, translateWorkItemsBackendError(err)
	}
	return workItemSummaries(values), nil
}

func (s *workItemsBackendStore) Search(ctx context.Context, query workitems.SearchQuery) ([]workitems.IssueSummary, error) {
	be, err := s.backend(ctx)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, workItemOperationTimeout)
	defer cancel()
	values, err := be.SearchIssues(ctx, query.Query, query.Limit)
	if err != nil {
		return nil, translateWorkItemsBackendError(err)
	}
	out := make([]workitems.IssueSummary, 0, len(values))
	for index := range values {
		out = append(out, workItemSummary(values[index]))
	}
	return out, nil
}

func (s *workItemsBackendStore) Get(ctx context.Context, query workitems.GetQuery) (*workitems.IssueDetail, error) {
	be, err := s.backend(ctx)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, workItemOperationTimeout)
	defer cancel()
	value, err := be.Get(ctx, query.IssueID)
	if err != nil {
		return nil, translateWorkItemsBackendError(err)
	}
	if value == nil {
		return nil, workitems.ErrInvalidPersistedState
	}
	return workItemDetail(*value), nil
}

func (s *workItemsBackendStore) Patch(ctx context.Context, command workitems.PatchCommand) error {
	be, err := s.backend(ctx)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, workItemOperationTimeout)
	defer cancel()
	err = be.Update(ctx, command.IssueID, backend.UpdateParams{
		Title: command.Title, Description: command.Description, Status: command.Status,
		Priority: command.Priority, Assignee: command.Assignee, Owner: command.Owner,
		Design: command.Design, DesignFormat: command.DesignFormat,
		AcceptanceCriteria: command.AcceptanceCriteria, Notes: command.Notes,
		ExternalRef: command.ExternalRef, EstimatedMinutes: command.EstimatedMinutes,
		IssueType: command.IssueType, AddLabels: command.AddLabels,
		RemoveLabels: command.RemoveLabels, SetLabels: command.SetLabels,
		Parent: command.Parent, DueAt: command.DueAt, DeferUntil: command.DeferUntil,
		AgentState: command.AgentState,
	})
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "cannot update template") {
		return fmt.Errorf("%s: %w", err.Error(), workitems.ErrConflict)
	}
	return translateWorkItemsBackendError(err)
}

func (s *workItemsBackendStore) Close(ctx context.Context, command workitems.CloseCommand) (*workitems.CloseResult, error) {
	be, err := s.backend(ctx)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, workItemOperationTimeout)
	defer cancel()
	value, err := be.Close(ctx, command.IssueID, backend.CloseParams{
		Reason: command.Reason, Session: command.Session,
		SuggestNext: command.SuggestNext, Force: command.Force,
	})
	if err != nil {
		if backend.IsAlreadyClosedConflict(err) {
			return nil, workitems.ErrAlreadyClosed
		}
		return nil, translateWorkItemsBackendError(err)
	}
	if value == nil {
		return nil, workitems.ErrInvalidPersistedState
	}
	out := &workitems.CloseResult{Unblocked: make([]workitems.IssueSummary, 0, len(value.Unblocked))}
	if value.Closed != nil {
		closed := workItemSummary(*value.Closed)
		out.Closed = &closed
	}
	for index := range value.Unblocked {
		out.Unblocked = append(out.Unblocked, workItemSummary(value.Unblocked[index]))
	}
	return out, nil
}

func (s *workItemsBackendStore) AssignRepository(ctx context.Context, command workitems.AssignRepositoryCommand) (*workitems.IssueSummary, error) {
	be, err := s.backend(ctx)
	if err != nil {
		return nil, err
	}
	repositories, ok := be.(backend.RepositoryRequirementBackend)
	if !ok {
		return nil, fmt.Errorf("issue backend does not support repository assignment: %w", workitems.ErrNotImplemented)
	}
	ctx, cancel := context.WithTimeout(ctx, workItemOperationTimeout)
	defer cancel()
	value, err := repositories.SetIssueRepository(ctx, command.IssueID, command.Repository)
	if err != nil {
		return nil, translateWorkItemsBackendError(err)
	}
	if value == nil {
		return nil, workitems.ErrInvalidPersistedState
	}
	out := workItemSummary(*value)
	return &out, nil
}

func (s *workItemsBackendStore) Claim(ctx context.Context, command workitems.ClaimCommand) (*workitems.IssueDetail, error) {
	be, err := s.backend(ctx)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, workItemOperationTimeout)
	defer cancel()
	// FleetDB's claim command atomically acquires the lock, assigns the actor,
	// and transitions status to in_progress. The follow-up is read-only and
	// obtains the canonical detail response; no second mutation is issued.
	if err := be.ClaimIssue(ctx, command.IssueID, 0); err != nil {
		return nil, translateWorkItemsBackendError(err)
	}
	value, err := be.Get(ctx, command.IssueID)
	if err != nil {
		return nil, translateWorkItemsBackendError(err)
	}
	if value == nil {
		return nil, workitems.ErrInvalidPersistedState
	}
	return workItemDetail(*value), nil
}

func (s *workItemsBackendStore) Reopen(ctx context.Context, command workitems.ReopenCommand) error {
	be, err := s.backend(ctx)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, workItemOperationTimeout)
	defer cancel()
	return translateWorkItemsBackendError(be.Reopen(ctx, command.IssueID, backend.ReopenParams{Reason: command.Reason}))
}

func (s *workItemsBackendStore) Delete(ctx context.Context, command workitems.DeleteCommand) (workitems.DeleteResult, error) {
	be, err := s.backend(ctx)
	if err != nil {
		return workitems.DeleteResult{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, workItemOperationTimeout)
	defer cancel()
	if err := be.Delete(ctx, backend.DeleteParams{IDs: []string{command.IssueID}, Force: true}); err != nil {
		return workitems.DeleteResult{}, translateWorkItemsBackendError(err)
	}
	return workitems.DeleteResult{DeletedCount: 1, DeletedIDs: []string{command.IssueID}}, nil
}

func (s *workItemsBackendStore) ListEvents(ctx context.Context, query workitems.ListEventsQuery) ([]*workitems.Event, error) {
	be, err := s.backend(ctx)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, workItemOperationTimeout)
	defer cancel()
	values, err := be.ListEvents(ctx, query.IssueID, query.Limit)
	if err != nil {
		return nil, translateWorkItemsBackendError(err)
	}
	out := make([]*workitems.Event, 0, len(values))
	for _, value := range values {
		id, _ := strconv.ParseInt(value.ID, 10, 64)
		out = append(out, &workitems.Event{
			ID: id, IssueID: value.IssueID, EventType: workitems.EventType(value.Kind),
			Actor: value.Actor, CreatedAt: value.CreatedAt,
		})
	}
	return out, nil
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
		out = append(out, workItemDependency(value, query.IssueID))
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

func workItemSummary(value backend.IssueData) workitems.IssueSummary {
	return workitems.IssueSummary{
		ID: value.ID, Title: value.Title, Status: value.Status, Priority: value.Priority,
		IssueType: value.IssueType, Assignee: value.Assignee, Owner: value.Owner,
		Labels: append([]string(nil), value.Labels...), SourceRepo: value.SourceRepo,
		Repo: value.SourceRepo, Parent: value.Parent, Design: value.Design,
		DesignArtifactID: value.DesignArtifactID, DesignFormat: value.DesignFormat,
		HasDesign: value.HasDesign || value.Design != "", Notes: value.Notes,
		CreatedBy: value.CreatedBy, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
		ClosedAt: value.ClosedAt, CloseReason: value.CloseReason, ExternalRef: value.ExternalRef,
		DueAt: value.DueAt, DeferUntil: value.DeferUntil,
		DependencyCount: value.DependencyCount, DependentCount: value.DependentCount,
		BlockedByCount: value.BlockedByCount, BlockedBy: append([]string(nil), value.BlockedBy...),
	}
}

func workItemSummaries(values []backend.IssueData) []workitems.IssueSummary {
	out := make([]workitems.IssueSummary, 0, len(values))
	for index := range values {
		out = append(out, workItemSummary(values[index]))
	}
	return out
}

func workItemDetail(value backend.IssueDetailData) *workitems.IssueDetail {
	dependencies := make([]workitems.Dependency, 0, len(value.Dependencies))
	for _, dependency := range value.Dependencies {
		dependencies = append(dependencies, workItemDependency(dependency, value.ID))
	}
	dependents := make([]workitems.Dependency, 0, len(value.Dependents))
	for _, dependency := range value.Dependents {
		dependents = append(dependents, workItemDependency(dependency, value.ID))
	}
	comments := make([]*workitems.Comment, 0, len(value.Comments))
	for _, comment := range value.Comments {
		comments = append(comments, &workitems.Comment{
			ID: comment.ID, IssueID: comment.IssueID, Author: comment.Author,
			Text: comment.Text, CreatedAt: comment.CreatedAt,
		})
	}
	return &workitems.IssueDetail{
		ID: value.ID, Title: value.Title, Status: value.Status, Priority: value.Priority,
		IssueType: value.IssueType, Assignee: value.Assignee, Owner: value.Owner,
		Labels: append([]string{}, value.Labels...), SourceRepo: value.SourceRepo,
		Repo: value.SourceRepo, Parent: value.Parent, Design: value.Design,
		DesignArtifactID: value.DesignArtifactID, DesignFormat: value.DesignFormat,
		HasDesign: value.HasDesign || value.Design != "", Description: value.Description,
		AcceptanceCriteria: value.AcceptanceCriteria, Notes: value.Notes,
		CreatedBy: value.CreatedBy, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
		ClosedAt: value.ClosedAt, CloseReason: value.CloseReason,
		ClosedBySession: value.ClosedBySession, ExternalRef: value.ExternalRef,
		EstimatedMinutes: value.EstimatedMinutes, DueAt: value.DueAt, DeferUntil: value.DeferUntil,
		Dependencies: dependencies, Dependents: dependents, Comments: comments,
	}
}

func workItemDependency(value backend.DependencyData, selfID string) workitems.Dependency {
	relatedID := value.DependsOnID
	if value.DependsOnID == selfID && value.IssueID != "" {
		relatedID = value.IssueID
	}
	return workitems.Dependency{
		ID: relatedID, Title: value.Title, Status: value.Status, Priority: value.Priority,
		CreatedAt: value.CreatedAt, DependencyType: value.Type,
		IssueType: value.IssueType, CreatedBy: value.CreatedBy,
	}
}

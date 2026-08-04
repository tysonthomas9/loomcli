package workspace

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

const maxWorkspaceNameLength = 64

type Service struct {
	store        CatalogStore
	repositories RepositoryCatalogStore
}

var _ API = (*Service)(nil)

type Option func(*Service) error

func WithRepositoryCatalog(repositories RepositoryCatalogStore) Option {
	return func(service *Service) error {
		if repositories == nil {
			return fmt.Errorf("compose Workspace: repository catalog is required: %w", ErrUnavailable)
		}
		service.repositories = repositories
		return nil
	}
}

func New(store CatalogStore, options ...Option) (*Service, error) {
	if store == nil {
		return nil, fmt.Errorf("compose Workspace: catalog store is required: %w", ErrUnavailable)
	}
	service := &Service{store: store}
	for _, option := range options {
		if option == nil {
			continue
		}
		if err := option(service); err != nil {
			return nil, err
		}
	}
	return service, nil
}

func (s *Service) Create(ctx context.Context, command CreateCommand) (*Reference, error) {
	key := strings.TrimSpace(command.Key)
	name := strings.TrimSpace(command.Name)
	if key == "" {
		return nil, fmt.Errorf("workspace key is required: %w", ErrInvalid)
	}
	if err := validateName(name); err != nil {
		return nil, err
	}
	designFormat := strings.TrimSpace(command.DesignFormat)
	if designFormat != "" && designFormat != DesignFormatMarkdown && designFormat != DesignFormatHTML {
		return nil, fmt.Errorf("design format must be markdown or html: %w", ErrInvalid)
	}
	created, err := s.store.Create(ctx, CreateInput{
		Key: key, Name: name, Description: strings.TrimSpace(command.Description),
		DefaultBranch: strings.TrimSpace(command.DefaultBranch), DesignFormat: designFormat,
	})
	if err != nil {
		return nil, err
	}
	return validatedReference(created, "create workspace "+key)
}

func (s *Service) Resolve(ctx context.Context, query ResolveQuery) (*Reference, error) {
	reference := strings.TrimSpace(query.Reference)
	if reference == "" {
		return nil, fmt.Errorf("workspace reference is required: %w", ErrInvalid)
	}
	value, err := s.store.GetByKey(ctx, reference)
	if errors.Is(err, ErrNotFound) {
		value, err = s.store.GetByName(ctx, reference)
	}
	if err != nil {
		return nil, err
	}
	return validatedReference(value, "resolve workspace "+reference)
}

func (s *Service) List(ctx context.Context, _ ListQuery) ([]Reference, error) {
	values, err := s.store.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Reference, len(values))
	for index := range values {
		value, err := validatedReference(&values[index], "list workspaces")
		if err != nil {
			return nil, err
		}
		out[index] = *value
	}
	return out, nil
}

func (s *Service) Rename(ctx context.Context, command RenameCommand) (*Reference, error) {
	name := strings.TrimSpace(command.Name)
	if err := validateName(name); err != nil {
		return nil, err
	}
	current, err := s.Resolve(ctx, ResolveQuery{Reference: command.Reference})
	if err != nil {
		return nil, err
	}
	if current.Name == name {
		return current, nil
	}
	existing, lookupErr := s.store.GetByName(ctx, name)
	switch {
	case lookupErr == nil && existing != nil && existing.Key != current.Key:
		return nil, fmt.Errorf("workspace name %q already exists: %w", name, ErrConflict)
	case lookupErr != nil && !errors.Is(lookupErr, ErrNotFound):
		return nil, lookupErr
	}
	updated, err := s.store.Rename(ctx, current.Key, name)
	if err != nil {
		return nil, err
	}
	return validatedReference(updated, "rename workspace "+current.Key)
}

func (s *Service) SetDesignFormat(ctx context.Context, command SetDesignFormatCommand) (*Reference, error) {
	format := strings.TrimSpace(command.Format)
	if format != DesignFormatMarkdown && format != DesignFormatHTML {
		return nil, fmt.Errorf("design format must be markdown or html: %w", ErrInvalid)
	}
	current, err := s.Resolve(ctx, ResolveQuery{Reference: command.Reference})
	if err != nil {
		return nil, err
	}
	if current.DesignFormat == format {
		return current, nil
	}
	updated, err := s.store.SetDesignFormat(ctx, current.Key, format)
	if err != nil {
		return nil, err
	}
	return validatedReference(updated, "set workspace design format "+current.Key)
}

func (s *Service) SetLifecycle(ctx context.Context, command SetLifecycleCommand) (*Reference, error) {
	if !validState(command.State) {
		return nil, fmt.Errorf("invalid workspace lifecycle state %q: %w", command.State, ErrInvalid)
	}
	current, err := s.Resolve(ctx, ResolveQuery{Reference: command.Reference})
	if err != nil {
		return nil, err
	}
	update := LifecycleUpdate{State: command.State, ErrorMessage: strings.TrimSpace(command.ErrorMessage)}
	if command.DefaultBranch != nil {
		branch := strings.TrimSpace(*command.DefaultBranch)
		update.DefaultBranch = &branch
	}
	updated, err := s.store.SetLifecycle(ctx, current.Key, update)
	if err != nil {
		return nil, err
	}
	return validatedReference(updated, "set workspace lifecycle "+current.Key)
}

func (s *Service) Delete(ctx context.Context, command DeleteCommand) (*Reference, error) {
	current, err := s.Resolve(ctx, ResolveQuery{Reference: command.Reference})
	if err != nil {
		return nil, err
	}
	if err := s.store.Delete(ctx, current.Key); err != nil {
		return nil, err
	}
	return validatedReference(current, "delete workspace "+current.Key)
}

func (s *Service) GetRepository(ctx context.Context, query GetRepositoryQuery) (*Repository, error) {
	if s.repositories == nil {
		return nil, fmt.Errorf("Workspace repository catalog unavailable: %w", ErrUnavailable)
	}
	name := strings.TrimSpace(query.Name)
	if name == "" {
		return nil, fmt.Errorf("repository name is required: %w", ErrInvalid)
	}
	workspace, err := s.Resolve(ctx, ResolveQuery{Reference: query.WorkspaceReference})
	if err != nil {
		return nil, err
	}
	value, err := s.repositories.Get(ctx, workspace.Key, name)
	if err != nil {
		return nil, err
	}
	return validatedRepository(value, workspace.Key, "get repository "+name)
}

func (s *Service) ListRepositories(ctx context.Context, query ListRepositoriesQuery) ([]Repository, error) {
	if s.repositories == nil {
		return nil, fmt.Errorf("Workspace repository catalog unavailable: %w", ErrUnavailable)
	}
	workspace, err := s.Resolve(ctx, ResolveQuery{Reference: query.WorkspaceReference})
	if err != nil {
		return nil, err
	}
	values, err := s.repositories.List(ctx, workspace.Key)
	if err != nil {
		return nil, err
	}
	out := make([]Repository, len(values))
	for index := range values {
		value, err := validatedRepository(&values[index], workspace.Key, "list repositories")
		if err != nil {
			return nil, err
		}
		out[index] = *value
	}
	return out, nil
}

func (s *Service) RegisterRepository(ctx context.Context, command RegisterRepositoryCommand) (*Repository, error) {
	if s.repositories == nil {
		return nil, fmt.Errorf("Workspace repository catalog unavailable: %w", ErrUnavailable)
	}
	name := strings.TrimSpace(command.Name)
	if name == "" {
		return nil, fmt.Errorf("repository name is required: %w", ErrInvalid)
	}
	workspace, err := s.Resolve(ctx, ResolveQuery{Reference: command.WorkspaceReference})
	if err != nil {
		return nil, err
	}
	value, err := s.repositories.Create(ctx, RepositoryInput{
		WorkspaceKey: workspace.Key, Name: name,
		RemoteURL: strings.TrimSpace(command.RemoteURL), Remote: strings.TrimSpace(command.Remote),
		DefaultBranch: strings.TrimSpace(command.DefaultBranch),
		Groups:        append([]string(nil), command.Groups...), SourceRepoID: strings.TrimSpace(command.SourceRepoID),
	})
	if err != nil {
		return nil, err
	}
	return validatedRepository(value, workspace.Key, "register repository "+name)
}

func (s *Service) UnregisterRepository(ctx context.Context, command UnregisterRepositoryCommand) (*Repository, error) {
	if s.repositories == nil {
		return nil, fmt.Errorf("Workspace repository catalog unavailable: %w", ErrUnavailable)
	}
	current, err := s.GetRepository(ctx, GetRepositoryQuery{
		WorkspaceReference: command.WorkspaceReference,
		Name:               command.Name,
	})
	if err != nil {
		return nil, err
	}
	if err := s.repositories.Delete(ctx, current.WorkspaceKey, current.Name); err != nil {
		return nil, err
	}
	return current, nil
}

func validatedReference(value *Reference, operation string) (*Reference, error) {
	if value == nil || strings.TrimSpace(value.Key) == "" || strings.TrimSpace(value.Name) == "" {
		return nil, fmt.Errorf("%s returned an invalid reference: %w", operation, ErrInvalidPersistedState)
	}
	copy := *value
	return &copy, nil
}

func validatedRepository(value *Repository, workspaceKey, operation string) (*Repository, error) {
	if value == nil || strings.TrimSpace(value.Name) == "" || value.WorkspaceKey != workspaceKey {
		return nil, fmt.Errorf("%s returned an invalid repository: %w", operation, ErrInvalidPersistedState)
	}
	copy := *value
	copy.Groups = append([]string(nil), value.Groups...)
	return &copy, nil
}

func validateName(name string) error {
	if name == "" {
		return fmt.Errorf("workspace name is required: %w", ErrInvalid)
	}
	if len(name) > maxWorkspaceNameLength {
		return fmt.Errorf("workspace name is too long (max %d characters): %w", maxWorkspaceNameLength, ErrInvalid)
	}
	for _, value := range name {
		if !((value >= 'a' && value <= 'z') || (value >= 'A' && value <= 'Z') ||
			(value >= '0' && value <= '9') || value == '-' || value == '_') {
			return fmt.Errorf("workspace name must contain only alphanumeric characters, hyphens, and underscores: %w", ErrInvalid)
		}
	}
	return nil
}

func validState(state State) bool {
	switch state {
	case StateCreating, StateCloning, StateInitializing, StateReady, StateError:
		return true
	default:
		return false
	}
}

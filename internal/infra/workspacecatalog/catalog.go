// Package workspacecatalog adapts the transitional FleetDB-backed Store
// interfaces to the Workspace capability's narrow persistence ports.
package workspacecatalog

import (
	"context"
	"errors"
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/modules/workspace"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func New(workspaces store.WorkspaceStore, repositories store.RepoStore) (workspace.API, error) {
	if workspaces == nil {
		return nil, nil
	}
	if repositories == nil {
		return nil, fmt.Errorf("compose Workspace repositories: %w", workspace.ErrUnavailable)
	}
	return workspace.New(
		catalogStore{store: workspaces},
		workspace.WithRepositoryCatalog(repositoryCatalogStore{store: repositories}),
	)
}

func NewCatalog(workspaces store.WorkspaceStore) (workspace.API, error) {
	if workspaces == nil {
		return nil, nil
	}
	return workspace.New(catalogStore{store: workspaces})
}

type catalogStore struct{ store store.WorkspaceStore }
type repositoryCatalogStore struct{ store store.RepoStore }

func (s catalogStore) Create(ctx context.Context, input workspace.CreateInput) (*workspace.Reference, error) {
	value, err := s.store.Create(ctx, store.WorkspaceCreate{
		Key: input.Key, Name: input.Name, Description: input.Description,
		DefaultBranch: input.DefaultBranch, DesignFormat: input.DesignFormat,
	})
	if err != nil {
		return nil, translateError(err)
	}
	return reference(value), nil
}

func (s catalogStore) GetByKey(ctx context.Context, key string) (*workspace.Reference, error) {
	value, err := s.store.Get(ctx, key)
	if err != nil {
		return nil, translateError(err)
	}
	return reference(value), nil
}

func (s catalogStore) GetByName(ctx context.Context, name string) (*workspace.Reference, error) {
	value, err := s.store.GetByName(ctx, name)
	if err != nil {
		return nil, translateError(err)
	}
	return reference(value), nil
}

func (s catalogStore) List(ctx context.Context) ([]workspace.Reference, error) {
	values, err := s.store.List(ctx)
	if err != nil {
		return nil, translateError(err)
	}
	out := make([]workspace.Reference, len(values))
	for index, value := range values {
		mapped := reference(value)
		if mapped == nil {
			return nil, workspace.ErrInvalidPersistedState
		}
		out[index] = *mapped
	}
	return out, nil
}

func (s catalogStore) Rename(ctx context.Context, key, name string) (*workspace.Reference, error) {
	value, err := s.store.Update(ctx, key, store.WorkspaceUpdate{Name: &name})
	if err != nil {
		return nil, translateError(err)
	}
	return reference(value), nil
}

func (s catalogStore) SetDesignFormat(ctx context.Context, key, format string) (*workspace.Reference, error) {
	value, err := s.store.Update(ctx, key, store.WorkspaceUpdate{DesignFormat: &format})
	if err != nil {
		return nil, translateError(err)
	}
	return reference(value), nil
}

func (s catalogStore) SetLifecycle(ctx context.Context, key string, update workspace.LifecycleUpdate) (*workspace.Reference, error) {
	state := update.State
	message := update.ErrorMessage
	patch := store.WorkspaceUpdate{State: &state, ErrorMessage: &message}
	if update.DefaultBranch != nil {
		branch := *update.DefaultBranch
		patch.DefaultBranch = &branch
	}
	value, err := s.store.Update(ctx, key, patch)
	if err != nil {
		return nil, translateError(err)
	}
	return reference(value), nil
}

func (s catalogStore) Delete(ctx context.Context, key string) error {
	return translateError(s.store.Delete(ctx, key))
}

func (s repositoryCatalogStore) Create(ctx context.Context, input workspace.RepositoryInput) (*workspace.Repository, error) {
	value, err := s.store.Create(ctx, store.RepoCreate{
		WorkspaceKey: input.WorkspaceKey, Name: input.Name,
		RemoteURL: input.RemoteURL, Remote: input.Remote, DefaultBranch: input.DefaultBranch,
		Groups: append([]string(nil), input.Groups...), SourceRepoID: input.SourceRepoID,
	})
	if err != nil {
		return nil, translateError(err)
	}
	return repository(value), nil
}

func (s repositoryCatalogStore) Get(ctx context.Context, workspaceKey, name string) (*workspace.Repository, error) {
	value, err := s.store.Get(ctx, workspaceKey, name)
	if err != nil {
		return nil, translateError(err)
	}
	return repository(value), nil
}

func (s repositoryCatalogStore) List(ctx context.Context, workspaceKey string) ([]workspace.Repository, error) {
	values, err := s.store.List(ctx, workspaceKey)
	if err != nil {
		return nil, translateError(err)
	}
	out := make([]workspace.Repository, len(values))
	for index, value := range values {
		mapped := repository(value)
		if mapped == nil {
			return nil, workspace.ErrInvalidPersistedState
		}
		out[index] = *mapped
	}
	return out, nil
}

func (s repositoryCatalogStore) Update(ctx context.Context, workspaceKey, name string, update workspace.RepositoryUpdate) (*workspace.Repository, error) {
	value, err := s.store.Update(ctx, workspaceKey, name, store.RepoUpdate{
		RemoteURL: update.RemoteURL, Remote: update.Remote, DefaultBranch: update.DefaultBranch,
		Groups: update.Groups, SourceRepoID: update.SourceRepoID,
	})
	if err != nil {
		return nil, translateError(err)
	}
	return repository(value), nil
}

func (s repositoryCatalogStore) Delete(ctx context.Context, workspaceKey, name string) error {
	return translateError(s.store.Delete(ctx, workspaceKey, name))
}

func reference(value *domain.Workspace) *workspace.Reference {
	if value == nil {
		return nil
	}
	return &workspace.Reference{
		Key: value.Key, Name: value.Name, Description: value.Description,
		State: value.State, ErrorMessage: value.ErrorMessage,
		DefaultBranch: value.DefaultBranch, DesignFormat: value.DesignFormat,
		CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}

func repository(value *domain.Repo) *workspace.Repository {
	if value == nil {
		return nil
	}
	copy := *value
	copy.Groups = append([]string(nil), value.Groups...)
	return &copy
}

func translateError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, domain.ErrNotFound):
		return workspace.ErrNotFound
	case errors.Is(err, domain.ErrInvalid):
		return fmt.Errorf("%s: %w", err.Error(), workspace.ErrInvalid)
	case errors.Is(err, domain.ErrConflict), errors.Is(err, domain.ErrAlreadyExists):
		return fmt.Errorf("%s: %w", err.Error(), workspace.ErrConflict)
	case errors.Is(err, domain.ErrUnavailable):
		return fmt.Errorf("%s: %w", err.Error(), workspace.ErrUnavailable)
	default:
		return err
	}
}

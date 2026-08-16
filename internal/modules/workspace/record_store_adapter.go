// Package workspace adapts FleetDB-backed records at the Workspace owner seam.
package workspace

import (
	"context"
	"errors"
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/platform/persistence"
)

func NewFromRecordStores(workspaces WorkspaceStore, repositories RepoStore) (API, error) {
	if workspaces == nil {
		return nil, nil
	}
	if repositories == nil {
		return nil, fmt.Errorf("compose Workspace repositories: %w", ErrUnavailable)
	}
	return New(
		catalogStore{store: workspaces},
		WithRepositoryCatalog(repositoryCatalogStore{store: repositories}),
	)
}

func NewCatalogFromRecordStore(workspaces WorkspaceStore) (API, error) {
	if workspaces == nil {
		return nil, nil
	}
	return New(catalogStore{store: workspaces})
}

type catalogStore struct{ store WorkspaceStore }
type repositoryCatalogStore struct{ store RepoStore }

func (s catalogStore) Create(ctx context.Context, input CreateInput) (*Reference, error) {
	value, err := s.store.Create(ctx, WorkspaceCreate(input))
	if err != nil {
		return nil, translateError(err)
	}
	return reference(value), nil
}

func (s catalogStore) GetByKey(ctx context.Context, key string) (*Reference, error) {
	value, err := s.store.Get(ctx, key)
	if err != nil {
		return nil, translateError(err)
	}
	return reference(value), nil
}

func (s catalogStore) GetByName(ctx context.Context, name string) (*Reference, error) {
	value, err := s.store.GetByName(ctx, name)
	if err != nil {
		return nil, translateError(err)
	}
	return reference(value), nil
}

func (s catalogStore) List(ctx context.Context) ([]Reference, error) {
	values, err := s.store.List(ctx)
	if err != nil {
		return nil, translateError(err)
	}
	out := make([]Reference, len(values))
	for index, value := range values {
		mapped := reference(value)
		if mapped == nil {
			return nil, ErrInvalidPersistedState
		}
		out[index] = *mapped
	}
	return out, nil
}

func (s catalogStore) Rename(ctx context.Context, key, name string) (*Reference, error) {
	value, err := s.store.Update(ctx, key, WorkspaceUpdate{Name: &name})
	if err != nil {
		return nil, translateError(err)
	}
	return reference(value), nil
}

func (s catalogStore) SetDesignFormat(ctx context.Context, key, format string) (*Reference, error) {
	value, err := s.store.Update(ctx, key, WorkspaceUpdate{DesignFormat: &format})
	if err != nil {
		return nil, translateError(err)
	}
	return reference(value), nil
}

func (s catalogStore) SetLifecycle(ctx context.Context, key string, update LifecycleUpdate) (*Reference, error) {
	state := update.State
	message := update.ErrorMessage
	patch := WorkspaceUpdate{State: &state, ErrorMessage: &message}
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

func (s repositoryCatalogStore) Create(ctx context.Context, input RepositoryInput) (*Repository, error) {
	value, err := s.store.Create(ctx, RepoCreate{
		WorkspaceKey: input.WorkspaceKey, Name: input.Name,
		RemoteURL: input.RemoteURL, Remote: input.Remote, DefaultBranch: input.DefaultBranch,
		Groups: append([]string(nil), input.Groups...), SourceRepoID: input.SourceRepoID,
	})
	if err != nil {
		return nil, translateError(err)
	}
	return repository(value), nil
}

func (s repositoryCatalogStore) Get(ctx context.Context, workspaceKey, name string) (*Repository, error) {
	value, err := s.store.Get(ctx, workspaceKey, name)
	if err != nil {
		return nil, translateError(err)
	}
	return repository(value), nil
}

func (s repositoryCatalogStore) List(ctx context.Context, workspaceKey string) ([]Repository, error) {
	values, err := s.store.List(ctx, workspaceKey)
	if err != nil {
		return nil, translateError(err)
	}
	out := make([]Repository, len(values))
	for index, value := range values {
		mapped := repository(value)
		if mapped == nil {
			return nil, ErrInvalidPersistedState
		}
		out[index] = *mapped
	}
	return out, nil
}

func (s repositoryCatalogStore) Update(ctx context.Context, workspaceKey, name string, update RepositoryUpdate) (*Repository, error) {
	value, err := s.store.Update(ctx, workspaceKey, name, RepoUpdate(update))
	if err != nil {
		return nil, translateError(err)
	}
	return repository(value), nil
}

func (s repositoryCatalogStore) Delete(ctx context.Context, workspaceKey, name string) error {
	return translateError(s.store.Delete(ctx, workspaceKey, name))
}

func reference(value *Workspace) *Reference {
	if value == nil {
		return nil
	}
	return &Reference{
		Key: value.Key, Name: value.Name, Description: value.Description,
		State: value.State, ErrorMessage: value.ErrorMessage,
		DefaultBranch: value.DefaultBranch, DesignFormat: value.DesignFormat,
		CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}

func repository(value *Repository) *Repository {
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
	case errors.Is(err, persistence.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, persistence.ErrInvalid):
		return fmt.Errorf("%s: %w", err.Error(), ErrInvalid)
	case errors.Is(err, persistence.ErrConflict), errors.Is(err, persistence.ErrAlreadyExists):
		return fmt.Errorf("%s: %w", err.Error(), ErrConflict)
	case errors.Is(err, persistence.ErrUnavailable):
		return fmt.Errorf("%s: %w", err.Error(), ErrUnavailable)
	default:
		return err
	}
}

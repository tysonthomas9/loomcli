package capabilitycomposition

import (
	"context"
	"errors"
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/modules/workspace"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// NewWorkspaceCatalog wraps the narrow persisted workspace collection at
// composition and exposes the Workspace module's catalog query port.
func NewWorkspaceCatalog(st store.WorkspaceStore) (workspace.API, error) {
	if st == nil {
		return nil, nil
	}
	return workspace.New(workspaceCatalogStore{store: st})
}

// NewWorkspaceCapability composes the complete shared Workspace catalog. Git
// checkout and worktree state remains outside this adapter in Source Control.
func NewWorkspaceCapability(workspaces store.WorkspaceStore, repositories store.RepoStore) (workspace.API, error) {
	if workspaces == nil {
		return nil, nil
	}
	if repositories == nil {
		return nil, fmt.Errorf("compose Workspace repositories: %w", workspace.ErrUnavailable)
	}
	return workspace.New(
		workspaceCatalogStore{store: workspaces},
		workspace.WithRepositoryCatalog(workspaceRepositoryCatalogStore{store: repositories}),
	)
}

type workspaceCatalogStore struct {
	store store.WorkspaceStore
}

type workspaceRepositoryCatalogStore struct {
	store store.RepoStore
}

func (s workspaceRepositoryCatalogStore) Get(ctx context.Context, workspaceKey, name string) (*workspace.Repository, error) {
	value, err := s.store.Get(ctx, workspaceKey, name)
	if err != nil {
		return nil, translateWorkspaceStoreError(err)
	}
	return workspaceRepository(value), nil
}

func (s workspaceRepositoryCatalogStore) List(ctx context.Context, workspaceKey string) ([]workspace.Repository, error) {
	values, err := s.store.List(ctx, workspaceKey)
	if err != nil {
		return nil, translateWorkspaceStoreError(err)
	}
	out := make([]workspace.Repository, len(values))
	for index, value := range values {
		mapped := workspaceRepository(value)
		if mapped == nil {
			return nil, workspace.ErrInvalidPersistedState
		}
		out[index] = *mapped
	}
	return out, nil
}

func (s workspaceCatalogStore) GetByKey(ctx context.Context, key string) (*workspace.Reference, error) {
	value, err := s.store.Get(ctx, key)
	if err != nil {
		return nil, translateWorkspaceStoreError(err)
	}
	return workspaceReference(value), nil
}

func (s workspaceCatalogStore) GetByName(ctx context.Context, name string) (*workspace.Reference, error) {
	value, err := s.store.GetByName(ctx, name)
	if err != nil {
		return nil, translateWorkspaceStoreError(err)
	}
	return workspaceReference(value), nil
}

func (s workspaceCatalogStore) List(ctx context.Context) ([]workspace.Reference, error) {
	values, err := s.store.List(ctx)
	if err != nil {
		return nil, translateWorkspaceStoreError(err)
	}
	out := make([]workspace.Reference, len(values))
	for index, value := range values {
		mapped := workspaceReference(value)
		if mapped == nil {
			return nil, workspace.ErrInvalidPersistedState
		}
		out[index] = *mapped
	}
	return out, nil
}

func (s workspaceCatalogStore) Rename(ctx context.Context, key, name string) (*workspace.Reference, error) {
	value, err := s.store.Update(ctx, key, store.WorkspaceUpdate{Name: &name})
	if err != nil {
		return nil, translateWorkspaceStoreError(err)
	}
	return workspaceReference(value), nil
}

func (s workspaceCatalogStore) SetDesignFormat(ctx context.Context, key, format string) (*workspace.Reference, error) {
	value, err := s.store.Update(ctx, key, store.WorkspaceUpdate{DesignFormat: &format})
	if err != nil {
		return nil, translateWorkspaceStoreError(err)
	}
	return workspaceReference(value), nil
}

func (s workspaceCatalogStore) Delete(ctx context.Context, key string) error {
	return translateWorkspaceStoreError(s.store.Delete(ctx, key))
}

func workspaceReference(value *domain.Workspace) *workspace.Reference {
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

func workspaceRepository(value *domain.Repo) *workspace.Repository {
	if value == nil {
		return nil
	}
	copy := *value
	copy.Groups = append([]string(nil), value.Groups...)
	return &copy
}

func translateWorkspaceStoreError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, domain.ErrNotFound):
		return workspace.ErrNotFound
	case errors.Is(err, domain.ErrInvalid):
		return fmt.Errorf("%s: %w", err.Error(), workspace.ErrInvalid)
	case errors.Is(err, domain.ErrConflict):
		return fmt.Errorf("%s: %w", err.Error(), workspace.ErrConflict)
	case errors.Is(err, domain.ErrUnavailable):
		return fmt.Errorf("%s: %w", err.Error(), workspace.ErrUnavailable)
	default:
		return err
	}
}

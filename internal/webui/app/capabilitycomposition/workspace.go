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

type workspaceCatalogStore struct {
	store store.WorkspaceStore
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

func translateWorkspaceStoreError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, domain.ErrNotFound):
		return workspace.ErrNotFound
	case errors.Is(err, domain.ErrInvalid):
		return fmt.Errorf("%s: %w", err.Error(), workspace.ErrInvalid)
	case errors.Is(err, domain.ErrUnavailable):
		return fmt.Errorf("%s: %w", err.Error(), workspace.ErrUnavailable)
	default:
		return err
	}
}

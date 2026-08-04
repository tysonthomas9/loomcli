package store

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

// RepoCreate is the input for RepoStore.Create. WorkspaceKey + Name are
// required; the workspace must already exist.
type RepoCreate struct {
	WorkspaceKey  string
	Name          string
	RemoteURL     string
	Remote        string
	DefaultBranch string
	Groups        []string
	SourceRepoID  string
}

// RepoUpdate is the partial-update payload for repos.
type RepoUpdate struct {
	RemoteURL     *string
	Remote        *string
	DefaultBranch *string
	Groups        *[]string
	SourceRepoID  *string
}

// RepoStore is the persistence interface for Repo entities. All methods
// are workspace-scoped — Repo names are unique within a workspace.
type RepoStore interface {
	Create(ctx context.Context, in RepoCreate) (*domain.Repo, error)
	Get(ctx context.Context, workspaceKey, name string) (*domain.Repo, error)
	List(ctx context.Context, workspaceKey string) ([]*domain.Repo, error)
	Update(ctx context.Context, workspaceKey, name string, patch RepoUpdate) (*domain.Repo, error)
	Delete(ctx context.Context, workspaceKey, name string) error
}

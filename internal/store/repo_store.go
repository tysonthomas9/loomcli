package store

import (
	"context"

	workspacemodule "github.com/tysonthomas9/loomcli/internal/modules/workspace"
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
	Create(ctx context.Context, in RepoCreate) (*workspacemodule.Repository, error)
	Get(ctx context.Context, workspaceKey, name string) (*workspacemodule.Repository, error)
	List(ctx context.Context, workspaceKey string) ([]*workspacemodule.Repository, error)
	Update(ctx context.Context, workspaceKey, name string, patch RepoUpdate) (*workspacemodule.Repository, error)
	Delete(ctx context.Context, workspaceKey, name string) error
}

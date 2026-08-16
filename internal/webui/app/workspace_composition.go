package app

import (
	"github.com/tysonthomas9/loomcli/internal/modules/workspace"
)

// NewWorkspaceCatalog wraps the narrow persisted workspace collection at
// composition and exposes the Workspace module's catalog query port.
func NewWorkspaceCatalog(st workspace.WorkspaceStore) (workspace.API, error) {
	if st == nil {
		return nil, nil
	}
	return workspace.NewCatalogFromRecordStore(st)
}

// NewWorkspaceCapability composes the complete shared Workspace catalog. Git
// checkout and worktree state remains outside this adapter in Source Control.
func NewWorkspaceCapability(workspaces workspace.WorkspaceStore, repositories workspace.RepoStore) (workspace.API, error) {
	return workspace.NewFromRecordStores(workspaces, repositories)
}

package app

import (
	"github.com/tysonthomas9/loomcli/internal/infra/workspacecatalog"
	"github.com/tysonthomas9/loomcli/internal/modules/workspace"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// NewWorkspaceCatalog wraps the narrow persisted workspace collection at
// composition and exposes the Workspace module's catalog query port.
func NewWorkspaceCatalog(st store.WorkspaceStore) (workspace.API, error) {
	if st == nil {
		return nil, nil
	}
	return workspacecatalog.NewCatalog(st)
}

// NewWorkspaceCapability composes the complete shared Workspace catalog. Git
// checkout and worktree state remains outside this adapter in Source Control.
func NewWorkspaceCapability(workspaces store.WorkspaceStore, repositories store.RepoStore) (workspace.API, error) {
	return workspacecatalog.New(workspaces, repositories)
}

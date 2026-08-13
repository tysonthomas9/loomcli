package opsimpl

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/storeadapter"
)

type testWorkspaceProjection struct {
	store store.Store
}

func (projection testWorkspaceProjection) WorkspaceData(ctx context.Context, workspaceKey string) (*ops.WorkspaceData, error) {
	return storeadapter.BuildWorkspaceDataForKey(ctx, projection.store, workspaceKey)
}

func (projection testWorkspaceProjection) WorkspacePath(ctx context.Context, workspaceKey string) string {
	return storeadapter.ResolveOrHealWorkspacePath(ctx, projection.store, workspaceKey)
}

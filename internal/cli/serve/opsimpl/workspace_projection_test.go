package opsimpl

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/app/query/operationalview"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/webui/storeadapter"
)

type testWorkspaceProjection struct {
	store *memstore.Store
}

func (projection testWorkspaceProjection) WorkspaceData(ctx context.Context, workspaceKey string) (*operationalview.Workspace, error) {
	return storeadapter.BuildWorkspaceDataForKey(ctx, projection.store, workspaceKey)
}

func (projection testWorkspaceProjection) WorkspacePath(ctx context.Context, workspaceKey string) string {
	return storeadapter.ResolveOrHealWorkspacePath(ctx, projection.store, workspaceKey)
}

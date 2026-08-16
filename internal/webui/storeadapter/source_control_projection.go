package storeadapter

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/app/query/operationalview"
)

// SourceControlWorkspaceProjection adapts the legacy Workspace read model to
// the two exact queries needed by machine-local Source Control mechanics.
type SourceControlWorkspaceProjection struct {
	topology WorkspaceTopologyReader
}

// NewSourceControlWorkspaceProjection creates the narrow read projection.
func NewSourceControlWorkspaceProjection(topology WorkspaceTopologyReader) SourceControlWorkspaceProjection {
	return SourceControlWorkspaceProjection{topology: topology}
}

// WorkspaceData returns canonical workspace topology.
func (projection SourceControlWorkspaceProjection) WorkspaceData(
	ctx context.Context,
	workspaceKey string,
) (*operationalview.Workspace, error) {
	return BuildWorkspaceDataForKey(ctx, projection.topology, workspaceKey)
}

// WorkspacePath resolves the canonical on-disk workspace placement.
func (projection SourceControlWorkspaceProjection) WorkspacePath(ctx context.Context, workspaceKey string) string {
	return ResolveOrHealWorkspacePath(ctx, projection.topology, workspaceKey)
}

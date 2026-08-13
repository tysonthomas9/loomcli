package storeadapter

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/ops"
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
) (*ops.WorkspaceData, error) {
	return BuildWorkspaceDataForKey(ctx, projection.topology, workspaceKey)
}

// WorkspacePath resolves the canonical on-disk workspace placement.
func (projection SourceControlWorkspaceProjection) WorkspacePath(ctx context.Context, workspaceKey string) string {
	return ResolveOrHealWorkspacePath(ctx, projection.topology, workspaceKey)
}

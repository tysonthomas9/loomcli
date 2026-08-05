package capabilitycomposition

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/store"
	workspacehandler "github.com/tysonthomas9/loomcli/internal/webui/handlers/workspace"
	"github.com/tysonthomas9/loomcli/internal/webui/storeadapter"
)

type workspaceTopologyReader interface {
	GetWorkspace(context.Context, string) (*ops.WorkspaceData, error)
}

// WorkspaceHTTPProjection composes machine-local paths and cross-capability
// UI topology without granting those concerns Workspace mutation authority.
type WorkspaceHTTPProjection struct {
	store    store.WorkspaceStore
	topology workspaceTopologyReader
}

var _ workspacehandler.CatalogProjection = (*WorkspaceHTTPProjection)(nil)

func NewWorkspaceHTTPProjection(st store.WorkspaceStore, topology workspaceTopologyReader) *WorkspaceHTTPProjection {
	return &WorkspaceHTTPProjection{store: st, topology: topology}
}

func (p *WorkspaceHTTPProjection) ActiveWorkspaceKey(ctx context.Context) string {
	if p == nil || p.store == nil {
		return ""
	}
	return storeadapter.ActiveWorkspaceKey(ctx, p.store)
}

func (p *WorkspaceHTTPProjection) WorkspacePath(key string) string {
	return storeadapter.ResolveWorkspacePath(key)
}

func (p *WorkspaceHTTPProjection) WorkspaceTopology(ctx context.Context, key string) (*ops.WorkspaceData, error) {
	return p.topology.GetWorkspace(ctx, key)
}

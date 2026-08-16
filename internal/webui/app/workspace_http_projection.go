package app

import (
	"context"

	workspaceowner "github.com/tysonthomas9/loomcli/internal/modules/workspace"

	"github.com/tysonthomas9/loomcli/internal/app/query/operationalview"
	workspacehandler "github.com/tysonthomas9/loomcli/internal/webui/handlers/workspace"
	"github.com/tysonthomas9/loomcli/internal/webui/storeadapter"
)

type workspaceTopologyReader interface {
	GetWorkspace(context.Context, string) (*operationalview.Workspace, error)
}

// WorkspaceHTTPProjection composes machine-local paths and cross-capability
// UI topology without granting those concerns Workspace mutation authority.
type WorkspaceHTTPProjection struct {
	store    workspaceowner.WorkspaceStore
	topology workspaceTopologyReader
}

var _ workspacehandler.CatalogProjection = (*WorkspaceHTTPProjection)(nil)

func NewWorkspaceHTTPProjection(st workspaceowner.WorkspaceStore, topology workspaceTopologyReader) *WorkspaceHTTPProjection {
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

func (p *WorkspaceHTTPProjection) WorkspaceTopology(ctx context.Context, key string) (*operationalview.Workspace, error) {
	return p.topology.GetWorkspace(ctx, key)
}

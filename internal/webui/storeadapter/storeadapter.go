// Package storeadapter exposes operationalview.Workspace-shaped views over narrow
// persisted workspace topology collections.
package storeadapter

import (
	"context"
	"errors"

	workspaceowner "github.com/tysonthomas9/loomcli/internal/modules/workspace"

	"github.com/tysonthomas9/loomcli/internal/app/query/operationalview"
	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/platform/persistence"
)

type workspaceRepositoryStore interface {
	Workspaces() workspaceowner.WorkspaceStore
	Repos() workspaceowner.RepoStore
}

// WorkspaceTopologyReader is the read surface required to compose Workspace
// and Repository projections for the legacy WorkspaceData transport shape.
type WorkspaceTopologyReader interface {
	Workspaces() workspaceowner.WorkspaceStore
	Repos() workspaceowner.RepoStore
}

// ActiveWorkspaceKey projects the explicit runtime workspace selection for UI
// composition. An unavailable selection is represented as the empty key.
func ActiveWorkspaceKey(ctx context.Context, workspaces workspaceowner.WorkspaceStore) string {
	key, _ := bootstrap.ResolveActiveWorkspaceKey(ctx, workspaces)
	return key
}

// BuildActiveWorkspaceData materializes the active workspace topology as
// an *operationalview.Workspace using the supplied Store. The "active" workspace
// key comes from the explicit runtime workspace. If no runtime workspace is
// set, the web UI falls back to the first workspace for initial rendering.
//
// Returns nil, nil when no workspace exists — single-repo /
// un-initialized mode.
func BuildActiveWorkspaceData(ctx context.Context, s WorkspaceTopologyReader) (*operationalview.Workspace, error) {
	if s == nil {
		return nil, nil
	}
	return newWorkspaceTopologyQuery(s).Active(ctx)
}

// BuildWorkspaceDataForKey materializes a specific workspace's topology
// as an *operationalview.Workspace. Includes per-workspace repo + agent lists
// plus a summary list of all workspaces (used by the workspace switcher
// in the frontend).
//
// Returns ErrNotFound (wrapped) if the workspace key does not exist.
func BuildWorkspaceDataForKey(ctx context.Context, s WorkspaceTopologyReader, key string) (*operationalview.Workspace, error) {
	if s == nil {
		return nil, errors.New("storeadapter: nil store")
	}
	return newWorkspaceTopologyQuery(s).ByKey(ctx, key)
}

// ListWorkspacePaths returns a map of workspace-key -> on-disk path for
// every workspace registered in the store. Used by the webui's workspace
// reconciliation + health doctor goroutines to enumerate live targets.
//
// Paths come from the per-machine state cache; workspaces missing from
// the cache map to "" so callers can fall back to defaults.
func ListWorkspacePaths(ctx context.Context, s workspaceRepositoryStore) (map[string]string, error) {
	if s == nil {
		return nil, nil
	}
	return newWorkspaceTopologyQuery(s).Paths(ctx)
}

// ResolveWorkspaceKeyByName looks up a workspace by display name and
// returns its stable Key (UUID-like uppercase identifier).
func ResolveWorkspaceKeyByName(ctx context.Context, s workspaceRepositoryStore, name string) (string, error) {
	if s == nil {
		return "", errors.New("storeadapter: nil store")
	}
	return newWorkspaceTopologyQuery(s).ResolveKey(ctx, name)
}

func newWorkspaceTopologyQuery(s WorkspaceTopologyReader) operationalview.WorkspaceTopologyQuery {
	return operationalview.NewWorkspaceTopologyQuery(
		s.Workspaces(), s.Repos(), localWorkspacePlacement{},
		func(ctx context.Context) (string, error) {
			key, err := bootstrap.ResolveActiveWorkspaceKey(ctx, s.Workspaces())
			if errors.Is(err, bootstrap.ErrNoActiveWorkspace) || errors.Is(err, persistence.ErrNotFound) {
				return "", nil
			}
			return key, err
		},
	)
}

type localWorkspacePlacement struct{}

func (localWorkspacePlacement) WorkspacePath(key string) string { return resolveWorkspacePath(key) }
func (localWorkspacePlacement) RepositoryPath(workspaceKey, repository string) string {
	return resolveRepoPath(workspaceKey, repository)
}
func (localWorkspacePlacement) Backend(key string) string {
	backend, _ := bootstrap.RuntimeProvider(key)
	return backend
}

// resolveWorkspacePath looks up the per-machine workspace path from the
// state cache. Returns "" if the cache is unreadable or has no entry.
func resolveWorkspacePath(key string) string {
	sc, err := bootstrap.LoadStateCache()
	if err == nil && sc != nil {
		if local, ok := sc.Workspaces[key]; ok && local.Path != "" {
			return local.Path
		}
	}
	return ""
}

// ResolveWorkspacePath exposes the local path lookup for list endpoints that
// need workspace summaries without materializing full WorkspaceData.
func ResolveWorkspacePath(key string) string {
	return resolveWorkspacePath(key)
}

// resolveRepoPath looks up the per-machine on-disk path for a repo within
// a workspace from the state cache. Returns "" if missing.
func resolveRepoPath(wsKey, repoName string) string {
	sc, err := bootstrap.LoadStateCache()
	if err == nil && sc != nil {
		if local, ok := sc.Workspaces[wsKey]; ok {
			if path, ok := local.Repos[repoName]; ok && path != "" {
				return path
			}
		}
	}
	return ""
}

// ResolveRepoPath exposes the machine-local checkout path recorded for a
// workspace repository. Callers must still validate that the path is a Git
// checkout for the expected canonical remote before using repository data.
func ResolveRepoPath(wsKey, repoName string) string {
	return resolveRepoPath(wsKey, repoName)
}

// Package storeadapter exposes ops.WorkspaceData-shaped views over a
// store.Store handle.
package storeadapter

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/localnodeconfig"
	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// BuildActiveWorkspaceData materializes the active workspace topology as
// an *ops.WorkspaceData using the supplied Store. The "active" workspace
// key comes from the explicit runtime workspace. If no runtime workspace is
// set, the web UI falls back to the first workspace for initial rendering.
//
// Returns nil, nil when no workspace exists — single-repo /
// un-initialized mode.
func BuildActiveWorkspaceData(ctx context.Context, s store.Store) (*ops.WorkspaceData, error) {
	if s == nil {
		return nil, nil
	}
	key, err := bootstrap.ResolveActiveWorkspaceKey(ctx, s.Workspaces())
	if err != nil {
		if errors.Is(err, bootstrap.ErrNoActiveWorkspace) || errors.Is(err, domain.ErrNotFound) {
			key, err = firstWorkspaceKey(ctx, s)
			if err != nil || key == "" {
				return nil, err
			}
			return BuildWorkspaceDataForKey(ctx, s, key)
		}
		return nil, err
	}
	return BuildWorkspaceDataForKey(ctx, s, key)
}

// BuildWorkspaceDataForKey materializes a specific workspace's topology
// as an *ops.WorkspaceData. Includes per-workspace repo + agent lists
// plus a summary list of all workspaces (used by the workspace switcher
// in the frontend).
//
// Returns ErrNotFound (wrapped) if the workspace key does not exist.
func BuildWorkspaceDataForKey(ctx context.Context, s store.Store, key string) (*ops.WorkspaceData, error) {
	if s == nil {
		return nil, errors.New("storeadapter: nil store")
	}
	ws, err := s.Workspaces().Get(ctx, key)
	if err != nil {
		return nil, err
	}
	repos, groups, err := loadRepos(ctx, s, ws.Key)
	if err != nil {
		return nil, err
	}
	summaries, err := loadSummaries(ctx, s, ws.Key)
	if err != nil {
		return nil, err
	}

	wsPath := resolveWorkspacePath(ws.Key)

	return &ops.WorkspaceData{
		ID:               ws.Key,
		Name:             ws.Name,
		Path:             wsPath,
		Repos:            repos,
		Groups:           groups,
		Agents:           []ops.WorkspaceAgentInfo{},
		Workspaces:       summaries,
		WorkspaceOrder:   nil,
		DefaultWorkspace: "",
		DesignFormat:     ws.DesignFormat,
	}, nil
}

// ListWorkspacePaths returns a map of workspace-key -> on-disk path for
// every workspace registered in the store. Used by the webui's workspace
// reconciliation + health doctor goroutines to enumerate live targets.
//
// Paths come from the per-machine state cache; workspaces missing from
// the cache map to "" so callers can fall back to defaults.
func ListWorkspacePaths(ctx context.Context, s store.Store) (map[string]string, error) {
	if s == nil {
		return nil, nil
	}
	all, err := s.Workspaces().List(ctx)
	if err != nil {
		return nil, fmt.Errorf("storeadapter: list workspaces: %w", err)
	}
	if len(all) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(all))
	for _, ws := range all {
		out[ws.Key] = resolveWorkspacePath(ws.Key)
	}
	return out, nil
}

// ResolveWorkspaceKeyByName looks up a workspace by display name and
// returns its stable Key (UUID-like uppercase identifier).
func ResolveWorkspaceKeyByName(ctx context.Context, s store.Store, name string) (string, error) {
	if s == nil {
		return "", errors.New("storeadapter: nil store")
	}
	// Try Get first because generated keys can also be valid workspace names.
	if ws, err := s.Workspaces().Get(ctx, name); err == nil {
		return ws.Key, nil
	}
	ws, err := s.Workspaces().GetByName(ctx, name)
	if err != nil {
		return "", err
	}
	return ws.Key, nil
}

func loadRepos(ctx context.Context, s store.Store, wsKey string) ([]ops.WorkspaceRepo, []string, error) {
	repos, err := s.Repos().List(ctx, wsKey)
	if err != nil {
		return nil, nil, fmt.Errorf("storeadapter: list repos: %w", err)
	}
	groupSet := make(map[string]bool)
	out := make([]ops.WorkspaceRepo, 0, len(repos))
	wsRoot := resolveWorkspacePath(wsKey)
	for _, r := range repos {
		db := r.DefaultBranch
		if db == "" {
			db = "main"
		}
		remote := r.Remote
		if remote == "" {
			remote = "origin"
		}
		// Best-effort path resolution: state cache should have an entry.
		repoPath := resolveRepoPath(wsKey, r.Name)
		if repoPath == "" && wsRoot != "" {
			repoPath = filepath.Join(wsRoot, r.Name)
		}
		out = append(out, ops.WorkspaceRepo{
			Name:          r.Name,
			Path:          repoPath,
			DefaultBranch: db,
			Remote:        remote,
			RemoteURL:     r.RemoteURL,
			SourceRepoID:  r.SourceRepoID,
			Groups:        r.Groups,
		})
		for _, g := range r.Groups {
			groupSet[g] = true
		}
	}
	groups := make([]string, 0, len(groupSet))
	for g := range groupSet {
		groups = append(groups, g)
	}
	sort.Strings(groups)
	return out, groups, nil
}

func loadSummaries(ctx context.Context, s store.Store, activeKey string) ([]ops.WorkspaceSummary, error) {
	all, err := s.Workspaces().List(ctx)
	if err != nil {
		return nil, fmt.Errorf("storeadapter: list workspaces: %w", err)
	}
	out := make([]ops.WorkspaceSummary, 0, len(all))
	for _, ws := range all {
		repoCount := 0
		if repos, repoErr := s.Repos().List(ctx, ws.Key); repoErr == nil {
			repoCount = len(repos)
		}
		backend, _ := localnodeconfig.RuntimeProvider(ws.Key)
		out = append(out, ops.WorkspaceSummary{
			ID:           ws.Key,
			Name:         ws.Name,
			Path:         resolveWorkspacePath(ws.Key),
			Active:       ws.Key == activeKey,
			RepoCount:    repoCount,
			IsDefault:    false,
			Backend:      backend,
			State:        string(ws.State),
			ErrorMessage: ws.ErrorMessage,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func firstWorkspaceKey(ctx context.Context, s store.Store) (string, error) {
	all, err := s.Workspaces().List(ctx)
	if err != nil {
		return "", fmt.Errorf("storeadapter: list workspaces: %w", err)
	}
	if len(all) == 0 {
		return "", nil
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Name < all[j].Name })
	return all[0].Key, nil
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

// DefaultWorkspaceKey is retained for compatibility with older callers.
// Default workspace selection has been removed, so it always returns empty.
func DefaultWorkspaceKey() string {
	return ""
}

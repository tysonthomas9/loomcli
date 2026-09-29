// Package serveadapter builds store-backed workspace operations for webui serve.
package serveadapter

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/domain"
	loomworkspace "github.com/tysonthomas9/loomcli/internal/loomgit/workspace"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// BuildWorkspaceIDResolverFn returns a closure satisfying
// webui.ServerConfig.WorkspaceIDResolverFn — name→key (or pass-through
// if name == key). In fleet-db mode key IS name for workspaces created
// via `loom workspace add`; the resolver is a thin existence check.
func BuildWorkspaceIDResolverFn(s store.Store) func(string) (string, error) {
	if s == nil {
		return nil
	}
	return func(name string) (string, error) {
		ctx := context.Background()
		// Try direct key lookup first — the dominant case.
		if ws, err := s.Workspaces().Get(ctx, name); err == nil && ws != nil {
			return ws.Key, nil
		} else if !errors.Is(err, domain.ErrNotFound) {
			return "", err
		}
		// Fallback: name lookup for workspaces with distinct Name vs Key.
		if ws, err := s.Workspaces().GetByName(ctx, name); err == nil && ws != nil {
			return ws.Key, nil
		}
		return "", fmt.Errorf("workspace %q not found", name)
	}
}

// ResolveInitialWorkspaceID returns the explicit workspace key (LOOM_WORKSPACE)
// or "" when no workspace is active. Used as the
// InitialWorkspaceID for the webui server bootstrap.
func ResolveInitialWorkspaceID(s store.Store) string {
	if s == nil {
		return ""
	}
	key, err := bootstrap.ResolveActiveWorkspaceKey(context.Background(), s.Workspaces())
	if err != nil {
		return ""
	}
	return key
}

// WorkspaceConfig resolves FleetDB repos against this machine's local paths.
func WorkspaceConfig(ctx context.Context, s store.Store, key string) (config.WorkspaceConfig, error) {
	sc, err := bootstrap.LoadStateCache()
	if err != nil {
		return config.WorkspaceConfig{}, err
	}
	local := bootstrap.WorkspaceLocalState{}
	if sc != nil {
		local = sc.Workspaces[key]
	}
	if !filepath.IsAbs(local.Path) {
		return config.WorkspaceConfig{}, fmt.Errorf("workspace %q has no absolute local path", key)
	}
	repoRows, err := s.Repos().List(ctx, key)
	if err != nil {
		return config.WorkspaceConfig{}, err
	}
	repos := make([]config.RepoConfig, 0, len(repoRows))
	for _, row := range repoRows {
		if row == nil {
			continue
		}
		path := local.Repos[row.Name]
		if path == "" && local.Path != "" {
			path = filepath.Join(local.Path, row.Name)
		}
		repos = append(repos, config.RepoConfig{Name: row.Name, Path: path, SourceRepoID: row.SourceRepoID})
	}
	return config.WorkspaceConfig{ID: key, Path: local.Path, Repos: repos}, nil
}

func BuildWorkspaceDeletePreviewFn(s store.Store) func(string) (service.WorkspaceDeletePreview, error) {
	if s == nil {
		return nil
	}
	return func(key string) (service.WorkspaceDeletePreview, error) {
		ctx := context.Background()
		ws, err := WorkspaceConfig(ctx, s, key)
		if err != nil {
			return service.WorkspaceDeletePreview{}, err
		}
		preview, err := loomworkspace.DryRun(ctx, ws)
		if err != nil {
			return service.WorkspaceDeletePreview{}, err
		}
		items := make([]service.WorkspaceDeleteItem, 0, len(preview.Items))
		for _, item := range preview.Items {
			items = append(items, service.WorkspaceDeleteItem{
				Repo: item.Repo, Path: item.Path, Kind: item.Kind,
				Detail: item.Detail, Size: item.Size,
			})
		}
		return service.WorkspaceDeletePreview{Items: items, Fingerprint: preview.Fingerprint}, nil
	}
}

// BuildWorkspaceDeleteConfirmedFn is the shared UI/CLI deletion entry point.
func BuildWorkspaceDeleteConfirmedFn(s store.Store) func(string, string) error {
	if s == nil {
		return nil
	}
	return func(key, fingerprint string) error {
		ctx := context.Background()
		ws, err := WorkspaceConfig(ctx, s, key)
		if err != nil {
			return err
		}
		return loomworkspace.DeleteWorkspace(ctx, ws, fingerprint, func(ctx context.Context, key string) error {
			if err := s.Workspaces().Delete(ctx, key); err != nil {
				return err
			}
			return deleteWorkspaceLocalState(key)
		})
	}
}

func deleteWorkspaceLocalState(key string) error {
	if key == "" {
		return nil
	}
	return bootstrap.MutateStateCache(func(sc *bootstrap.StateCache) error {
		if sc.Workspaces != nil {
			delete(sc.Workspaces, key)
		}
		if sc.LastWorkspace == key {
			sc.LastWorkspace = ""
		}
		return nil
	})
}

// BuildSetDefaultWorkspaceFn is retained for compatibility with older server
// wiring. Default workspace selection is disabled in the service layer.
func BuildSetDefaultWorkspaceFn(s store.Store) func(string) error {
	if s == nil {
		return nil
	}
	return func(key string) error {
		// Validate the workspace exists before recording.
		if _, err := s.Workspaces().Get(context.Background(), key); err != nil {
			return err
		}
		return bootstrap.SetActiveWorkspaceKey(key)
	}
}

// BuildClearDefaultWorkspaceFn is retained for compatibility with older server
// wiring. Default workspace selection is disabled in the service layer.
func BuildClearDefaultWorkspaceFn() func() error {
	return bootstrap.ClearActiveWorkspaceKey
}

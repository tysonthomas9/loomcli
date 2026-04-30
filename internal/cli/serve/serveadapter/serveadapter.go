// Package serveadapter builds the workspace-related closures
// webui.ServerConfig expects from a fleet-db Store handle. Replaces the
// yaml-backed adapters in internal/cli/serve/workspacemgr (loomcli-26v50.23
// + .25): the read path (config, list, resolver, initial ID) goes through
// the store; write paths that touch disk (clone repos, create worktrees)
// remain in workspacemgr until the disk-side flow is reworked.
package serveadapter

import (
	"context"
	"errors"
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/storeadapter"
)

// BuildWorkspaceConfigFn returns a closure satisfying
// webui.ServerConfig.WorkspaceConfigFn (active-workspace topology).
// Returns nil, nil when no workspaces exist (single-repo mode preserved).
func BuildWorkspaceConfigFn(s store.Store) func() (*ops.WorkspaceData, error) {
	if s == nil {
		return nil
	}
	return func() (*ops.WorkspaceData, error) {
		return storeadapter.BuildActiveWorkspaceData(context.Background(), s)
	}
}

// BuildWorkspaceConfigByIDFn returns a closure satisfying
// webui.ServerConfig.WorkspaceConfigByIDFn (topology by workspace key).
func BuildWorkspaceConfigByIDFn(s store.Store) func(string) (*ops.WorkspaceData, error) {
	if s == nil {
		return nil
	}
	return func(key string) (*ops.WorkspaceData, error) {
		return storeadapter.BuildWorkspaceDataForKey(context.Background(), s, key)
	}
}

// BuildWorkspaceListFn returns a closure satisfying
// webui.ServerConfig.WorkspaceListFn — id→path map. Path is empty in
// fleet-db mode (workspace state is server-side; per-machine paths
// belong on the local checkout cache, not the workspace record). The
// webui treats an empty path as "no local checkout" and renders the
// workspace anyway.
func BuildWorkspaceListFn(s store.Store) func() (map[string]string, error) {
	if s == nil {
		return nil
	}
	return func() (map[string]string, error) {
		ctx := context.Background()
		list, err := s.Workspaces().List(ctx)
		if err != nil {
			return nil, err
		}
		out := make(map[string]string, len(list))
		for _, ws := range list {
			out[ws.Key] = "" // no per-machine path in fleet-db mode
		}
		return out, nil
	}
}

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
		// Fallback: name lookup for legacy workspaces with distinct
		// Name vs Key.
		if ws, err := s.Workspaces().GetByName(ctx, name); err == nil && ws != nil {
			return ws.Key, nil
		}
		return "", fmt.Errorf("workspace %q not found", name)
	}
}

// ResolveInitialWorkspaceID returns the active workspace key (LOOM_WORKSPACE
// env > state cache) or "" when no workspace is active. Used as the
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

// BuildWorkspaceDeleteFn returns a closure satisfying
// webui.ServerConfig.WorkspaceDeleteFn — store-backed delete. The
// store cascades repo/agent/role/daemon-profile deletion server-side;
// disk-side worktree teardown is the user's responsibility (the
// noun-verb CLI prompts before clobbering checkouts; the webui's
// "delete workspace" is metadata-only by design).
func BuildWorkspaceDeleteFn(s store.Store) func(string) error {
	if s == nil {
		return nil
	}
	return func(key string) error {
		return s.Workspaces().Delete(context.Background(), key)
	}
}

// BuildSetDefaultWorkspaceFn satisfies SetDefaultWorkspaceFn by
// recording the choice in the per-user state cache (regenerable;
// cloud deployments will eventually move this to a per-user
// preference store, but state.json suffices for single-user local
// mode).
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

// BuildClearDefaultWorkspaceFn clears the per-user active-workspace
// hint in the state cache.
func BuildClearDefaultWorkspaceFn() func() error {
	return func() error {
		return bootstrap.SetActiveWorkspaceKey("")
	}
}

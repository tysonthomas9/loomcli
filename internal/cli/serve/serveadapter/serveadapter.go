// Package serveadapter builds store-backed workspace operations for webui serve.
package serveadapter

import (
	"context"
	"errors"
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
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

// BuildWorkspaceDeleteFn returns a closure satisfying
// webui.ServerConfig.WorkspaceDeleteFn — store-backed delete. The
// store cascades repo/agent/role/daemon-profile deletion server-side.
// Local checkout paths are removed from the per-machine state cache so
// selected-workspace hints and path lookups cannot point at a deleted workspace.
func BuildWorkspaceDeleteFn(s store.Store) func(string) error {
	if s == nil {
		return nil
	}
	return func(key string) error {
		ctx := context.Background()
		if err := s.Workspaces().Delete(ctx, key); err != nil {
			return err
		}
		return deleteWorkspaceLocalState(key)
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

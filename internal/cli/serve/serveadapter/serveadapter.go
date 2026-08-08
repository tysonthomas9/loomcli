// Package serveadapter builds store-backed workspace operations for webui serve.
package serveadapter

import (
	"context"
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
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
		} else if !store.IsNotFound(err) {
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

func deleteWorkspaceLocalState(key string) error {
	if key == "" {
		return nil
	}
	return bootstrap.RemoveWorkspaceLocalState(key)
}

// BuildWorkspaceDeleteCleanupFn returns the machine-local cleanup half of a
// Workspace deletion. The durable record must already have been removed by
// the Workspace owner command.
func BuildWorkspaceDeleteCleanupFn() func(string) error {
	return deleteWorkspaceLocalState
}

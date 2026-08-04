package cmdstore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/infra/workspacecatalog"
	workspacemodule "github.com/tysonthomas9/loomcli/internal/modules/workspace"
)

// WorkspaceCatalog composes the Workspace owner API over the shared Store
// handle. CLI adapters use this helper instead of reaching into Workspace and
// Repository persistence sub-stores directly.
func WorkspaceCatalog(handle *bootstrap.StoreHandle) (workspacemodule.API, error) {
	if handle == nil || handle.Store == nil {
		return nil, fmt.Errorf("compose Workspace capability: %w", workspacemodule.ErrUnavailable)
	}
	api, err := workspacecatalog.New(handle.Store.Workspaces(), handle.Store.Repos())
	if err != nil {
		return nil, fmt.Errorf("compose Workspace capability: %w", err)
	}
	if api == nil {
		return nil, fmt.Errorf("compose Workspace capability: %w", workspacemodule.ErrUnavailable)
	}
	return api, nil
}

// ActiveWorkspaceCatalog resolves the explicit process-scoped Workspace
// selection through the owner API and returns its canonical key.
func ActiveWorkspaceCatalog(ctx context.Context, api workspacemodule.API) (string, error) {
	key := strings.TrimSpace(os.Getenv(bootstrap.EnvWorkspace))
	if key == "" {
		return "", bootstrap.ErrNoActiveWorkspace
	}
	if api == nil {
		return "", fmt.Errorf("validate active workspace %q: %w", key, workspacemodule.ErrUnavailable)
	}
	workspace, err := api.Resolve(ctx, workspacemodule.ResolveQuery{Reference: key})
	if err != nil {
		if errors.Is(err, workspacemodule.ErrNotFound) {
			return "", fmt.Errorf("active workspace %q not found in fleet-db: %w", key, err)
		}
		return "", fmt.Errorf("validate active workspace %q: %w", key, err)
	}
	return workspace.Key, nil
}

func WithWorkspaceCatalog(fn func(context.Context, *bootstrap.StoreHandle, workspacemodule.API) error) error {
	return WithStore(func(ctx context.Context, handle *bootstrap.StoreHandle) error {
		api, err := WorkspaceCatalog(handle)
		if err != nil {
			return err
		}
		return fn(ctx, handle, api)
	})
}

func WithActiveWorkspaceCatalog(fn func(context.Context, *bootstrap.StoreHandle, workspacemodule.API, string) error) error {
	return WithWorkspaceCatalog(func(ctx context.Context, handle *bootstrap.StoreHandle, api workspacemodule.API) error {
		workspace, err := ActiveWorkspaceCatalog(ctx, api)
		if err != nil {
			return err
		}
		return fn(ctx, handle, api, workspace)
	})
}

package workspacemgr

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/workspace"
)

// EnsureProjectRegistered calls workspace.EnsureCurrentProjectRegistered to
// register the current project in workspace config.
func EnsureProjectRegistered() {
	workspace.EnsureCurrentProjectRegistered()
}

// EnsureDaemonsForAllWorkspaces activates ready configured workspaces for
// FleetDB-backed subscribers.
func EnsureDaemonsForAllWorkspaces(ctx context.Context, onReady func(wsID string)) {
	workspace.EnsureDaemonsForAllWorkspaces(cli.GetDeps(nil), ctx, onReady)
}

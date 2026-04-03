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

// EnsureDaemonsForAllWorkspaces auto-starts bd daemons for every configured
// workspace (other than the CWD, which is managed by the main serve loop).
func EnsureDaemonsForAllWorkspaces(ctx context.Context) {
	workspace.EnsureDaemonsForAllWorkspaces(cli.GetDeps(nil), ctx)
}

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
// When onReady is non-nil, it is invoked with the workspace UUID after each
// daemon is confirmed reachable.
func EnsureDaemonsForAllWorkspaces(ctx context.Context, onReady func(wsID string)) {
	workspace.EnsureDaemonsForAllWorkspaces(cli.GetDeps(nil), ctx, onReady)
}

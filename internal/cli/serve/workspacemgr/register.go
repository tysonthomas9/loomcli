package workspacemgr

import "github.com/tysonthomas9/loomcli/internal/cli/workspace"

// EnsureProjectRegistered calls workspace.EnsureCurrentProjectRegistered to
// register the current project in workspace config.
func EnsureProjectRegistered() {
	workspace.EnsureCurrentProjectRegistered()
}

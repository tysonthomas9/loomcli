package trigger

import workspaceowner "github.com/tysonthomas9/loomcli/internal/modules/workspace"

// workspaceLister is the workspace query required by runtime sweepers.
type workspaceLister interface {
	Workspaces() workspaceowner.WorkspaceStore
}

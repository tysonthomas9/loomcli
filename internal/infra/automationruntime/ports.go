package trigger

import "github.com/tysonthomas9/loomcli/internal/store"

// workspaceLister is the workspace query required by runtime sweepers.
type workspaceLister interface {
	Workspaces() store.WorkspaceStore
}

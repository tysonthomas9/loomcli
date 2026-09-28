package loomgit

import "github.com/tysonthomas9/loomcli/internal/loomgit/internal/layout/refname"

// InteractiveBranch is the working-area branch for a workspace lead.
func InteractiveBranch(workspace, lead string) (string, error) {
	return refname.InteractiveBranch(workspace, lead)
}

// CheckWorkspaceNamespace rejects user branches that block Loom's refs.
func CheckWorkspaceNamespace(repo, workspace string) error {
	return refname.CheckNamespace(repo, workspace)
}

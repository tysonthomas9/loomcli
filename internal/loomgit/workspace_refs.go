package loomgit

import (
	"strconv"

	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/layout/refname"
)

// InteractiveBranch is the working-area branch for a workspace lead.
func InteractiveBranch(workspace, lead string) (string, error) {
	return refname.InteractiveBranch(workspace, lead)
}

func TaskCopyBranch(workspace, taskCopy string) (string, error) {
	return refname.TaskCopyBranch(workspace, taskCopy)
}

// InteractiveIdentity validates and decodes a lead working-area branch.
func InteractiveIdentity(name string) (workspace, lead string, ok bool) {
	return refname.InteractiveIdentity(name)
}

// CheckWorkspaceNamespace rejects user branches that block Loom's refs.
func CheckWorkspaceNamespace(repo, workspace string) error {
	return refname.CheckNamespace(repo, workspace)
}

// PRHead is the hidden ref that holds a fetched pull request head.
func PRHead(workspace string, number int) (string, error) {
	return refname.PRHead(workspace, strconv.Itoa(number))
}

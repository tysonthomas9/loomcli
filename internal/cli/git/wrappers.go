package git

import "github.com/tysonthomas9/loomcli/internal/cli"

// RunGitCommand executes a git command in the specified directory using defaultDeps.
func RunGitCommand(dir string, args ...string) (string, error) {
	return runGit(defaultDeps, dir, args...)
}

// RunGitCommandWithOutput executes a git command and streams output to stdout/stderr.
func RunGitCommandWithOutput(dir string, args ...string) error {
	return runGitOutput(defaultDeps, dir, args...)
}

func GitFetch(dir string) error                  { return gitFetch(defaultDeps, dir) }
func GitCheckout(dir, branch string) error       { return gitCheckout(defaultDeps, dir, branch) }
func GitPull(dir, branch string) error           { return gitPull(defaultDeps, dir, branch) }
func GitMerge(dir, branch, message string) error { return gitMerge(defaultDeps, dir, branch, message) }
func GitMergeOrigin(dir, branch, message string) error {
	return gitMergeOrigin(defaultDeps, dir, branch, message)
}
func GitPush(dir, branch string) error          { return gitPush(defaultDeps, dir, branch) }
func GitPushForce(dir, branch string) error     { return gitPushForce(defaultDeps, dir, branch) }
func GitReset(dir, ref string) error            { return gitReset(defaultDeps, dir, ref) }
func GitClean(dir string) error                 { return gitClean(defaultDeps, dir) }
func GitCleanDryRun(dir string) (string, error) { return gitCleanDryRun(defaultDeps, dir) }

func GitCleanExclude(dir string, excludes []string) error {
	return gitCleanExclude(defaultDeps, dir, excludes)
}

func GitCleanDryRunExclude(dir string, excludes []string) (string, error) {
	return gitCleanDryRunExclude(defaultDeps, dir, excludes)
}

func GetConflictedFiles(dir string) ([]string, error) {
	return getConflictedFilesDeps(defaultDeps, dir)
}

func HasCommitsBetween(dir, target, source string) (bool, error) {
	return hasCommitsBetweenDeps(defaultDeps, dir, target, source)
}

func IsCleanWorkingTree(dir string) (bool, error) {
	return IsCleanWorkingTreeDeps(defaultDeps, dir)
}

func GitFetchRemote(dir, remote string) error {
	return gitFetchRemote(defaultDeps, dir, remote)
}

func GitMergeRemote(dir, remote, branch, message string) error {
	return gitMergeRemote(defaultDeps, dir, remote, branch, message)
}

func GitPushRemote(dir, remote, branch string) error {
	return gitPushRemote(defaultDeps, dir, remote, branch)
}

func GitPullRemote(dir, remote, branch string) error {
	return gitPullRemote(defaultDeps, dir, remote, branch)
}

func HasCommitsBetweenRemote(dir, remote, target, source string) (bool, error) {
	return hasCommitsBetweenRemoteDeps(defaultDeps, dir, remote, target, source)
}

func IsRefCheckedOutInWorktree(dir, branch string) (bool, string, error) {
	return isRefCheckedOutInWorktreeDeps(defaultDeps, dir, branch)
}

func GitCheckoutDetached(dir, ref string) error {
	return gitCheckoutDetached(defaultDeps, dir, ref)
}

func GitCreateBranchFromHead(dir, name string) error {
	return gitCreateBranchFromHead(defaultDeps, dir, name)
}

func GitDeleteBranch(dir, name string, force bool) error {
	return gitDeleteBranch(defaultDeps, dir, name, force)
}

func GitPushRefspec(dir, remote, localRef, remoteRef string) error {
	return gitPushRefspec(defaultDeps, dir, remote, localRef, remoteRef)
}

func GitStash(dir string) (bool, error) { return gitStash(defaultDeps, dir) }
func GitStashPop(dir string) error      { return gitStashPop(defaultDeps, dir) }

func BranchExistsLocally(dir, branch string) (bool, error) {
	return branchExistsLocallyDeps(defaultDeps, dir, branch)
}

func RemoteBranchExists(dir, remote, branch string) (bool, error) {
	return remoteBranchExistsDeps(defaultDeps, dir, remote, branch)
}

func GitCheckoutNewFromRef(dir, branch, startPoint string) error {
	return gitCheckoutNewFromRef(defaultDeps, dir, branch, startPoint)
}

func GitMergeAbort(dir string) error {
	return gitMergeAbort(defaultDeps, dir)
}

func HasUnmergedFiles(dir string) (bool, error) {
	return hasUnmergedFilesDeps(defaultDeps, dir)
}

func GetCurrentBranch(path string) (string, error) {
	return cli.GetCurrentBranch(path)
}

func getStashCount(dir string) (int, error) {
	return getStashCountDeps(defaultDeps, dir)
}

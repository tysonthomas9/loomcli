package git

import "github.com/tysonthomas9/loomcli/internal/cli"

// RunGitCommand executes a git command in the specified directory using defaultDeps.
func RunGitCommand(dir string, args ...string) (string, error) {
	return runGit(ensureDefaultDeps(), dir, args...)
}

// RunGitCommandWithOutput executes a git command and streams output to stdout/stderr.
func RunGitCommandWithOutput(dir string, args ...string) error {
	return runGitOutput(ensureDefaultDeps(), dir, args...)
}

func GitFetch(dir string) error            { return gitFetch(ensureDefaultDeps(), dir) }
func GitCheckout(dir, branch string) error { return gitCheckout(ensureDefaultDeps(), dir, branch) }
func GitPull(dir, branch string) error     { return gitPull(ensureDefaultDeps(), dir, branch) }
func GitMerge(dir, branch, message string) error {
	return gitMerge(ensureDefaultDeps(), dir, branch, message)
}
func GitMergeOrigin(dir, branch, message string) error {
	return gitMergeOrigin(ensureDefaultDeps(), dir, branch, message)
}
func GitPush(dir, branch string) error          { return gitPush(ensureDefaultDeps(), dir, branch) }
func GitPushForce(dir, branch string) error     { return gitPushForce(ensureDefaultDeps(), dir, branch) }
func GitReset(dir, ref string) error            { return gitReset(ensureDefaultDeps(), dir, ref) }
func GitClean(dir string) error                 { return gitClean(ensureDefaultDeps(), dir) }
func GitCleanDryRun(dir string) (string, error) { return gitCleanDryRun(ensureDefaultDeps(), dir) }

func GitCleanExclude(dir string, excludes []string) error {
	return gitCleanExclude(ensureDefaultDeps(), dir, excludes)
}

func GitCleanDryRunExclude(dir string, excludes []string) (string, error) {
	return gitCleanDryRunExclude(ensureDefaultDeps(), dir, excludes)
}

func GetConflictedFiles(dir string) ([]string, error) {
	return getConflictedFilesDeps(ensureDefaultDeps(), dir)
}

func HasCommitsBetween(dir, target, source string) (bool, error) {
	return hasCommitsBetweenDeps(ensureDefaultDeps(), dir, target, source)
}

func IsCleanWorkingTree(dir string) (bool, error) {
	return IsCleanWorkingTreeDeps(ensureDefaultDeps(), dir)
}

func GitFetchRemote(dir, remote string) error {
	return gitFetchRemote(ensureDefaultDeps(), dir, remote)
}

func GitMergeRemote(dir, remote, branch, message string) error {
	return gitMergeRemote(ensureDefaultDeps(), dir, remote, branch, message)
}

func GitPushRemote(dir, remote, branch string) error {
	return gitPushRemote(ensureDefaultDeps(), dir, remote, branch)
}

func GitPullRemote(dir, remote, branch string) error {
	return gitPullRemote(ensureDefaultDeps(), dir, remote, branch)
}

func HasCommitsBetweenRemote(dir, remote, target, source string) (bool, error) {
	return hasCommitsBetweenRemoteDeps(ensureDefaultDeps(), dir, remote, target, source)
}

func IsRefCheckedOutInWorktree(dir, branch string) (bool, string, error) {
	return isRefCheckedOutInWorktreeDeps(ensureDefaultDeps(), dir, branch)
}

func GitCheckoutDetached(dir, ref string) error {
	return gitCheckoutDetached(ensureDefaultDeps(), dir, ref)
}

func GitCreateBranchFromHead(dir, name string) error {
	return gitCreateBranchFromHead(ensureDefaultDeps(), dir, name)
}

func GitDeleteBranch(dir, name string, force bool) error {
	return gitDeleteBranch(ensureDefaultDeps(), dir, name, force)
}

func GitPushRefspec(dir, remote, localRef, remoteRef string) error {
	return gitPushRefspec(ensureDefaultDeps(), dir, remote, localRef, remoteRef)
}

func GitStash(dir string) (bool, error) { return gitStash(ensureDefaultDeps(), dir) }
func GitStashPop(dir string) error      { return gitStashPop(ensureDefaultDeps(), dir) }

func BranchExistsLocally(dir, branch string) (bool, error) {
	return branchExistsLocallyDeps(ensureDefaultDeps(), dir, branch)
}

func RemoteBranchExists(dir, remote, branch string) (bool, error) {
	return remoteBranchExistsDeps(ensureDefaultDeps(), dir, remote, branch)
}

func GitCheckoutNewFromRef(dir, branch, startPoint string) error {
	return gitCheckoutNewFromRef(ensureDefaultDeps(), dir, branch, startPoint)
}

func GitMergeAbort(dir string) error {
	return gitMergeAbort(ensureDefaultDeps(), dir)
}

func HasUnmergedFiles(dir string) (bool, error) {
	return hasUnmergedFilesDeps(ensureDefaultDeps(), dir)
}

func GetCurrentBranch(path string) (string, error) {
	return cli.GetCurrentBranch(path)
}

func getStashCount(dir string) (int, error) {
	return getStashCountDeps(ensureDefaultDeps(), dir)
}

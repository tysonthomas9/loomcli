package cli

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// gitRefPattern matches safe git ref names: alphanumeric start, then alphanumeric/underscore/dot/slash/hyphen.
// Note: does NOT exclude ".."; that is handled separately in validateGitRef.
// Keep in sync with internal/webui/handlers_git.go:validGitRef
var gitRefPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_./-]*$`)

// validateGitRef rejects unsafe ref/branch/remote names to prevent git argument
// injection and path traversal. Empty strings are allowed (handled downstream by
// resolveRemote or git itself).
func validateGitRef(name string) error {
	if name == "" {
		return nil // empty handled downstream by resolveRemote or git itself
	}
	if !gitRefPattern.MatchString(name) {
		return fmt.Errorf("invalid git ref %q: must match [a-zA-Z0-9][a-zA-Z0-9_./-]*", name)
	}
	if strings.Contains(name, "..") {
		return fmt.Errorf("invalid git ref %q: must not contain '..'", name)
	}
	return nil
}

// runGit is a deps-aware helper for running git commands.
// Production functions in tested call chains use this instead of RunGitCommand.
func runGit(deps *Deps, dir string, args ...string) (string, error) {
	result := deps.Git.Run(dir, args...)
	if result.Err != nil {
		return "", fmt.Errorf("git %s failed: %s", strings.Join(args, " "), result.Stderr)
	}
	return result.Stdout, nil
}

// runGitOutput is a deps-aware helper for running git commands with output streaming.
func runGitOutput(deps *Deps, dir string, args ...string) error {
	return deps.Git.RunWithOutput(dir, args...)
}

// RunGitCommand executes a git command in the specified directory.
// It routes through the package-level defaultDeps.Git runner, which
// delegates to execCommand for backward compatibility.
func RunGitCommand(dir string, args ...string) (string, error) {
	return runGit(defaultDeps, dir, args...)
}

func defaultRunGitWithOutput(dir string, args ...string) error {
	cmd := exec.Command("git", args...) //nolint:gosec // G204 — args from internal callers
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// RunGitCommandWithOutput executes a git command and streams output to stdout/stderr.
// It routes through the package-level defaultDeps.Git runner.
func RunGitCommandWithOutput(dir string, args ...string) error {
	return runGitOutput(defaultDeps, dir, args...)
}

// --- exported backward-compatible wrappers ---

// GitFetch fetches from origin
func GitFetch(dir string) error {
	return gitFetch(defaultDeps, dir)
}

// GitCheckout checks out a branch
func GitCheckout(dir, branch string) error {
	return gitCheckout(defaultDeps, dir, branch)
}

// GitPull pulls from origin for the current branch
func GitPull(dir, branch string) error {
	return gitPull(defaultDeps, dir, branch)
}

// GitMerge attempts to merge a branch
func GitMerge(dir, branch, message string) error {
	return gitMerge(defaultDeps, dir, branch, message)
}

// GitMergeOrigin attempts to merge origin/branch
func GitMergeOrigin(dir, branch, message string) error {
	return gitMergeOrigin(defaultDeps, dir, branch, message)
}

// GitPush pushes to origin
func GitPush(dir, branch string) error {
	return gitPush(defaultDeps, dir, branch)
}

// GitPushForce force pushes to origin
func GitPushForce(dir, branch string) error {
	return gitPushForce(defaultDeps, dir, branch)
}

// GitReset performs a hard reset to a ref
func GitReset(dir, ref string) error {
	return gitReset(defaultDeps, dir, ref)
}

// GitClean removes untracked files and directories
func GitClean(dir string) error {
	return gitClean(defaultDeps, dir)
}

// GitCleanDryRun returns the list of untracked files that would be removed by git clean
func GitCleanDryRun(dir string) (string, error) {
	return gitCleanDryRun(defaultDeps, dir)
}

// GitCleanExclude removes untracked files and directories, excluding paths
// that match any of the given exclude patterns (passed as --exclude flags).
func GitCleanExclude(dir string, excludes []string) error {
	return gitCleanExclude(defaultDeps, dir, excludes)
}

// GitCleanDryRunExclude returns the list of untracked files that would be
// removed by git clean, excluding paths matching the given patterns.
func GitCleanDryRunExclude(dir string, excludes []string) (string, error) {
	return gitCleanDryRunExclude(defaultDeps, dir, excludes)
}

// GetConflictedFiles returns a list of files with merge conflicts
func GetConflictedFiles(dir string) ([]string, error) {
	return getConflictedFilesDeps(defaultDeps, dir)
}

// HasCommitsBetween checks if source has commits not in target
func HasCommitsBetween(dir, target, source string) (bool, error) {
	return hasCommitsBetweenDeps(defaultDeps, dir, target, source)
}

// IsCleanWorkingTree checks if the working tree is clean
func IsCleanWorkingTree(dir string) (bool, error) {
	return isCleanWorkingTreeDeps(defaultDeps, dir)
}

// resolveRemote returns "origin" if remote is empty, otherwise returns remote as-is.
func resolveRemote(remote string) string {
	if remote == "" {
		return "origin"
	}
	return remote
}

// GitFetchRemote fetches from the specified remote
func GitFetchRemote(dir, remote string) error {
	return gitFetchRemote(defaultDeps, dir, remote)
}

// GitMergeRemote attempts to merge remote/branch
func GitMergeRemote(dir, remote, branch, message string) error {
	return gitMergeRemote(defaultDeps, dir, remote, branch, message)
}

// GitPushRemote pushes to the specified remote
func GitPushRemote(dir, remote, branch string) error {
	return gitPushRemote(defaultDeps, dir, remote, branch)
}

// GitPullRemote pulls from the specified remote for the given branch
func GitPullRemote(dir, remote, branch string) error {
	return gitPullRemote(defaultDeps, dir, remote, branch)
}

// HasCommitsBetweenRemote checks if source has commits not in target using a specific remote
func HasCommitsBetweenRemote(dir, remote, target, source string) (bool, error) {
	return hasCommitsBetweenRemoteDeps(defaultDeps, dir, remote, target, source)
}

// IsRefCheckedOutInWorktree checks if a branch is checked out in any worktree.
func IsRefCheckedOutInWorktree(dir, branch string) (bool, string, error) {
	return isRefCheckedOutInWorktreeDeps(defaultDeps, dir, branch)
}

// GitCheckoutDetached checks out a ref in detached HEAD mode
func GitCheckoutDetached(dir, ref string) error {
	return gitCheckoutDetached(defaultDeps, dir, ref)
}

// GitCreateBranchFromHead creates a new branch at the current HEAD and switches to it
func GitCreateBranchFromHead(dir, name string) error {
	return gitCreateBranchFromHead(defaultDeps, dir, name)
}

// GitDeleteBranch deletes a local branch. Use force=true for -D (force delete).
func GitDeleteBranch(dir, name string, force bool) error {
	return gitDeleteBranch(defaultDeps, dir, name, force)
}

// GitPushRefspec pushes a local ref to a different remote ref using a refspec
func GitPushRefspec(dir, remote, localRef, remoteRef string) error {
	return gitPushRefspec(defaultDeps, dir, remote, localRef, remoteRef)
}

// getStashCount returns the number of entries in the stash.
func getStashCount(dir string) (int, error) {
	return getStashCountDeps(defaultDeps, dir)
}

// GitStash stashes local changes. Returns true if changes were actually stashed,
// false if nothing was stashed (e.g. only untracked files, or clean tree).
func GitStash(dir string) (bool, error) {
	return gitStash(defaultDeps, dir)
}

// GitStashPop pops the most recent stash entry
func GitStashPop(dir string) error {
	return gitStashPop(defaultDeps, dir)
}

// BranchExistsLocally checks if a branch exists as a local ref.
func BranchExistsLocally(dir, branch string) (bool, error) {
	return branchExistsLocallyDeps(defaultDeps, dir, branch)
}

// RemoteBranchExists checks if a branch exists on the specified remote.
func RemoteBranchExists(dir, remote, branch string) (bool, error) {
	return remoteBranchExistsDeps(defaultDeps, dir, remote, branch)
}

// GitCheckoutNewFromRef creates a new local branch at the given starting point.
func GitCheckoutNewFromRef(dir, branch, startPoint string) error {
	return gitCheckoutNewFromRef(defaultDeps, dir, branch, startPoint)
}

// GitMergeAbort aborts an in-progress merge, restoring the pre-merge state.
func GitMergeAbort(dir string) error {
	return gitMergeAbort(defaultDeps, dir)
}

// HasUnmergedFiles checks if there are unmerged files in the working tree
func HasUnmergedFiles(dir string) (bool, error) {
	return hasUnmergedFilesDeps(defaultDeps, dir)
}

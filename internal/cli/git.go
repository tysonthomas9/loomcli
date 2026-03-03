package cli

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// validateGitRef rejects ref/branch/remote names starting with '-' to prevent
// git argument injection. Empty strings are allowed (handled downstream by
// resolveRemote or git itself).
func validateGitRef(name string) error {
	if strings.HasPrefix(name, "-") {
		return fmt.Errorf("invalid git ref %q: must not start with '-'", name)
	}
	return nil
}

// RunGitCommand executes a git command in the specified directory.
// It routes through the package-level defaultDeps.Git runner, which
// delegates to execCommand for backward compatibility.
func RunGitCommand(dir string, args ...string) (string, error) {
	result := defaultDeps.Git.Run(dir, args...)
	if result.Err != nil {
		return "", fmt.Errorf("git %s failed: %s", strings.Join(args, " "), result.Stderr)
	}
	return result.Stdout, nil
}

// outputCommandExecutor is the function type for executing commands with streaming output
type outputCommandExecutor func(dir string, args ...string) error

// runGitWithOutputFunc is the package-level executor (swappable for tests)
var runGitWithOutputFunc outputCommandExecutor = defaultRunGitWithOutput

func defaultRunGitWithOutput(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// RunGitCommandWithOutput executes a git command and streams output to stdout/stderr.
// It routes through the package-level defaultDeps.Git runner.
func RunGitCommandWithOutput(dir string, args ...string) error {
	return defaultDeps.Git.RunWithOutput(dir, args...)
}

// GitFetch fetches from origin
func GitFetch(dir string) error {
	fmt.Println("Fetching from origin...")
	return RunGitCommandWithOutput(dir, "fetch", "origin")
}

// GitCheckout checks out a branch
func GitCheckout(dir, branch string) error {
	if err := validateGitRef(branch); err != nil {
		return err
	}
	fmt.Printf("Checking out %s...\n", branch)
	return RunGitCommandWithOutput(dir, "checkout", branch)
}

// GitPull pulls from origin for the current branch
func GitPull(dir, branch string) error {
	if err := validateGitRef(branch); err != nil {
		return err
	}
	fmt.Printf("Pulling from origin/%s...\n", branch)
	return RunGitCommandWithOutput(dir, "pull", "origin", branch)
}

// GitMerge attempts to merge a branch
func GitMerge(dir, branch, message string) error {
	if err := validateGitRef(branch); err != nil {
		return err
	}
	fmt.Printf("Merging %s...\n", branch)
	return RunGitCommandWithOutput(dir, "merge", "-m", message, "--", branch)
}

// GitMergeOrigin attempts to merge origin/branch
func GitMergeOrigin(dir, branch, message string) error {
	fmt.Printf("Merging origin/%s...\n", branch)
	return RunGitCommandWithOutput(dir, "merge", "origin/"+branch, "-m", message)
}

// GitPush pushes to origin
func GitPush(dir, branch string) error {
	if err := validateGitRef(branch); err != nil {
		return err
	}
	fmt.Printf("Pushing to origin/%s...\n", branch)
	return RunGitCommandWithOutput(dir, "push", "origin", branch)
}

// GitPushForce force pushes to origin
func GitPushForce(dir, branch string) error {
	if err := validateGitRef(branch); err != nil {
		return err
	}
	fmt.Printf("Force pushing to origin/%s...\n", branch)
	return RunGitCommandWithOutput(dir, "push", "origin", branch, "--force")
}

// GitReset performs a hard reset to a ref
func GitReset(dir, ref string) error {
	if err := validateGitRef(ref); err != nil {
		return err
	}
	fmt.Printf("Resetting to %s...\n", ref)
	return RunGitCommandWithOutput(dir, "reset", "--hard", ref)
}

// GitClean removes untracked files and directories
func GitClean(dir string) error {
	fmt.Println("Cleaning untracked files...")
	return RunGitCommandWithOutput(dir, "clean", "-fd")
}

// GitCleanDryRun returns the list of untracked files that would be removed by git clean
func GitCleanDryRun(dir string) (string, error) {
	return RunGitCommand(dir, "clean", "-fdn")
}

// GetConflictedFiles returns a list of files with merge conflicts
func GetConflictedFiles(dir string) ([]string, error) {
	output, err := RunGitCommand(dir, "diff", "--name-only", "--diff-filter=U")
	if err != nil {
		return nil, err
	}

	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil, nil
	}
	return lines, nil
}

// HasCommitsBetween checks if source has commits not in target
func HasCommitsBetween(dir, target, source string) (bool, error) {
	if err := validateGitRef(target); err != nil {
		return false, err
	}
	if err := validateGitRef(source); err != nil {
		return false, err
	}
	output, err := RunGitCommand(dir, "log", fmt.Sprintf("%s..%s", target, source), "--oneline")
	if err != nil {
		// If the command fails, assume there might be commits
		return true, nil
	}
	return strings.TrimSpace(output) != "", nil
}

// IsCleanWorkingTree checks if the working tree is clean
func IsCleanWorkingTree(dir string) (bool, error) {
	output, err := RunGitCommand(dir, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(output) == "", nil
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
	r := resolveRemote(remote)
	if err := validateGitRef(r); err != nil {
		return err
	}
	fmt.Printf("Fetching from %s...\n", r)
	return RunGitCommandWithOutput(dir, "fetch", r)
}

// GitMergeRemote attempts to merge remote/branch
func GitMergeRemote(dir, remote, branch, message string) error {
	r := resolveRemote(remote)
	if err := validateGitRef(r); err != nil {
		return err
	}
	if err := validateGitRef(branch); err != nil {
		return err
	}
	ref := r + "/" + branch
	fmt.Printf("Merging %s...\n", ref)
	return RunGitCommandWithOutput(dir, "merge", ref, "-m", message)
}

// GitPushRemote pushes to the specified remote
func GitPushRemote(dir, remote, branch string) error {
	r := resolveRemote(remote)
	if err := validateGitRef(r); err != nil {
		return err
	}
	if err := validateGitRef(branch); err != nil {
		return err
	}
	fmt.Printf("Pushing to %s/%s...\n", r, branch)
	return RunGitCommandWithOutput(dir, "push", r, branch)
}

// GitPullRemote pulls from the specified remote for the given branch
func GitPullRemote(dir, remote, branch string) error {
	r := resolveRemote(remote)
	if err := validateGitRef(r); err != nil {
		return err
	}
	if err := validateGitRef(branch); err != nil {
		return err
	}
	fmt.Printf("Pulling from %s/%s...\n", r, branch)
	return RunGitCommandWithOutput(dir, "pull", r, branch)
}

// HasCommitsBetweenRemote checks if source has commits not in target using a specific remote
func HasCommitsBetweenRemote(dir, remote, target, source string) (bool, error) {
	r := resolveRemote(remote)
	if err := validateGitRef(r); err != nil {
		return false, err
	}
	if err := validateGitRef(target); err != nil {
		return false, err
	}
	if err := validateGitRef(source); err != nil {
		return false, err
	}
	output, err := RunGitCommand(dir, "log", fmt.Sprintf("%s/%s..%s", r, target, source), "--oneline")
	if err != nil {
		return true, nil
	}
	return strings.TrimSpace(output) != "", nil
}

// IsRefCheckedOutInWorktree checks if a branch is checked out in any worktree.
// Returns (isCheckedOut, worktreePath, error).
func IsRefCheckedOutInWorktree(dir, branch string) (bool, string, error) {
	output, err := RunGitCommand(dir, "worktree", "list", "--porcelain")
	if err != nil {
		return false, "", err
	}

	var currentWorktree string
	for _, line := range strings.Split(output, "\n") {
		if line == "" {
			currentWorktree = ""
			continue
		}
		if strings.HasPrefix(line, "worktree ") {
			currentWorktree = strings.TrimPrefix(line, "worktree ")
		} else if strings.HasPrefix(line, "branch refs/heads/") {
			branchName := strings.TrimPrefix(line, "branch refs/heads/")
			if branchName == branch {
				return true, currentWorktree, nil
			}
		}
	}

	return false, "", nil
}

// GitCheckoutDetached checks out a ref in detached HEAD mode
func GitCheckoutDetached(dir, ref string) error {
	if err := validateGitRef(ref); err != nil {
		return err
	}
	fmt.Printf("Checking out %s (detached)...\n", ref)
	return RunGitCommandWithOutput(dir, "checkout", "--detach", ref)
}

// GitCreateBranchFromHead creates a new branch at the current HEAD and switches to it
func GitCreateBranchFromHead(dir, name string) error {
	if err := validateGitRef(name); err != nil {
		return err
	}
	fmt.Printf("Creating branch %s...\n", name)
	return RunGitCommandWithOutput(dir, "checkout", "-b", name)
}

// GitDeleteBranch deletes a local branch. Use force=true for -D (force delete).
func GitDeleteBranch(dir, name string, force bool) error {
	if err := validateGitRef(name); err != nil {
		return err
	}
	flag := "-d"
	if force {
		flag = "-D"
	}
	return RunGitCommandWithOutput(dir, "branch", flag, "--", name)
}

// GitPushRefspec pushes a local ref to a different remote ref using a refspec
func GitPushRefspec(dir, remote, localRef, remoteRef string) error {
	r := resolveRemote(remote)
	if err := validateGitRef(r); err != nil {
		return err
	}
	if err := validateGitRef(localRef); err != nil {
		return err
	}
	if err := validateGitRef(remoteRef); err != nil {
		return err
	}
	refspec := localRef + ":" + remoteRef
	fmt.Printf("Pushing %s to %s/%s...\n", localRef, r, remoteRef)
	return RunGitCommandWithOutput(dir, "push", r, refspec)
}

// getStashCount returns the number of entries in the stash.
func getStashCount(dir string) (int, error) {
	output, err := RunGitCommand(dir, "stash", "list")
	if err != nil {
		return 0, err
	}
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return 0, nil
	}
	return len(strings.Split(trimmed, "\n")), nil
}

// GitStash stashes local changes. Returns true if changes were actually stashed,
// false if nothing was stashed (e.g. only untracked files, or clean tree).
func GitStash(dir string) (bool, error) {
	countBefore, err := getStashCount(dir)
	if err != nil {
		return false, err
	}

	fmt.Println("Stashing local changes...")
	if err := RunGitCommandWithOutput(dir, "stash"); err != nil {
		return false, fmt.Errorf("failed to stash changes: %w", err)
	}

	countAfter, err := getStashCount(dir)
	if err != nil {
		return false, err
	}

	return countAfter > countBefore, nil
}

// GitStashPop pops the most recent stash entry
func GitStashPop(dir string) error {
	fmt.Println("Restoring stashed changes...")
	return RunGitCommandWithOutput(dir, "stash", "pop")
}

// BranchExistsLocally checks if a branch exists as a local ref.
func BranchExistsLocally(dir, branch string) (bool, error) {
	if err := validateGitRef(branch); err != nil {
		return false, err
	}
	_, err := RunGitCommand(dir, "rev-parse", "--verify", "refs/heads/"+branch)
	if err != nil {
		return false, nil
	}
	return true, nil
}

// RemoteBranchExists checks if a branch exists on the specified remote.
func RemoteBranchExists(dir, remote, branch string) (bool, error) {
	r := resolveRemote(remote)
	if err := validateGitRef(r); err != nil {
		return false, err
	}
	if err := validateGitRef(branch); err != nil {
		return false, err
	}
	_, err := RunGitCommand(dir, "rev-parse", "--verify", "refs/remotes/"+r+"/"+branch)
	if err != nil {
		return false, nil
	}
	return true, nil
}

// GitCheckoutNewFromRef creates a new local branch at the given starting point.
func GitCheckoutNewFromRef(dir, branch, startPoint string) error {
	if err := validateGitRef(branch); err != nil {
		return err
	}
	if err := validateGitRef(startPoint); err != nil {
		return err
	}
	fmt.Printf("Creating branch %s from %s...\n", branch, startPoint)
	return RunGitCommandWithOutput(dir, "checkout", "-b", branch, startPoint)
}

// HasUnmergedFiles checks if there are unmerged files in the working tree
func HasUnmergedFiles(dir string) (bool, error) {
	files, err := GetConflictedFiles(dir)
	if err != nil {
		return false, err
	}
	return len(files) > 0, nil
}

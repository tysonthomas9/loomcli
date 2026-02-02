package cli

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// RunGitCommand executes a git command in the specified directory
func RunGitCommand(dir string, args ...string) (string, error) {
	result := execCommand(dir, "git", args...)
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

// RunGitCommandWithOutput executes a git command and streams output to stdout/stderr
func RunGitCommandWithOutput(dir string, args ...string) error {
	return runGitWithOutputFunc(dir, args...)
}

// GitFetch fetches from origin
func GitFetch(dir string) error {
	fmt.Println("Fetching from origin...")
	return RunGitCommandWithOutput(dir, "fetch", "origin")
}

// GitCheckout checks out a branch
func GitCheckout(dir, branch string) error {
	fmt.Printf("Checking out %s...\n", branch)
	return RunGitCommandWithOutput(dir, "checkout", branch)
}

// GitPull pulls from origin for the current branch
func GitPull(dir, branch string) error {
	fmt.Printf("Pulling from origin/%s...\n", branch)
	return RunGitCommandWithOutput(dir, "pull", "origin", branch)
}

// GitMerge attempts to merge a branch
func GitMerge(dir, branch, message string) error {
	fmt.Printf("Merging %s...\n", branch)
	return RunGitCommandWithOutput(dir, "merge", branch, "-m", message)
}

// GitMergeOrigin attempts to merge origin/branch
func GitMergeOrigin(dir, branch, message string) error {
	fmt.Printf("Merging origin/%s...\n", branch)
	return RunGitCommandWithOutput(dir, "merge", "origin/"+branch, "-m", message)
}

// GitPush pushes to origin
func GitPush(dir, branch string) error {
	fmt.Printf("Pushing to origin/%s...\n", branch)
	return RunGitCommandWithOutput(dir, "push", "origin", branch)
}

// GitPushForce force pushes to origin
func GitPushForce(dir, branch string) error {
	fmt.Printf("Force pushing to origin/%s...\n", branch)
	return RunGitCommandWithOutput(dir, "push", "origin", branch, "--force")
}

// GitReset performs a hard reset to a ref
func GitReset(dir, ref string) error {
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
	output, err := RunGitCommand(dir, "log", fmt.Sprintf("%s..origin/%s", target, source), "--oneline")
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
	fmt.Printf("Fetching from %s...\n", r)
	return RunGitCommandWithOutput(dir, "fetch", r)
}

// GitMergeRemote attempts to merge remote/branch
func GitMergeRemote(dir, remote, branch, message string) error {
	r := resolveRemote(remote)
	ref := r + "/" + branch
	fmt.Printf("Merging %s...\n", ref)
	return RunGitCommandWithOutput(dir, "merge", ref, "-m", message)
}

// GitPushRemote pushes to the specified remote
func GitPushRemote(dir, remote, branch string) error {
	r := resolveRemote(remote)
	fmt.Printf("Pushing to %s/%s...\n", r, branch)
	return RunGitCommandWithOutput(dir, "push", r, branch)
}

// GitPullRemote pulls from the specified remote for the given branch
func GitPullRemote(dir, remote, branch string) error {
	r := resolveRemote(remote)
	fmt.Printf("Pulling from %s/%s...\n", r, branch)
	return RunGitCommandWithOutput(dir, "pull", r, branch)
}

// HasCommitsBetweenRemote checks if source has commits not in target using a specific remote
func HasCommitsBetweenRemote(dir, remote, target, source string) (bool, error) {
	r := resolveRemote(remote)
	output, err := RunGitCommand(dir, "log", fmt.Sprintf("%s..%s/%s", target, r, source), "--oneline")
	if err != nil {
		return true, nil
	}
	return strings.TrimSpace(output) != "", nil
}

// GitStash stashes local changes. Returns true if changes were actually stashed,
// false if working tree was clean (nothing to stash).
func GitStash(dir string) (bool, error) {
	// Check if there's anything to stash
	clean, err := IsCleanWorkingTree(dir)
	if err != nil {
		return false, err
	}
	if clean {
		return false, nil
	}

	fmt.Println("Stashing local changes...")
	if err := RunGitCommandWithOutput(dir, "stash"); err != nil {
		return false, fmt.Errorf("failed to stash changes: %w", err)
	}
	return true, nil
}

// GitStashPop pops the most recent stash entry
func GitStashPop(dir string) error {
	fmt.Println("Restoring stashed changes...")
	return RunGitCommandWithOutput(dir, "stash", "pop")
}

// HasUnmergedFiles checks if there are unmerged files in the working tree
func HasUnmergedFiles(dir string) (bool, error) {
	files, err := GetConflictedFiles(dir)
	if err != nil {
		return false, err
	}
	return len(files) > 0, nil
}

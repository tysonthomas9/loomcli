package main

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

// RunGitCommandWithOutput executes a git command and streams output to stdout/stderr
func RunGitCommandWithOutput(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
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

// HasCommitsBetween checks if source has commits not in target.
// On error, returns true (assumes there might be commits).
func HasCommitsBetween(dir, target, source string) bool {
	output, err := RunGitCommand(dir, "log", fmt.Sprintf("%s..origin/%s", target, source), "--oneline")
	if err != nil {
		// If the command fails, assume there might be commits
		return true
	}
	return strings.TrimSpace(output) != ""
}

// IsCleanWorkingTree checks if the working tree is clean
func IsCleanWorkingTree(dir string) (bool, error) {
	output, err := RunGitCommand(dir, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(output) == "", nil
}

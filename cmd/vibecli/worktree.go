package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// WorktreeInfo holds information about a discovered worktree
type WorktreeInfo struct {
	Name   string
	Path   string
	Branch string
}

// GetWorktreesDir returns the worktrees directory path
func GetWorktreesDir() string {
	if dir := os.Getenv("VIBECLI_WORKTREES_DIR"); dir != "" {
		return dir
	}
	return "worktrees"
}

// GetDefaultBranch returns the default integration branch
func GetDefaultBranch() string {
	if branch := os.Getenv("VIBECLI_DEFAULT_BRANCH"); branch != "" {
		return branch
	}
	return "feature/web-ui"
}

// GetScriptDir returns the directory where vibecli is run from
func GetScriptDir() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to get current directory: %w", err)
	}
	return cwd, nil
}

// ResolveWorktreePath converts a worktree name to its full path
// Accepts:
//   - A worktree name (e.g., "falcon") -> ./worktrees/falcon
//   - An absolute path (e.g., /path/to/worktree) -> as-is
//   - Empty string -> current directory
func ResolveWorktreePath(name string) (string, error) {
	if name == "" {
		return os.Getwd()
	}

	// Absolute path - use as-is
	if filepath.IsAbs(name) {
		if _, err := os.Stat(name); err != nil {
			return "", fmt.Errorf("worktree path does not exist: %s", name)
		}
		return name, nil
	}

	// Relative name - resolve to worktrees directory
	scriptDir, err := GetScriptDir()
	if err != nil {
		return "", err
	}

	worktreePath := filepath.Join(scriptDir, GetWorktreesDir(), name)
	if _, err := os.Stat(worktreePath); err != nil {
		return "", fmt.Errorf("worktree '%s' not found at %s", name, worktreePath)
	}

	return worktreePath, nil
}

// GetWorktreeName extracts the worktree name from a path
func GetWorktreeName(path string) string {
	return filepath.Base(path)
}

// DiscoverWorktrees finds all worktrees in the worktrees directory
func DiscoverWorktrees() ([]WorktreeInfo, error) {
	scriptDir, err := GetScriptDir()
	if err != nil {
		return nil, err
	}

	worktreesDir := filepath.Join(scriptDir, GetWorktreesDir())
	if _, err := os.Stat(worktreesDir); err != nil {
		return nil, fmt.Errorf("worktrees directory not found: %s", worktreesDir)
	}

	entries, err := os.ReadDir(worktreesDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read worktrees directory: %w", err)
	}

	var worktrees []WorktreeInfo
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		worktreePath := filepath.Join(worktreesDir, entry.Name())

		// Verify it's a git directory
		gitDir := filepath.Join(worktreePath, ".git")
		if _, err := os.Stat(gitDir); err != nil {
			continue // Not a git worktree
		}

		// Get the current branch
		branch, err := GetCurrentBranch(worktreePath)
		if err != nil {
			branch = "unknown"
		}

		worktrees = append(worktrees, WorktreeInfo{
			Name:   entry.Name(),
			Path:   worktreePath,
			Branch: branch,
		})
	}

	return worktrees, nil
}

// GetCurrentBranch returns the current branch for a git directory
func GetCurrentBranch(path string) (string, error) {
	output, err := RunGitCommand(path, "branch", "--show-current")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(output), nil
}

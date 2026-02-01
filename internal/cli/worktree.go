package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// WorktreeInfo holds information about a discovered worktree
type WorktreeInfo struct {
	Name   string
	Path   string
	Branch string
}

// GetWorktreesDir returns the worktrees directory path
// Priority: --worktrees flag > LOOM_WORKTREES_DIR env var > default "worktrees"
func GetWorktreesDir() string {
	if worktreesFlag != "" {
		return filepath.Clean(worktreesFlag)
	}
	if dir := os.Getenv("LOOM_WORKTREES_DIR"); dir != "" {
		return filepath.Clean(dir)
	}
	return "worktrees"
}

// GetDefaultBranch returns the default integration branch.
// Resolution order: LOOM_DEFAULT_BRANCH env var > auto-detected from worktree topology > "main"
// This is a convenience wrapper that discovers worktrees automatically.
// When worktrees are already available, use GetDefaultBranchForWorktrees instead.
func GetDefaultBranch() string {
	worktrees, _ := DiscoverWorktrees()
	return GetDefaultBranchForWorktrees(worktrees)
}

// GetDefaultBranchForWorktrees returns the default integration branch using
// pre-discovered worktrees to avoid redundant filesystem/git operations.
func GetDefaultBranchForWorktrees(worktrees []WorktreeInfo) string {
	if branch := os.Getenv("LOOM_DEFAULT_BRANCH"); branch != "" {
		return branch
	}
	if len(worktrees) >= 2 {
		if detected := DetectIntegrationBranch(worktrees); detected != "" {
			return detected
		}
	}
	return "main"
}

// DetectIntegrationBranch analyzes worktree branches to find a common integration
// branch that is closer than main. Returns empty string if no such branch is found.
func DetectIntegrationBranch(worktrees []WorktreeInfo) string {
	if len(worktrees) < 2 {
		return ""
	}

	repoPath := worktrees[0].Path

	// Get all remote branches as candidates
	output, err := RunGitCommand(repoPath, "branch", "-r", "--format=%(refname:short)")
	if err != nil {
		return ""
	}

	// Build set of worktree branch names (with origin/ prefix) to exclude
	wtBranches := make(map[string]bool)
	for _, wt := range worktrees {
		wtBranches["origin/"+wt.Branch] = true
	}

	var candidates []string
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		branch := strings.TrimSpace(line)
		if branch == "" || branch == "origin/HEAD" {
			continue
		}
		// Skip worktree branches themselves and main/master
		if wtBranches[branch] || branch == "origin/main" || branch == "origin/master" {
			continue
		}
		candidates = append(candidates, branch)
	}

	// For each candidate, check if it's an ancestor of ALL worktree branches
	bestBranch := ""
	bestMaxDist := -1

	for _, candidate := range candidates {
		isAncestorOfAll := true
		maxDist := 0

		for _, wt := range worktrees {
			// Check if candidate is ancestor of worktree branch
			_, err := RunGitCommand(repoPath, "merge-base", "--is-ancestor", candidate, "origin/"+wt.Branch)
			if err != nil {
				isAncestorOfAll = false
				break
			}

			// Get distance (commits between candidate and worktree branch)
			distOutput, err := RunGitCommand(repoPath, "rev-list", "--count", candidate+"..origin/"+wt.Branch)
			if err != nil {
				isAncestorOfAll = false
				break
			}
			dist, _ := strconv.Atoi(strings.TrimSpace(distOutput))
			if dist > maxDist {
				maxDist = dist
			}
		}

		if !isAncestorOfAll {
			continue
		}

		// Pick the candidate closest to all worktrees (smallest max distance)
		if bestBranch == "" || maxDist < bestMaxDist {
			bestBranch = candidate
			bestMaxDist = maxDist
		}
	}

	if bestBranch == "" {
		return ""
	}

	// Only use detected branch if it's actually closer than main/master
	// Try origin/main first, fall back to origin/master
	mainRef := "origin/main"
	if _, err := RunGitCommand(repoPath, "rev-parse", "--verify", "origin/main"); err != nil {
		if _, err := RunGitCommand(repoPath, "rev-parse", "--verify", "origin/master"); err != nil {
			// Neither main nor master exists as remote branch; can't compare
			return ""
		}
		mainRef = "origin/master"
	}
	mainMaxDist := 0
	for _, wt := range worktrees {
		distOutput, err := RunGitCommand(repoPath, "rev-list", "--count", mainRef+"..origin/"+wt.Branch)
		if err != nil {
			return ""
		}
		dist, _ := strconv.Atoi(strings.TrimSpace(distOutput))
		if dist > mainMaxDist {
			mainMaxDist = dist
		}
	}
	if bestMaxDist >= mainMaxDist {
		return ""
	}

	return strings.TrimPrefix(bestBranch, "origin/")
}

// ResolveWorktreesDir returns the absolute path to the worktrees directory
// If the configured path is absolute, use it directly; otherwise join with scriptDir
func ResolveWorktreesDir() (string, error) {
	dir := GetWorktreesDir()
	if filepath.IsAbs(dir) {
		return dir, nil
	}
	scriptDir, err := GetScriptDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(scriptDir, dir), nil
}

// GetScriptDir returns the directory where loom is run from
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
	worktreesDir, err := ResolveWorktreesDir()
	if err != nil {
		return "", err
	}

	worktreePath := filepath.Join(worktreesDir, name)
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
	worktreesDir, err := ResolveWorktreesDir()
	if err != nil {
		return nil, err
	}
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

package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// WorktreeInfo holds information about a discovered worktree
type WorktreeInfo struct {
	Name      string
	Path      string
	Branch    string
	Workspace string      // workspace name (empty in legacy mode)
	Repo      *RepoConfig // source repo config (nil in legacy mode)
}

// ResolverMode indicates how the Resolver discovers worktrees
type ResolverMode int

const (
	ModeLegacy    ResolverMode = iota // scan ./worktrees/ directory
	ModeWorkspace                     // read from ~/.loom/config.yaml
)

// validateWorktreeName checks that a worktree name does not contain path
// traversal sequences. Returns an error if the name is unsafe.
func validateWorktreeName(name string) error {
	cleaned := filepath.Clean(name)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return fmt.Errorf("invalid worktree name %q: path traversal not allowed", name)
	}
	return nil
}

// resolveLegacyPath is the original ResolveWorktreePath logic.
func resolveLegacyPath(name string) (string, error) {
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

	// Validate name does not traverse outside worktrees directory
	if err := validateWorktreeName(name); err != nil {
		return "", err
	}

	// Relative name - resolve to worktrees directory
	worktreesDir, err := ResolveWorktreesDir()
	if err != nil {
		return "", err
	}

	worktreePath := filepath.Clean(filepath.Join(worktreesDir, name))

	// Defense-in-depth: verify resolved path is within worktrees directory
	absWorktreesDir := filepath.Clean(worktreesDir)
	if !strings.HasPrefix(worktreePath, absWorktreesDir+string(filepath.Separator)) &&
		worktreePath != absWorktreesDir {
		return "", fmt.Errorf("invalid worktree name %q: resolved path escapes worktrees directory", name)
	}

	if _, err := os.Stat(worktreePath); err != nil {
		return "", fmt.Errorf("worktree '%s' not found at %s", name, worktreePath)
	}

	return worktreePath, nil
}

// getWorktreesDirLegacy is the original GetWorktreesDir logic.
func getWorktreesDirLegacy() string {
	if worktreesFlag != "" {
		return filepath.Clean(worktreesFlag)
	}
	if dir := os.Getenv("LOOM_WORKTREES_DIR"); dir != "" {
		return filepath.Clean(dir)
	}
	return "worktrees"
}

// GetWorktreesDir returns the worktrees directory path
// Priority: --worktrees flag > LOOM_WORKTREES_DIR env var > default "worktrees"
func GetWorktreesDir() string {
	return getDefaultResolver().GetWorktreesDir()
}

// GetDefaultBranch returns the default integration branch.
// Resolution order: LOOM_DEFAULT_BRANCH env var > auto-detected from worktree topology > "main"
// This is a convenience wrapper that discovers worktrees automatically.
// When worktrees are already available, use GetDefaultBranchForWorktrees instead.
func GetDefaultBranch() string {
	return getDefaultResolver().GetDefaultBranch()
}

// ResolveWorktreesDir returns the absolute path to the worktrees directory
// If the configured path is absolute, use it directly; otherwise join with scriptDir
func ResolveWorktreesDir() (string, error) {
	dir := getWorktreesDirLegacy()
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
	return getDefaultResolver().ResolveWorktreePath(name)
}

// DiscoverWorktrees finds all worktrees in the worktrees directory
func DiscoverWorktrees() ([]WorktreeInfo, error) {
	return getDefaultResolver().DiscoverWorktrees()
}

// getCurrentBranchDeps is the deps-aware implementation of GetCurrentBranch.
func getCurrentBranchDeps(deps *Deps, path string) (string, error) {
	output, err := runGit(deps, path, "branch", "--show-current")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(output), nil
}

// GetCurrentBranch returns the current branch for a git directory
func GetCurrentBranch(path string) (string, error) {
	return getCurrentBranchDeps(defaultDeps, path)
}

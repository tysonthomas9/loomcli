package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/localworkspace"
)

// WorktreeInfo holds information about a discovered worktree
type WorktreeInfo struct {
	Name             string
	Path             string
	Branch           string
	Workspace        string             // workspace name
	Repo             *config.RepoConfig // source repo config
	IsLinkedWorktree bool               // true if .git is a file (linked worktree), false if .git is a directory (source repo)
}

// IsGitLinkedWorktree reports whether the path is a git linked worktree
// (has a .git file) rather than a source repo (has a .git directory).
func IsGitLinkedWorktree(repoPath string) bool {
	return localworkspace.IsGitLinkedWorktree(repoPath)
}

// ResolverMode indicates how the Resolver discovers worktrees
type ResolverMode int

const (
	ModeWorkspace ResolverMode = iota
)

// validateWorktreeName checks that a worktree name does not contain path
// traversal sequences. Returns an error if the name is unsafe.
func ValidateWorktreeName(name string) error {
	cleaned := filepath.Clean(name)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return fmt.Errorf("invalid worktree name %q: path traversal not allowed", name)
	}
	return nil
}

// GetWorktreesDir returns the active workspace root path.
func GetWorktreesDir() string {
	return GetDefaultResolver().GetWorktreesDir()
}

// GetDefaultBranch returns the default integration branch.
// Resolution order: LOOM_DEFAULT_BRANCH env var > auto-detected from worktree topology > "main"
// This is a convenience wrapper that discovers worktrees automatically.
// When worktrees are already available, use GetDefaultBranchForWorktrees instead.
func GetDefaultBranch() string {
	return GetDefaultResolver().GetDefaultBranch()
}

// ResolveWorktreesDir returns the active workspace root path.
func ResolveWorktreesDir() (string, error) {
	return GetDefaultResolver().GetWorktreesDir(), nil
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
	return GetDefaultResolver().ResolveWorktreePath(name)
}

// DiscoverWorktrees finds all worktrees in the worktrees directory
func DiscoverWorktrees() ([]WorktreeInfo, error) {
	return GetDefaultResolver().DiscoverWorktrees()
}

// DiscoverAgentWorktrees finds agent worktrees in the active workspace.
func DiscoverAgentWorktrees() ([]WorktreeInfo, error) {
	return GetDefaultResolver().DiscoverAgentWorktrees()
}

// getCurrentBranchDeps is the deps-aware implementation of GetCurrentBranch.
func getCurrentBranchDeps(deps *Deps, path string) (string, error) {
	output, err := RunGit(deps, path, "branch", "--show-current")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(output), nil
}

// GetCurrentBranch returns the current branch for a git directory
func GetCurrentBranch(path string) (string, error) {
	return getCurrentBranchDeps(ensureDefaultDeps(), path)
}

// --- Branch detection (merged from worktree_branch.go) ---

// integrationBranchCache caches the result of DetectIntegrationBranch.
// The integration branch changes rarely (only when someone pushes a new
// remote branch), so a 60s TTL avoids the O(candidates x worktrees) git
// commands that otherwise run on every monitor data collection.
var (
	integrationBranchMu      sync.Mutex
	integrationBranchCache   string
	integrationBranchCacheAt time.Time
	integrationBranchTTL     = 60 * time.Second
)

// DefaultBranchForWorktree returns the default branch for a single worktree.
// When Repo is set, uses RepoConfig.DefaultBranch with "main" fallback.
func DefaultBranchForWorktree(wt WorktreeInfo) string {
	if branch := os.Getenv("LOOM_DEFAULT_BRANCH"); branch != "" {
		return branch
	}
	if wt.Repo != nil && wt.Repo.DefaultBranch != "" {
		return wt.Repo.DefaultBranch
	}
	return "main"
}

// GetDefaultBranchForWorktrees returns the default integration branch using
// pre-discovered worktrees to avoid redundant filesystem/git operations.
func GetDefaultBranchForWorktrees(worktrees []WorktreeInfo) string {
	if branch := os.Getenv("LOOM_DEFAULT_BRANCH"); branch != "" {
		return branch
	}
	// Worktrees may span multiple repos.
	// DetectIntegrationBranch uses worktrees[0].Path for all git ops,
	// which is meaningless across repos. Use per-repo config instead.
	for _, wt := range worktrees {
		if wt.Repo != nil {
			// Workspace Mode: return first non-empty DefaultBranch, or "main"
			for _, w := range worktrees {
				if w.Repo != nil && w.Repo.DefaultBranch != "" {
					return w.Repo.DefaultBranch
				}
			}
			return "main"
		}
	}

	if len(worktrees) < 2 {
		return "main"
	}

	integrationBranchMu.Lock()
	if integrationBranchCache != "" && time.Since(integrationBranchCacheAt) < integrationBranchTTL {
		result := integrationBranchCache
		integrationBranchMu.Unlock()
		return result
	}
	integrationBranchMu.Unlock()

	result := "main"
	if detected := detectIntegrationBranchDeps(ensureDefaultDeps(), worktrees); detected != "" {
		result = detected
	}

	integrationBranchMu.Lock()
	integrationBranchCache = result
	integrationBranchCacheAt = time.Now()
	integrationBranchMu.Unlock()

	return result
}

// TestingResetIntegrationBranchCache clears the integration branch cache for tests.
func TestingResetIntegrationBranchCache() {
	integrationBranchMu.Lock()
	integrationBranchCache = ""
	integrationBranchCacheAt = time.Time{}
	integrationBranchMu.Unlock()
}

// detectIntegrationBranchDeps is the deps-aware implementation of DetectIntegrationBranch.
func detectIntegrationBranchDeps(deps *Deps, worktrees []WorktreeInfo) string {
	if len(worktrees) < 2 {
		return ""
	}

	// Safety: if any worktree has Repo set (workspace mode), cross-repo
	// git operations are meaningless. Return empty to skip detection.
	for _, wt := range worktrees {
		if wt.Repo != nil {
			return ""
		}
	}

	repoPath := worktrees[0].Path //nolint:gosec // safe: len(worktrees) == 0 is checked above

	// Get all remote branches as candidates
	output, err := RunGit(deps, repoPath, "branch", "-r", "--format=%(refname:short)")
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
			_, err := RunGit(deps, repoPath, "merge-base", "--is-ancestor", candidate, "origin/"+wt.Branch)
			if err != nil {
				isAncestorOfAll = false
				break
			}

			// Get distance (commits between candidate and worktree branch)
			distOutput, err := RunGit(deps, repoPath, "rev-list", "--count", candidate+"..origin/"+wt.Branch)
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
	if _, err := RunGit(deps, repoPath, "rev-parse", "--verify", "origin/main"); err != nil {
		if _, err := RunGit(deps, repoPath, "rev-parse", "--verify", "origin/master"); err != nil {
			// Neither main nor master exists as remote branch; can't compare
			return ""
		}
		mainRef = "origin/master"
	}
	mainMaxDist := 0
	for _, wt := range worktrees {
		distOutput, err := RunGit(deps, repoPath, "rev-list", "--count", mainRef+"..origin/"+wt.Branch)
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

// DetectIntegrationBranch analyzes worktree branches to find a common integration
// branch that is closer than main. Returns empty string if no such branch is found.
func DetectIntegrationBranch(worktrees []WorktreeInfo) string {
	return detectIntegrationBranchDeps(ensureDefaultDeps(), worktrees)
}

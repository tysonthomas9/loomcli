package cli

import (
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

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
// In workspace mode (Repo != nil), uses RepoConfig.DefaultBranch with "main" fallback.
// In legacy mode, returns "main" (caller should use GetDefaultBranchForWorktrees for auto-detection).
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
	// In workspace mode, worktrees may span multiple repos.
	// DetectIntegrationBranch uses worktrees[0].Path for all git ops,
	// which is meaningless across repos. Use per-repo config instead.
	for _, wt := range worktrees {
		if wt.Repo != nil {
			// Workspace mode: return first non-empty DefaultBranch, or "main"
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
	if detected := DetectIntegrationBranch(worktrees); detected != "" {
		result = detected
	}

	integrationBranchMu.Lock()
	integrationBranchCache = result
	integrationBranchCacheAt = time.Now()
	integrationBranchMu.Unlock()

	return result
}

// DetectIntegrationBranch analyzes worktree branches to find a common integration
// branch that is closer than main. Returns empty string if no such branch is found.
func DetectIntegrationBranch(worktrees []WorktreeInfo) string {
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

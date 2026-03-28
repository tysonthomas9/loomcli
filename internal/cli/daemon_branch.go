package cli

import (
	"fmt"
	"log"
	"log/slog"
)

// epicBranchName returns the branch name for an epic.
func epicBranchName(epicID string) string {
	return "epic/" + epicID
}

// EnsureWorktreeBranch switches the worktree to the target branch.
// If already on the target branch, it's a no-op. If the working tree is dirty,
// changes are stashed and discarded before switching (dirty state is ephemeral
// daemon artifacts like lock files and beads state). The remote is used for
// fetch and remote branch lookups. The fallbackRef (e.g. "origin/main") is
// used when creating a brand-new branch.
func EnsureWorktreeBranch(worktreePath, targetBranch, remote, fallbackRef string) error {
	// Check current branch
	current, err := GetCurrentBranch(worktreePath)
	if err != nil {
		return fmt.Errorf("failed to get current branch: %w", err)
	}
	if current == targetBranch {
		return nil
	}

	// Handle dirty working tree — stash and discard
	clean, err := IsCleanWorkingTree(worktreePath)
	if err != nil {
		return fmt.Errorf("failed to check working tree: %w", err)
	}
	if !clean {
		if err := discardDirtyState(worktreePath); err != nil {
			return fmt.Errorf("could not discard dirty state before branch switch: %w", err)
		}
	}

	// Fetch (non-fatal if offline)
	if err := GitFetchRemote(worktreePath, remote); err != nil {
		log.Printf("[daemon] Warning: fetch failed (continuing with local refs): %v", err)
	}

	// Try local branch first
	exists, err := BranchExistsLocally(worktreePath, targetBranch)
	if err != nil {
		return fmt.Errorf("failed to check local branch: %w", err)
	}
	if exists {
		return GitCheckout(worktreePath, targetBranch)
	}

	// Try remote tracking branch
	remoteExists, err := RemoteBranchExists(worktreePath, remote, targetBranch)
	if err != nil {
		return fmt.Errorf("failed to check remote branch: %w", err)
	}
	if remoteExists {
		return GitCheckoutNewFromRef(worktreePath, targetBranch, remote+"/"+targetBranch)
	}

	// Create new branch from fallback ref (e.g. origin/main)
	if err := GitCheckoutNewFromRef(worktreePath, targetBranch, fallbackRef); err != nil {
		// Fallback ref may not exist (no remote, or branch not pushed yet).
		// Use HEAD as a last resort — the branch will diverge from wherever
		// the worktree currently is, which is acceptable for local/test setups.
		log.Printf("[daemon] Warning: fallback ref %q failed, creating branch from HEAD", fallbackRef)
		return GitCheckoutNewFromRef(worktreePath, targetBranch, "HEAD")
	}
	return nil
}

// discardDirtyState resets tracked changes and removes non-protected untracked
// files. Protected runtime paths (.beads/, .loom/, sessions/, loom.yaml,
// AGENTS.md) are preserved so daemon/session state survives branch switches.
func discardDirtyState(worktreePath string) error {
	// Reset tracked changes (staged + unstaged modifications)
	if _, err := RunGitCommand(worktreePath, "checkout", "--", "."); err != nil {
		slog.Warn("git checkout -- . failed (may have no tracked changes)", "worktree", worktreePath, "err", err)
	}

	// Remove only non-protected untracked files
	if err := GitCleanExclude(worktreePath, protectedRuntimePaths); err != nil {
		return fmt.Errorf("selective git clean failed: %w", err)
	}

	return nil
}

// gitStashIncludeUntracked stashes all changes including untracked files.
// Returns true if a new stash entry was created.
func gitStashIncludeUntracked(dir string) (bool, error) {
	countBefore, err := getStashCount(dir)
	if err != nil {
		return false, err
	}

	if _, err := RunGitCommand(dir, "stash", "--include-untracked"); err != nil {
		return false, fmt.Errorf("git stash --include-untracked failed: %w", err)
	}

	countAfter, err := getStashCount(dir)
	if err != nil {
		return false, err
	}

	return countAfter > countBefore, nil
}

// gitStashDrop drops the most recent stash entry.
func gitStashDrop(dir string) error {
	if _, err := RunGitCommand(dir, "stash", "drop", "stash@{0}"); err != nil {
		return fmt.Errorf("git stash drop failed: %w", err)
	}
	return nil
}

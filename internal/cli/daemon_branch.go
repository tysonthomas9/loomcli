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

// discardDirtyState stashes all changes (including untracked files) and
// immediately drops the stash. The dirty state in daemon worktrees is
// ephemeral (lock files, beads state) and should not be preserved across
// branch switches.
func discardDirtyState(worktreePath string) error {
	// Stash everything including untracked files
	stashed, err := gitStashIncludeUntracked(worktreePath)
	if err != nil {
		return fmt.Errorf("git stash failed: %w", err)
	}
	if !stashed {
		// Nothing was actually stashed (e.g., only ignored files)
		return nil
	}

	// Log what we're discarding
	if show, err := RunGitCommand(worktreePath, "stash", "show", "stash@{0}"); err == nil {
		slog.Info("discarding dirty state before branch switch", "worktree", worktreePath, "files", show)
	}

	// Drop the stash — we don't need this state on the new branch
	if err := gitStashDrop(worktreePath); err != nil {
		slog.Warn("stash drop failed (benign)", "worktree", worktreePath, "err", err)
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

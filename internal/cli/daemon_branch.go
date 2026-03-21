package cli

import (
	"fmt"
	"log"
)

// epicBranchName returns the branch name for an epic.
func epicBranchName(epicID string) string {
	return "epic/" + epicID
}

// EnsureWorktreeBranch switches the worktree to the target branch.
// If already on the target branch, it's a no-op. If the working tree is dirty,
// a WIP commit is created before switching. The remote is used for fetch and
// remote branch lookups. The fallbackRef (e.g. "origin/main") is used when
// creating a brand-new branch.
func EnsureWorktreeBranch(worktreePath, targetBranch, remote, fallbackRef string) error {
	// Check current branch
	current, err := GetCurrentBranch(worktreePath)
	if err != nil {
		return fmt.Errorf("failed to get current branch: %w", err)
	}
	if current == targetBranch {
		return nil
	}

	// Handle dirty working tree
	clean, err := IsCleanWorkingTree(worktreePath)
	if err != nil {
		return fmt.Errorf("failed to check working tree: %w", err)
	}
	if !clean {
		msg := fmt.Sprintf("WIP: daemon branch switch from %s to %s", current, targetBranch)
		if err := commitWIP(worktreePath, msg); err != nil {
			log.Printf("[daemon] WIP commit failed, falling back to stash: %v", err)
			if _, stashErr := GitStash(worktreePath); stashErr != nil {
				return fmt.Errorf("dirty worktree and both WIP commit and stash failed: commit: %w, stash: %v", err, stashErr)
			}
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

// commitWIP stages all changes and creates a WIP commit.
func commitWIP(worktreePath, message string) error {
	if _, err := RunGitCommand(worktreePath, "add", "-A"); err != nil {
		return fmt.Errorf("git add failed: %w", err)
	}
	if _, err := RunGitCommand(worktreePath, "commit", "-m", message); err != nil {
		return fmt.Errorf("git commit failed: %w", err)
	}
	return nil
}

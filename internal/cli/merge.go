package cli

import (
	"fmt"
	"os"
	"strings"
)

// stashIfDirty stashes working tree changes and returns a cleanup function
// that pops the stash. If working tree is clean, cleanup is a no-op.
func stashIfDirty(repoPath string) (cleanup func(), err error) {
	noop := func() {}

	stashed, stashErr := GitStash(repoPath)
	if stashErr != nil {
		return noop, stashErr
	}

	if !stashed {
		return noop, nil
	}

	return func() {
		if err := GitStashPop(repoPath); err != nil {
			hasConflicts, _ := HasUnmergedFiles(repoPath)
			if hasConflicts {
				fmt.Println("⚠ Warning: Stash pop caused conflicts. Resolve manually with 'git stash show -p | git apply'")
			} else {
				fmt.Fprintf(os.Stderr, "Warning: failed to restore stashed changes: %v\n", err)
			}
		}
	}, nil
}

// checkoutTarget saves the current branch, checks out targetBranch, and returns
// a cleanup function that restores the original branch. The cleanup function is
// safe to call even when the checkout fails (it always restores the original branch).
func checkoutTarget(repoPath, targetBranch string) (restoreOrigBranch func(), err error) {
	origBranch, _ := GetCurrentBranch(repoPath)

	restore := func() {
		if origBranch != "" {
			_ = GitCheckout(repoPath, origBranch)
		}
	}

	if err := GitCheckout(repoPath, targetBranch); err != nil {
		return restore, err
	}

	return restore, nil
}

// isWorktreeConflictErr returns true if the error indicates the target branch
// is already checked out in another worktree.
func isWorktreeConflictErr(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return strings.Contains(errStr, "already used by worktree") ||
		strings.Contains(errStr, "already checked out")
}

// mergeSource merges sourceBranch into the current branch with the standard
// commit message. Returns nil on success, or the list of conflicted files
// and a merge error if conflicts were detected.
func mergeSource(repoPath, sourceBranch, targetBranch string) (conflicts []string, err error) {
	mergeMsg := fmt.Sprintf("Merge %s into %s\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>", sourceBranch, targetBranch)
	if err := GitMerge(repoPath, sourceBranch, mergeMsg); err != nil {
		conflicts, conflictErr := GetConflictedFiles(repoPath)
		if conflictErr != nil || len(conflicts) == 0 {
			return nil, fmt.Errorf("merge failed: %v", err)
		}
		return conflicts, fmt.Errorf("merge conflicts detected")
	}
	return nil, nil
}

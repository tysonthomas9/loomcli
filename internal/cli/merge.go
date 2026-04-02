package cli

import (
	"fmt"
	"os"
	"strings"
)

// stashIfDirty stashes working tree changes and returns a cleanup function
// that pops the stash. If working tree is clean, cleanup is a no-op.
func stashIfDirty(repoPath string) (cleanup func(), err error) {
	return stashIfDirtyDeps(defaultDeps, repoPath)
}

// stashIfDirtyDeps is the deps-aware variant of stashIfDirty.
func stashIfDirtyDeps(deps *Deps, repoPath string) (cleanup func(), err error) {
	noop := func() {}

	stashed, stashErr := gitStash(deps, repoPath)
	if stashErr != nil {
		return noop, stashErr
	}

	if !stashed {
		return noop, nil
	}

	return func() {
		if err := gitStashPop(deps, repoPath); err != nil {
			hasConflicts, _ := hasUnmergedFilesDeps(deps, repoPath)
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
	return checkoutTargetDeps(defaultDeps, repoPath, targetBranch)
}

// checkoutTargetDeps is the deps-aware variant of checkoutTarget.
func checkoutTargetDeps(deps *Deps, repoPath, targetBranch string) (restoreOrigBranch func(), err error) {
	origBranch, _ := getCurrentBranchDeps(deps, repoPath)

	restore := func() {
		if origBranch != "" {
			_ = gitCheckout(deps, repoPath, origBranch)
		}
	}

	if err := gitCheckout(deps, repoPath, targetBranch); err != nil {
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
	return mergeSourceDeps(defaultDeps, repoPath, sourceBranch, targetBranch)
}

// mergeSourceDeps is the deps-aware variant of mergeSource.
func mergeSourceDeps(deps *Deps, repoPath, sourceBranch, targetBranch string) (conflicts []string, err error) {
	mergeMsg := fmt.Sprintf("Merge %s into %s\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>", sourceBranch, targetBranch)
	if err := gitMerge(deps, repoPath, sourceBranch, mergeMsg); err != nil {
		conflicts, conflictErr := getConflictedFilesDeps(deps, repoPath)
		if conflictErr != nil || len(conflicts) == 0 {
			return nil, fmt.Errorf("merge failed: %v", err)
		}
		return conflicts, fmt.Errorf("merge conflicts detected")
	}
	return nil, nil
}

// getCurrentBranchDeps is defined in worktree.go

package cli

import (
	"fmt"
	"os"
	"time"
)

func pushAllWorkspaces(deps *Deps, targetBranch string) {
	resolver, err := NewResolver()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating resolver: %v\n", err)
		os.Exit(1)
	}

	wsNames := resolver.WorkspaceNames()
	if len(wsNames) == 0 {
		fmt.Println("No workspaces found.")
		return
	}

	fmt.Println("=========================================")
	fmt.Printf("Pushing all workspaces -> %s\n", targetBranchDisplay(targetBranch))
	fmt.Println("=========================================")
	fmt.Println("")

	for _, wsName := range wsNames {
		fmt.Printf("--- Workspace: %s ---\n", wsName)
		if err := resolver.SetWorkspace(wsName); err != nil {
			fmt.Fprintf(os.Stderr, "Error setting workspace %s: %v\n", wsName, err)
			continue
		}

		worktrees, err := resolver.DiscoverWorktrees()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error discovering repos in workspace %s: %v\n", wsName, err)
			continue
		}

		if len(worktrees) == 0 {
			fmt.Printf("No repos found in workspace %s\n", wsName)
			continue
		}

		pushWorkspaceWorktrees(deps, worktrees, "", targetBranch)
		fmt.Println("")
	}

	fmt.Println("=========================================")
	fmt.Printf("All workspaces pushed!\n")
	fmt.Println("=========================================")
}

func pushWorkspaceRepos(deps *Deps, resolver *Resolver, sourceBranch, targetBranch string) {
	worktrees, err := resolver.DiscoverWorktrees()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error discovering repos: %v\n", err)
		os.Exit(1)
	}

	if len(worktrees) == 0 {
		fmt.Printf("No repos found in workspace %s\n", resolver.WorkspaceName())
		return
	}

	fmt.Println("=========================================")
	fmt.Printf("Pushing workspace %q: %s -> %s\n", resolver.WorkspaceName(), sourceBranch, targetBranchDisplay(targetBranch))
	fmt.Println("=========================================")
	fmt.Println("")

	pushWorkspaceWorktrees(deps, worktrees, sourceBranch, targetBranch)

	fmt.Println("=========================================")
	fmt.Printf("Workspace %q push complete!\n", resolver.WorkspaceName())
	fmt.Println("=========================================")
}

func pushWorkspaceWorktrees(deps *Deps, worktrees []WorktreeInfo, sourceBranch, targetBranch string) {
	type result struct {
		repo    string
		success bool
		err     string
	}
	var results []result

	for _, wt := range worktrees {
		if wt.Repo == nil {
			continue
		}

		target := targetBranch
		if target == "" {
			target = wt.Repo.DefaultBranch
			if target == "" {
				target = "main"
			}
		}

		source := sourceBranch
		if source == "" {
			source = wt.Branch
		}

		remote := wt.Repo.Remote

		err := pushBranchInRepo(deps, wt.Path, source, target, remote)
		if err != nil {
			results = append(results, result{repo: wt.Name, success: false, err: err.Error()})
		} else {
			results = append(results, result{repo: wt.Name, success: true})
		}
		fmt.Println("")
	}

	// Print summary
	fmt.Println("--- Summary ---")
	for _, r := range results {
		if r.success {
			fmt.Printf("  ✓ %s\n", r.repo)
		} else {
			fmt.Printf("  ✗ %s: %s\n", r.repo, r.err)
		}
	}
}

func pushBranchInRepo(deps *Deps, repoPath, sourceBranch, targetBranch, remote string) error {
	r := resolveRemote(remote)

	fmt.Println("=========================================")
	fmt.Printf("Push: %s -> %s (repo: %s, remote: %s)\n", sourceBranch, targetBranch, repoPath, r)
	fmt.Println("=========================================")

	// Fetch latest
	if err := gitFetchRemote(deps, repoPath, remote); err != nil {
		return fmt.Errorf("fetching: %v", err)
	}

	// Stash local changes if working tree is dirty
	stashCleanup, err := stashIfDirtyDeps(deps, repoPath)
	if err != nil {
		return fmt.Errorf("stashing changes: %v", err)
	}
	defer stashCleanup()

	// Checkout target branch, with deferred restore of original branch
	restoreBranch, err := checkoutTargetDeps(deps, repoPath, targetBranch)
	defer restoreBranch()
	if err != nil {
		if isWorktreeConflictErr(err) {
			fmt.Printf("⚠ Target branch %s is checked out in another worktree\n", targetBranch)
			fmt.Println("⚠ Using detached HEAD approach")
			return pushBranchInRepoDetached(deps, repoPath, sourceBranch, targetBranch, remote)
		}
		return fmt.Errorf("checking out %s: %v", targetBranch, err)
	}

	// Pull latest
	if err := gitPullRemote(deps, repoPath, remote, targetBranch); err != nil {
		return fmt.Errorf("pulling %s: %v", targetBranch, err)
	}

	// Check if there are commits to merge
	hasCommits, err := hasCommitsBetweenRemoteDeps(deps, repoPath, remote, targetBranch, sourceBranch)
	if err == nil && !hasCommits {
		fmt.Printf("✓ Already up to date (no new commits in %s)\n", sourceBranch)
		return nil
	}

	// Attempt merge
	conflicts, mergeErr := mergeSourceDeps(deps, repoPath, sourceBranch, targetBranch)
	if mergeErr != nil {
		if len(conflicts) > 0 {
			if err := resolveConflictsWithAgentDeps(deps, repoPath, sourceBranch, targetBranch, conflicts); err != nil {
				return fmt.Errorf("resolving conflicts: %v", err)
			}
			return nil
		}
		return mergeErr
	}

	fmt.Println("✓ Push completed successfully (no conflicts)")

	// Push
	if err := gitPushRemote(deps, repoPath, remote, targetBranch); err != nil {
		return fmt.Errorf("pushing: %v", err)
	}

	fmt.Printf("✓ Pushed to %s/%s\n", r, targetBranch)
	return nil
}

// pushBranchInRepoDetached handles pushing when the target branch is checked out
// in another worktree. Uses detached HEAD + temp branch to avoid conflicts.
func pushBranchInRepoDetached(deps *Deps, repoPath, sourceBranch, targetBranch, remote string) error {
	r := resolveRemote(remote)
	tempBranch := fmt.Sprintf("loom-push-temp-%d", time.Now().UnixNano())

	// Checkout origin/<target> detached
	if err := gitCheckoutDetached(deps, repoPath, r+"/"+targetBranch); err != nil {
		return fmt.Errorf("checking out %s/%s detached: %v", r, targetBranch, err)
	}

	// Ensure we restore source branch on any exit path (including early return)
	defer func() {
		_ = gitCheckout(deps, repoPath, sourceBranch)
	}()

	// Check if there are commits to merge before creating temp branch
	hasCommits, err := hasCommitsBetweenRemoteDeps(deps, repoPath, remote, targetBranch, sourceBranch)
	if err == nil && !hasCommits {
		fmt.Printf("✓ Already up to date (no new commits in %s)\n", sourceBranch)
		return nil
	}

	// Create temp branch from detached HEAD
	if err := gitCreateBranchFromHead(deps, repoPath, tempBranch); err != nil {
		return fmt.Errorf("creating temp branch: %v", err)
	}

	// Cleanup temp branch on exit
	defer func() {
		_ = gitDeleteBranch(deps, repoPath, tempBranch, true)
	}()

	// Attempt merge
	conflicts, mergeErr := mergeSourceDeps(deps, repoPath, sourceBranch, targetBranch)
	if mergeErr != nil {
		if len(conflicts) > 0 {
			pushRef := fmt.Sprintf("HEAD:%s", targetBranch)
			if err := resolveConflictsDetachedDeps(deps, repoPath, sourceBranch, targetBranch, conflicts, pushRef); err != nil {
				return fmt.Errorf("resolving conflicts: %v", err)
			}
			return nil
		}
		return mergeErr
	}

	fmt.Println("✓ Push completed successfully (no conflicts)")

	// Push temp branch to remote target using refspec
	if err := gitPushRefspec(deps, repoPath, remote, tempBranch, targetBranch); err != nil {
		return fmt.Errorf("pushing: %v", err)
	}

	fmt.Printf("✓ Pushed to %s/%s\n", r, targetBranch)
	return nil
}

func targetBranchDisplay(target string) string {
	if target == "" {
		return "(per-repo default)"
	}
	return target
}

func pushAllWorktrees(deps *Deps, targetBranch string) {
	fmt.Println("=========================================")
	fmt.Printf("Pushing all worktrees -> %s\n", targetBranch)
	fmt.Println("=========================================")
	fmt.Println("")

	worktrees, err := DiscoverWorktrees()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error discovering worktrees: %v\n", err)
		os.Exit(1)
	}

	if len(worktrees) == 0 {
		fmt.Println("No worktrees found.")
		return
	}

	// List what will be pushed
	for _, wt := range worktrees {
		fmt.Printf("Found: %s -> %s\n", wt.Name, wt.Branch)
	}
	fmt.Println("")
	fmt.Printf("Will push %d branches into %s\n", len(worktrees), targetBranch)
	fmt.Println("")

	// Push each branch
	for _, wt := range worktrees {
		pushBranch(deps, wt.Branch, targetBranch)
		fmt.Println("")
	}

	fmt.Println("=========================================")
	fmt.Printf("All worktrees pushed into %s!\n", targetBranch)
	fmt.Println("=========================================")
}

func pushBranch(deps *Deps, sourceBranch, targetBranch string) {
	scriptDir, err := GetScriptDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("=========================================")
	fmt.Printf("Push: %s -> %s\n", sourceBranch, targetBranch)
	fmt.Println("=========================================")

	// Fetch latest
	if err := gitFetch(deps, scriptDir); err != nil {
		fmt.Fprintf(os.Stderr, "Error fetching: %v\n", err)
		return
	}

	// Stash local changes if working tree is dirty
	stashCleanup, err := stashIfDirtyDeps(deps, scriptDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error stashing: %v\n", err)
		return
	}
	defer stashCleanup()

	// Checkout target branch, with deferred restore of original branch
	restoreBranch, err := checkoutTargetDeps(deps, scriptDir, targetBranch)
	defer restoreBranch()
	if err != nil {
		if isWorktreeConflictErr(err) {
			fmt.Printf("⚠ Target branch %s is checked out in another worktree\n", targetBranch)
			fmt.Println("⚠ Using detached HEAD approach")
			if err := pushBranchDetached(deps, scriptDir, sourceBranch, targetBranch); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			}
			return
		}
		fmt.Fprintf(os.Stderr, "Error checking out %s: %v\n", targetBranch, err)
		return
	}

	// Pull latest
	if err := gitPull(deps, scriptDir, targetBranch); err != nil {
		fmt.Fprintf(os.Stderr, "Error pulling %s: %v\n", targetBranch, err)
		return
	}

	// Check if there are commits to merge
	hasCommits, err := hasCommitsBetweenDeps(deps, scriptDir, targetBranch, sourceBranch)
	if err == nil && !hasCommits {
		fmt.Printf("✓ Already up to date (no new commits in %s)\n", sourceBranch)
		return
	}

	// Attempt merge
	conflicts, mergeErr := mergeSourceDeps(deps, scriptDir, sourceBranch, targetBranch)
	if mergeErr != nil {
		if len(conflicts) > 0 {
			if err := resolveConflictsWithAgentDeps(deps, scriptDir, sourceBranch, targetBranch, conflicts); err != nil {
				fmt.Fprintf(os.Stderr, "Error resolving conflicts: %v\n", err)
			}
			return
		}
		fmt.Fprintf(os.Stderr, "Push failed: %v\n", mergeErr)
		return
	}

	fmt.Println("✓ Push completed successfully (no conflicts)")

	// Push
	if err := gitPush(deps, scriptDir, targetBranch); err != nil {
		fmt.Fprintf(os.Stderr, "Error pushing: %v\n", err)
		return
	}

	fmt.Printf("✓ Pushed to origin/%s\n", targetBranch)
}

// pushBranchDetached handles legacy push when target branch is checked out elsewhere.
// Uses detached HEAD + temp branch approach with "origin" as the remote.
func pushBranchDetached(deps *Deps, scriptDir, sourceBranch, targetBranch string) error {
	tempBranch := fmt.Sprintf("loom-push-temp-%d", time.Now().UnixNano())

	// Checkout origin/<target> detached
	if err := gitCheckoutDetached(deps, scriptDir, "origin/"+targetBranch); err != nil {
		return fmt.Errorf("checking out origin/%s detached: %v", targetBranch, err)
	}

	// Ensure we restore source branch on any exit path (including early return)
	defer func() {
		_ = gitCheckout(deps, scriptDir, sourceBranch)
	}()

	// Check if there are commits to merge before creating temp branch
	hasCommits, err := hasCommitsBetweenDeps(deps, scriptDir, targetBranch, sourceBranch)
	if err == nil && !hasCommits {
		fmt.Printf("✓ Already up to date (no new commits in %s)\n", sourceBranch)
		return nil
	}

	// Create temp branch from detached HEAD
	if err := gitCreateBranchFromHead(deps, scriptDir, tempBranch); err != nil {
		return fmt.Errorf("creating temp branch: %v", err)
	}

	// Cleanup temp branch on exit
	defer func() {
		_ = gitDeleteBranch(deps, scriptDir, tempBranch, true)
	}()

	// Attempt merge
	conflicts, mergeErr := mergeSourceDeps(deps, scriptDir, sourceBranch, targetBranch)
	if mergeErr != nil {
		if len(conflicts) > 0 {
			pushRef := fmt.Sprintf("HEAD:%s", targetBranch)
			if err := resolveConflictsDetachedDeps(deps, scriptDir, sourceBranch, targetBranch, conflicts, pushRef); err != nil {
				return fmt.Errorf("resolving conflicts: %v", err)
			}
			return nil
		}
		return mergeErr
	}

	fmt.Println("✓ Push completed successfully (no conflicts)")

	// Push temp branch to remote target using refspec
	if err := gitPushRefspec(deps, scriptDir, "", tempBranch, targetBranch); err != nil {
		return fmt.Errorf("pushing: %v", err)
	}

	fmt.Printf("✓ Pushed to origin/%s\n", targetBranch)
	return nil
}

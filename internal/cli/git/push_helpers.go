package git

import (
	"fmt"
	"os"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli"
)

func pushAllWorkspaces(deps *cli.Deps, targetBranch string) error {
	resolver, err := cli.NewResolver()
	if err != nil {
		return fmt.Errorf("creating resolver: %w", err)
	}

	wsNames := resolver.WorkspaceNames()
	if len(wsNames) == 0 {
		fmt.Println("No workspaces found.")
		return nil
	}

	fmt.Println("=========================================")
	fmt.Printf("Pushing all workspaces -> %s\n", targetBranchDisplay(targetBranch))
	fmt.Println("=========================================")
	fmt.Println("")

	var failures int
	for _, wsName := range wsNames {
		fmt.Printf("--- Workspace: %s ---\n", wsName)
		if err := resolver.SetWorkspace(wsName); err != nil {
			fmt.Fprintf(os.Stderr, "Error setting workspace %s: %v\n", wsName, err)
			failures++
			continue
		}

		worktrees, err := resolver.DiscoverWorktrees()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error discovering repos in workspace %s: %v\n", wsName, err)
			failures++
			continue
		}

		if len(worktrees) == 0 {
			fmt.Printf("No repos found in workspace %s\n", wsName)
			continue
		}

		if err := pushWorkspaceWorktrees(deps, worktrees, "", targetBranch); err != nil {
			failures++
		}
		fmt.Println("")
	}

	fmt.Println("=========================================")
	fmt.Printf("All workspaces pushed!\n")
	fmt.Println("=========================================")
	if failures > 0 {
		return fmt.Errorf("%d workspace(s) failed to push", failures)
	}
	return nil
}

func pushWorkspaceRepos(deps *cli.Deps, resolver *cli.Resolver, sourceBranch, targetBranch string) error {
	worktrees, err := resolver.DiscoverWorktrees()
	if err != nil {
		return fmt.Errorf("discovering repos: %w", err)
	}

	if len(worktrees) == 0 {
		fmt.Printf("No repos found in workspace %s\n", resolver.WorkspaceName())
		return nil
	}

	fmt.Println("=========================================")
	fmt.Printf("Pushing workspace %q: %s -> %s\n", resolver.WorkspaceName(), sourceBranch, targetBranchDisplay(targetBranch))
	fmt.Println("=========================================")
	fmt.Println("")

	pushErr := pushWorkspaceWorktrees(deps, worktrees, sourceBranch, targetBranch)

	fmt.Println("=========================================")
	fmt.Printf("Workspace %q push complete!\n", resolver.WorkspaceName())
	fmt.Println("=========================================")
	return pushErr
}

func pushWorkspaceWorktrees(deps *cli.Deps, worktrees []cli.WorktreeInfo, sourceBranch, targetBranch string) error {
	type result struct {
		repo    string
		success bool
		err     string
	}
	var results []result
	failures := 0

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
			failures++
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
	if failures > 0 {
		return fmt.Errorf("%d repo(s) failed to push", failures)
	}
	return nil
}

func pushBranchInRepo(deps *cli.Deps, repoPath, sourceBranch, targetBranch, remote string) error {
	r := resolveRemote(remote)

	fmt.Println("=========================================")
	fmt.Printf("Push: %s -> %s (repo: %s, remote: %s)\n", sourceBranch, targetBranch, repoPath, r)
	fmt.Println("=========================================")

	if err := gitFetchRemote(deps, repoPath, remote); err != nil {
		return fmt.Errorf("fetching: %v", err)
	}

	stashCleanup, err := stashIfDirtyDeps(deps, repoPath)
	if err != nil {
		return fmt.Errorf("stashing changes: %v", err)
	}
	defer stashCleanup()

	restoreBranch, err := checkoutTargetDeps(deps, repoPath, targetBranch)
	defer restoreBranch()
	if err != nil {
		return handlePushCheckoutErr(deps, err, repoPath, sourceBranch, targetBranch, remote)
	}

	return pushAfterCheckoutInRepo(deps, repoPath, sourceBranch, targetBranch, remote, r)
}

// handlePushCheckoutErr handles checkout errors during pushBranchInRepo.
func handlePushCheckoutErr(deps *cli.Deps, err error, repoPath, sourceBranch, targetBranch, remote string) error {
	if isWorktreeConflictErr(err) {
		fmt.Printf("⚠ Target branch %s is checked out in another worktree\n", targetBranch)
		fmt.Println("⚠ Using detached HEAD approach")
		return pushBranchInRepoDetached(deps, repoPath, sourceBranch, targetBranch, remote)
	}
	return fmt.Errorf("checking out %s: %v", targetBranch, err)
}

// pushAfterCheckoutInRepo performs pull, merge, and push after successful checkout.
func pushAfterCheckoutInRepo(deps *cli.Deps, repoPath, sourceBranch, targetBranch, remote, r string) error {
	if err := gitPullRemote(deps, repoPath, remote, targetBranch); err != nil {
		return fmt.Errorf("pulling %s: %v", targetBranch, err)
	}

	hasCommits, err := hasCommitsBetweenRemoteDeps(deps, repoPath, remote, targetBranch, sourceBranch)
	if err == nil && !hasCommits {
		fmt.Printf("✓ Already up to date (no new commits in %s)\n", sourceBranch)
		return nil
	}

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
	if err := gitPushRemote(deps, repoPath, remote, targetBranch); err != nil {
		return fmt.Errorf("pushing: %v", err)
	}
	fmt.Printf("✓ Pushed to %s/%s\n", r, targetBranch)
	return nil
}

// pushBranchInRepoDetached handles pushing when the target branch is checked out
// in another worktree. Uses detached HEAD + temp branch to avoid conflicts.
func pushBranchInRepoDetached(deps *cli.Deps, repoPath, sourceBranch, targetBranch, remote string) error {
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

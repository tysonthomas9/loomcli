package cli

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var pushAll bool
var pushWorkspace string

var pushCmd = &cobra.Command{
	Use:               "push [worktree] [target]",
	Short:             "Push worktree branch to target branch",
	Aliases:           []string{"merge"},
	GroupID:           "git",
	ValidArgsFunction: branchCompletion,
	Long: `Push completed work from worktree branches to target branch.

Merges worktree branch INTO the target branch (e.g., main), pushing
completed work. If conflicts occur, Claude is launched to resolve them.

Arguments:
  worktree    Source worktree to push from (e.g., falcon)
  target      Target branch to push into (default: main or per-repo default)

Flags:
  -a, --all          Push all worktree branches to target
  -W, --workspace    Workspace to operate on (workspace mode only)

Examples:
  loom push falcon                        # Push falcon to main (or per-repo default)
  loom push falcon main                   # Push falcon to main explicitly
  loom push --all                         # Push all worktrees to their default targets
  loom push --all main                    # Push all worktrees to main
  loom push -W myworkspace falcon         # Push in specific workspace`,
	Args: func(cmd *cobra.Command, args []string) error {
		if IsWorkspaceMode() {
			if pushAll {
				if len(args) > 1 {
					return fmt.Errorf("--all flag accepts at most 1 argument (target branch)")
				}
				return nil
			}
			if len(args) < 1 || len(args) > 2 {
				return fmt.Errorf("requires 1-2 arguments: <worktree> [target]")
			}
			return nil
		}
		// Legacy mode
		if pushAll {
			if len(args) != 1 {
				return fmt.Errorf("--all flag requires exactly 1 argument (target branch)")
			}
			return nil
		}
		if len(args) != 2 {
			return fmt.Errorf("requires exactly 2 arguments: <source> <target>")
		}
		return nil
	},
	Run: runPush,
}

func init() {
	pushCmd.Flags().BoolVarP(&pushAll, "all", "a", false, "Push all worktree branches to target")
	pushCmd.Flags().StringVarP(&pushWorkspace, "workspace", "W", "", "Workspace to operate on")
	rootCmd.AddCommand(pushCmd)
}

func runPush(cmd *cobra.Command, args []string) {
	if IsWorkspaceMode() {
		if pushAll && pushWorkspace != "" {
			fmt.Fprintln(os.Stderr, "Error: --all and --workspace are mutually exclusive")
			os.Exit(1)
		}

		targetBranch := ""
		sourceBranch := ""

		if pushAll {
			if len(args) == 1 {
				targetBranch = args[0]
			}
			pushAllWorkspaces(targetBranch)
		} else {
			sourceBranch = args[0]
			if len(args) == 2 {
				targetBranch = args[1]
			}

			resolver, err := NewResolver()
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error creating resolver: %v\n", err)
				os.Exit(1)
			}

			wsName := pushWorkspace
			if wsName == "" {
				wsName = resolver.WorkspaceName()
			} else {
				if err := resolver.SetWorkspace(wsName); err != nil {
					available := resolver.WorkspaceNames()
					fmt.Fprintf(os.Stderr, "Error: workspace %q not found. Available: %v\n", wsName, available)
					os.Exit(1)
				}
			}

			pushWorkspaceRepos(resolver, sourceBranch, targetBranch)
		}
		return
	}

	// Legacy mode
	if pushAll {
		pushAllWorktrees(args[0])
	} else {
		pushBranch(args[0], args[1])
	}
}

func pushAllWorkspaces(targetBranch string) {
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

		pushWorkspaceWorktrees(worktrees, "", targetBranch)
		fmt.Println("")
	}

	fmt.Println("=========================================")
	fmt.Printf("All workspaces pushed!\n")
	fmt.Println("=========================================")
}

func pushWorkspaceRepos(resolver *Resolver, sourceBranch, targetBranch string) {
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

	pushWorkspaceWorktrees(worktrees, sourceBranch, targetBranch)

	fmt.Println("=========================================")
	fmt.Printf("Workspace %q push complete!\n", resolver.WorkspaceName())
	fmt.Println("=========================================")
}

func pushWorkspaceWorktrees(worktrees []WorktreeInfo, sourceBranch, targetBranch string) {
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

		err := pushBranchInRepo(wt.Path, source, target, remote)
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

func pushBranchInRepo(repoPath, sourceBranch, targetBranch, remote string) error {
	r := resolveRemote(remote)

	fmt.Println("=========================================")
	fmt.Printf("Push: %s -> %s (repo: %s, remote: %s)\n", sourceBranch, targetBranch, repoPath, r)
	fmt.Println("=========================================")

	// Fetch latest
	if err := GitFetchRemote(repoPath, remote); err != nil {
		return fmt.Errorf("fetching: %v", err)
	}

	// Stash local changes if working tree is dirty
	stashed, stashErr := GitStash(repoPath)
	if stashErr != nil {
		return fmt.Errorf("stashing changes: %v", stashErr)
	}
	// Ensure we pop stash at end (even on error)
	if stashed {
		defer func() {
			if err := GitStashPop(repoPath); err != nil {
				// Check if stash pop caused conflicts
				hasConflicts, _ := HasUnmergedFiles(repoPath)
				if hasConflicts {
					fmt.Println("⚠ Warning: Stash pop caused conflicts. Resolve manually with 'git stash show -p | git apply'")
				} else {
					fmt.Fprintf(os.Stderr, "Warning: failed to restore stashed changes: %v\n", err)
				}
			}
		}()
	}

	// Save current branch so we can restore it after the operation
	origBranch, _ := GetCurrentBranch(repoPath)
	defer func() {
		if origBranch != "" {
			_ = GitCheckout(repoPath, origBranch)
		}
	}()

	// Checkout target branch — if it's checked out in another worktree, git
	// will fail and we fall back to the detached HEAD approach. This avoids a
	// TOCTOU race where the worktree state could change between a pre-check
	// and the checkout.
	if err := GitCheckout(repoPath, targetBranch); err != nil {
		errStr := err.Error()
		if strings.Contains(errStr, "already used by worktree") ||
			strings.Contains(errStr, "already checked out") {
			fmt.Printf("⚠ Target branch %s is checked out in another worktree\n", targetBranch)
			fmt.Println("⚠ Using detached HEAD approach")
			return pushBranchInRepoDetached(repoPath, sourceBranch, targetBranch, remote)
		}
		return fmt.Errorf("checking out %s: %v", targetBranch, err)
	}

	// Pull latest
	if err := GitPullRemote(repoPath, remote, targetBranch); err != nil {
		return fmt.Errorf("pulling %s: %v", targetBranch, err)
	}

	// Check if there are commits to merge
	hasCommits, err := HasCommitsBetweenRemote(repoPath, remote, targetBranch, sourceBranch)
	if err == nil && !hasCommits {
		fmt.Printf("✓ Already up to date (no new commits in %s)\n", sourceBranch)
		return nil
	}

	// Attempt merge
	mergeMsg := fmt.Sprintf("Merge %s into %s\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>", sourceBranch, targetBranch)
	if err := GitMerge(repoPath, sourceBranch, mergeMsg); err != nil {
		// Check for conflicts
		conflicts, conflictErr := GetConflictedFiles(repoPath)
		if conflictErr != nil || len(conflicts) == 0 {
			return fmt.Errorf("merge failed: %v", err)
		}

		fmt.Println("")
		fmt.Println("⚠ Merge conflicts detected. Launching AI agent to resolve...")
		fmt.Println("")
		fmt.Println("Conflicted files:")
		for _, f := range conflicts {
			fmt.Printf("  - %s\n", f)
		}
		fmt.Println("")

		// Launch Claude for conflict resolution
		if err := InvokeAgentForConflicts(repoPath, sourceBranch, targetBranch, conflicts); err != nil {
			return fmt.Errorf("resolving conflicts: %v", err)
		}
		return nil
	}

	fmt.Println("✓ Push completed successfully (no conflicts)")

	// Push
	if err := GitPushRemote(repoPath, remote, targetBranch); err != nil {
		return fmt.Errorf("pushing: %v", err)
	}

	fmt.Printf("✓ Pushed to %s/%s\n", r, targetBranch)
	return nil
}

// pushBranchInRepoDetached handles pushing when the target branch is checked out
// in another worktree. Uses detached HEAD + temp branch to avoid conflicts.
func pushBranchInRepoDetached(repoPath, sourceBranch, targetBranch, remote string) error {
	r := resolveRemote(remote)
	tempBranch := fmt.Sprintf("loom-push-temp-%d", time.Now().UnixNano())

	// Checkout origin/<target> detached
	if err := GitCheckoutDetached(repoPath, r+"/"+targetBranch); err != nil {
		return fmt.Errorf("checking out %s/%s detached: %v", r, targetBranch, err)
	}

	// Ensure we restore source branch on any exit path (including early return)
	defer func() {
		_ = GitCheckout(repoPath, sourceBranch)
	}()

	// Check if there are commits to merge before creating temp branch
	hasCommits, err := HasCommitsBetweenRemote(repoPath, remote, targetBranch, sourceBranch)
	if err == nil && !hasCommits {
		fmt.Printf("✓ Already up to date (no new commits in %s)\n", sourceBranch)
		return nil
	}

	// Create temp branch from detached HEAD
	if err := GitCreateBranchFromHead(repoPath, tempBranch); err != nil {
		return fmt.Errorf("creating temp branch: %v", err)
	}

	// Cleanup temp branch on exit
	defer func() {
		_ = GitDeleteBranch(repoPath, tempBranch, true)
	}()

	// Attempt merge
	mergeMsg := fmt.Sprintf("Merge %s into %s\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>", sourceBranch, targetBranch)
	if err := GitMerge(repoPath, sourceBranch, mergeMsg); err != nil {
		// Check for conflicts
		conflicts, conflictErr := GetConflictedFiles(repoPath)
		if conflictErr != nil || len(conflicts) == 0 {
			return fmt.Errorf("merge failed: %v", err)
		}

		fmt.Println("")
		fmt.Println("⚠ Merge conflicts detected. Launching AI agent to resolve...")
		fmt.Println("")
		fmt.Println("Conflicted files:")
		for _, f := range conflicts {
			fmt.Printf("  - %s\n", f)
		}
		fmt.Println("")

		// Launch Claude with push command using refspec for detached approach
		prompt := generateConflictResolutionPromptWithPush(sourceBranch, targetBranch, conflicts, fmt.Sprintf("HEAD:%s", targetBranch))
		if err := InvokeAgent(repoPath, prompt, ""); err != nil {
			return fmt.Errorf("resolving conflicts: %v", err)
		}
		return nil
	}

	fmt.Println("✓ Push completed successfully (no conflicts)")

	// Push temp branch to remote target using refspec
	if err := GitPushRefspec(repoPath, remote, tempBranch, targetBranch); err != nil {
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

func pushAllWorktrees(targetBranch string) {
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
		pushBranch(wt.Branch, targetBranch)
		fmt.Println("")
	}

	fmt.Println("=========================================")
	fmt.Printf("All worktrees pushed into %s!\n", targetBranch)
	fmt.Println("=========================================")
}

func pushBranch(sourceBranch, targetBranch string) {
	scriptDir, err := GetScriptDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("=========================================")
	fmt.Printf("Push: %s -> %s\n", sourceBranch, targetBranch)
	fmt.Println("=========================================")

	// Fetch latest
	if err := GitFetch(scriptDir); err != nil {
		fmt.Fprintf(os.Stderr, "Error fetching: %v\n", err)
		return
	}

	// Stash local changes if working tree is dirty
	stashed, stashErr := GitStash(scriptDir)
	if stashErr != nil {
		fmt.Fprintf(os.Stderr, "Error stashing: %v\n", stashErr)
		return
	}
	if stashed {
		defer func() {
			if err := GitStashPop(scriptDir); err != nil {
				hasConflicts, _ := HasUnmergedFiles(scriptDir)
				if hasConflicts {
					fmt.Println("⚠ Warning: Stash pop caused conflicts. Resolve manually with 'git stash show -p | git apply'")
				} else {
					fmt.Fprintf(os.Stderr, "Warning: failed to restore stashed changes: %v\n", err)
				}
			}
		}()
	}

	// Save current branch so we can restore it after the operation
	origBranch, _ := GetCurrentBranch(scriptDir)
	defer func() {
		if origBranch != "" {
			_ = GitCheckout(scriptDir, origBranch)
		}
	}()

	// Checkout target branch — if it's checked out in another worktree, git
	// will fail and we fall back to the detached HEAD approach.
	if err := GitCheckout(scriptDir, targetBranch); err != nil {
		errStr := err.Error()
		if strings.Contains(errStr, "already used by worktree") ||
			strings.Contains(errStr, "already checked out") {
			fmt.Printf("⚠ Target branch %s is checked out in another worktree\n", targetBranch)
			fmt.Println("⚠ Using detached HEAD approach")
			if err := pushBranchDetached(scriptDir, sourceBranch, targetBranch); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			}
			return
		}
		fmt.Fprintf(os.Stderr, "Error checking out %s: %v\n", targetBranch, err)
		return
	}

	// Pull latest
	if err := GitPull(scriptDir, targetBranch); err != nil {
		fmt.Fprintf(os.Stderr, "Error pulling %s: %v\n", targetBranch, err)
		return
	}

	// Check if there are commits to merge
	hasCommits, err := HasCommitsBetween(scriptDir, targetBranch, sourceBranch)
	if err == nil && !hasCommits {
		fmt.Printf("✓ Already up to date (no new commits in %s)\n", sourceBranch)
		return
	}

	// Attempt merge
	mergeMsg := fmt.Sprintf("Merge %s into %s\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>", sourceBranch, targetBranch)
	if err := GitMerge(scriptDir, sourceBranch, mergeMsg); err != nil {
		// Check for conflicts
		conflicts, conflictErr := GetConflictedFiles(scriptDir)
		if conflictErr != nil || len(conflicts) == 0 {
			fmt.Fprintf(os.Stderr, "Push failed: %v\n", err)
			return
		}

		fmt.Println("")
		fmt.Println("⚠ Merge conflicts detected. Launching AI agent to resolve...")
		fmt.Println("")
		fmt.Println("Conflicted files:")
		for _, f := range conflicts {
			fmt.Printf("  - %s\n", f)
		}
		fmt.Println("")

		// Launch Claude for conflict resolution
		if err := InvokeAgentForConflicts(scriptDir, sourceBranch, targetBranch, conflicts); err != nil {
			fmt.Fprintf(os.Stderr, "Error resolving conflicts: %v\n", err)
			return
		}
		return
	}

	fmt.Println("✓ Push completed successfully (no conflicts)")

	// Push
	if err := GitPush(scriptDir, targetBranch); err != nil {
		fmt.Fprintf(os.Stderr, "Error pushing: %v\n", err)
		return
	}

	fmt.Printf("✓ Pushed to origin/%s\n", targetBranch)
}

// pushBranchDetached handles legacy push when target branch is checked out elsewhere.
// Uses detached HEAD + temp branch approach with "origin" as the remote.
func pushBranchDetached(scriptDir, sourceBranch, targetBranch string) error {
	tempBranch := fmt.Sprintf("loom-push-temp-%d", time.Now().UnixNano())

	// Checkout origin/<target> detached
	if err := GitCheckoutDetached(scriptDir, "origin/"+targetBranch); err != nil {
		return fmt.Errorf("checking out origin/%s detached: %v", targetBranch, err)
	}

	// Ensure we restore source branch on any exit path (including early return)
	defer func() {
		_ = GitCheckout(scriptDir, sourceBranch)
	}()

	// Check if there are commits to merge before creating temp branch
	hasCommits, err := HasCommitsBetween(scriptDir, targetBranch, sourceBranch)
	if err == nil && !hasCommits {
		fmt.Printf("✓ Already up to date (no new commits in %s)\n", sourceBranch)
		return nil
	}

	// Create temp branch from detached HEAD
	if err := GitCreateBranchFromHead(scriptDir, tempBranch); err != nil {
		return fmt.Errorf("creating temp branch: %v", err)
	}

	// Cleanup temp branch on exit
	defer func() {
		_ = GitDeleteBranch(scriptDir, tempBranch, true)
	}()

	// Attempt merge
	mergeMsg := fmt.Sprintf("Merge %s into %s\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>", sourceBranch, targetBranch)
	if err := GitMerge(scriptDir, sourceBranch, mergeMsg); err != nil {
		// Check for conflicts
		conflicts, conflictErr := GetConflictedFiles(scriptDir)
		if conflictErr != nil || len(conflicts) == 0 {
			return fmt.Errorf("merge failed: %v", err)
		}

		fmt.Println("")
		fmt.Println("⚠ Merge conflicts detected. Launching AI agent to resolve...")
		fmt.Println("")
		fmt.Println("Conflicted files:")
		for _, f := range conflicts {
			fmt.Printf("  - %s\n", f)
		}
		fmt.Println("")

		// Launch Claude with push command using refspec for detached approach
		prompt := generateConflictResolutionPromptWithPush(sourceBranch, targetBranch, conflicts, fmt.Sprintf("HEAD:%s", targetBranch))
		if err := InvokeAgent(scriptDir, prompt, ""); err != nil {
			return fmt.Errorf("resolving conflicts: %v", err)
		}
		return nil
	}

	fmt.Println("✓ Push completed successfully (no conflicts)")

	// Push temp branch to remote target using refspec
	if err := GitPushRefspec(scriptDir, "", tempBranch, targetBranch); err != nil {
		return fmt.Errorf("pushing: %v", err)
	}

	fmt.Printf("✓ Pushed to origin/%s\n", targetBranch)
	return nil
}

package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var mergeAll bool
var mergeWorkspace string

var mergeCmd = &cobra.Command{
	Use:               "merge <source> <target>",
	Short:             "Merge branches with AI conflict resolution",
	GroupID:           "git",
	ValidArgsFunction: branchCompletion,
	Long: `Merge branches with AI-assisted conflict resolution.

When conflicts occur, Claude is launched to resolve them automatically.

Arguments:
  source    Source branch to merge from (e.g., webui/falcon)
  target    Target branch to merge into (required in legacy mode, optional in workspace mode)

Flags:
  -a, --all          Merge all worktree branches into target (legacy) or all workspaces (workspace mode)
  -W, --workspace    Workspace to operate on (workspace mode only)

Examples:
  loom merge webui/falcon main             # Merge to main (legacy)
  loom merge webui/falcon feature/web-ui   # Merge to feature/web-ui (legacy)
  loom merge --all main                    # Merge all worktrees to main (legacy)
  loom merge falcon                        # Merge source in workspace (uses per-repo default_branch)
  loom merge falcon main                   # Merge source in workspace to explicit target
  loom merge --all                         # Merge all workspaces (uses per-repo default_branch)
  loom merge --all main                    # Merge all workspaces to explicit target
  loom merge -W myworkspace falcon         # Merge in specific workspace`,
	Args: func(cmd *cobra.Command, args []string) error {
		if IsWorkspaceMode() {
			if mergeAll {
				if len(args) > 1 {
					return fmt.Errorf("--all flag accepts at most 1 argument (target branch)")
				}
				return nil
			}
			if len(args) < 1 || len(args) > 2 {
				return fmt.Errorf("requires 1-2 arguments: <source> [target]")
			}
			return nil
		}
		// Legacy mode
		if mergeAll {
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
	Run: runMerge,
}

func init() {
	mergeCmd.Flags().BoolVarP(&mergeAll, "all", "a", false, "Merge all worktree branches into target")
	mergeCmd.Flags().StringVarP(&mergeWorkspace, "workspace", "W", "", "Workspace to operate on")
	rootCmd.AddCommand(mergeCmd)
}

func runMerge(cmd *cobra.Command, args []string) {
	if IsWorkspaceMode() {
		if mergeAll && mergeWorkspace != "" {
			fmt.Fprintln(os.Stderr, "Error: --all and --workspace are mutually exclusive")
			os.Exit(1)
		}

		targetBranch := ""
		sourceBranch := ""

		if mergeAll {
			if len(args) == 1 {
				targetBranch = args[0]
			}
			mergeAllWorkspaces(targetBranch)
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

			wsName := mergeWorkspace
			if wsName == "" {
				wsName = resolver.WorkspaceName()
			} else {
				if err := resolver.SetWorkspace(wsName); err != nil {
					available := resolver.WorkspaceNames()
					fmt.Fprintf(os.Stderr, "Error: workspace %q not found. Available: %v\n", wsName, available)
					os.Exit(1)
				}
			}

			mergeWorkspaceRepos(resolver, sourceBranch, targetBranch)
		}
		return
	}

	// Legacy mode
	if mergeAll {
		mergeAllWorktrees(args[0])
	} else {
		mergeBranch(args[0], args[1])
	}
}

func mergeAllWorkspaces(targetBranch string) {
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
	fmt.Printf("Merging all workspaces -> %s\n", targetBranchDisplay(targetBranch))
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

		mergeWorkspaceWorktrees(worktrees, "", targetBranch)
		fmt.Println("")
	}

	fmt.Println("=========================================")
	fmt.Printf("All workspaces merged!\n")
	fmt.Println("=========================================")
}

func mergeWorkspaceRepos(resolver *Resolver, sourceBranch, targetBranch string) {
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
	fmt.Printf("Merging workspace %q: %s -> %s\n", resolver.WorkspaceName(), sourceBranch, targetBranchDisplay(targetBranch))
	fmt.Println("=========================================")
	fmt.Println("")

	mergeWorkspaceWorktrees(worktrees, sourceBranch, targetBranch)

	fmt.Println("=========================================")
	fmt.Printf("Workspace %q merge complete!\n", resolver.WorkspaceName())
	fmt.Println("=========================================")
}

func mergeWorkspaceWorktrees(worktrees []WorktreeInfo, sourceBranch, targetBranch string) {
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

		err := mergeBranchInRepo(wt.Path, source, target, remote)
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

func mergeBranchInRepo(repoPath, sourceBranch, targetBranch, remote string) error {
	r := resolveRemote(remote)

	fmt.Println("=========================================")
	fmt.Printf("Merge: %s -> %s (repo: %s, remote: %s)\n", sourceBranch, targetBranch, repoPath, r)
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

	// Checkout target branch
	if err := GitCheckout(repoPath, targetBranch); err != nil {
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
	if err := GitMergeRemote(repoPath, remote, sourceBranch, mergeMsg); err != nil {
		// Check for conflicts
		conflicts, conflictErr := GetConflictedFiles(repoPath)
		if conflictErr != nil || len(conflicts) == 0 {
			return fmt.Errorf("merge failed: %v", err)
		}

		fmt.Println("")
		fmt.Println("⚠ Merge conflicts detected. Launching Claude to resolve...")
		fmt.Println("")
		fmt.Println("Conflicted files:")
		for _, f := range conflicts {
			fmt.Printf("  - %s\n", f)
		}
		fmt.Println("")

		// Launch Claude for conflict resolution
		if err := InvokeClaudeForConflicts(repoPath, sourceBranch, targetBranch, conflicts); err != nil {
			return fmt.Errorf("resolving conflicts: %v", err)
		}
		return nil
	}

	fmt.Println("✓ Merge completed successfully (no conflicts)")

	// Push
	if err := GitPushRemote(repoPath, remote, targetBranch); err != nil {
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

func mergeAllWorktrees(targetBranch string) {
	fmt.Println("=========================================")
	fmt.Printf("Merging all worktrees -> %s\n", targetBranch)
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

	// List what will be merged
	for _, wt := range worktrees {
		fmt.Printf("Found: %s -> %s\n", wt.Name, wt.Branch)
	}
	fmt.Println("")
	fmt.Printf("Will merge %d branches into %s\n", len(worktrees), targetBranch)
	fmt.Println("")

	// Merge each branch
	for _, wt := range worktrees {
		mergeBranch(wt.Branch, targetBranch)
		fmt.Println("")
	}

	fmt.Println("=========================================")
	fmt.Printf("All worktrees merged into %s!\n", targetBranch)
	fmt.Println("=========================================")
}

func mergeBranch(sourceBranch, targetBranch string) {
	scriptDir, err := GetScriptDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("=========================================")
	fmt.Printf("Merge: %s -> %s\n", sourceBranch, targetBranch)
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

	// Checkout target branch
	if err := GitCheckout(scriptDir, targetBranch); err != nil {
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
	if err := GitMergeOrigin(scriptDir, sourceBranch, mergeMsg); err != nil {
		// Check for conflicts
		conflicts, conflictErr := GetConflictedFiles(scriptDir)
		if conflictErr != nil || len(conflicts) == 0 {
			fmt.Fprintf(os.Stderr, "Merge failed: %v\n", err)
			return
		}

		fmt.Println("")
		fmt.Println("⚠ Merge conflicts detected. Launching Claude to resolve...")
		fmt.Println("")
		fmt.Println("Conflicted files:")
		for _, f := range conflicts {
			fmt.Printf("  - %s\n", f)
		}
		fmt.Println("")

		// Launch Claude for conflict resolution
		if err := InvokeClaudeForConflicts(scriptDir, sourceBranch, targetBranch, conflicts); err != nil {
			fmt.Fprintf(os.Stderr, "Error resolving conflicts: %v\n", err)
			return
		}
		return
	}

	fmt.Println("✓ Merge completed successfully (no conflicts)")

	// Push
	if err := GitPush(scriptDir, targetBranch); err != nil {
		fmt.Fprintf(os.Stderr, "Error pushing: %v\n", err)
		return
	}

	fmt.Printf("✓ Pushed to origin/%s\n", targetBranch)
}

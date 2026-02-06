package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var mergeAll bool

var mergeCmd = &cobra.Command{
	Use:               "merge <source> [target]",
	Short:             "Merge branches with AI conflict resolution",
	GroupID:           "git",
	ValidArgsFunction: branchCompletion,
	Long: `Merge branches with AI-assisted conflict resolution.

When conflicts occur, Claude is launched to resolve them automatically.

Arguments:
  source    Source branch to merge from (e.g., webui/falcon)
  target    Target branch to merge into (default: feature/web-ui)

Flags:
  -a, --all    Merge all worktree branches into target

Examples:
  vibecli merge webui/falcon                  # Merge to feature/web-ui
  vibecli merge webui/falcon main             # Merge to main
  vibecli merge --all                         # Merge all worktrees to feature/web-ui
  vibecli merge --all main                    # Merge all worktrees to main`,
	Args: func(cmd *cobra.Command, args []string) error {
		if mergeAll {
			if len(args) > 1 {
				return fmt.Errorf("--all flag accepts at most 1 argument (target branch)")
			}
			return nil
		}
		if len(args) < 1 {
			return fmt.Errorf("requires source branch argument (or use --all)")
		}
		return nil
	},
	Run: runMerge,
}

func init() {
	mergeCmd.Flags().BoolVarP(&mergeAll, "all", "a", false, "Merge all worktree branches into target")
	rootCmd.AddCommand(mergeCmd)
}

func runMerge(cmd *cobra.Command, args []string) {
	defaultBranch := GetDefaultBranch()

	if mergeAll {
		// Merge all worktrees
		targetBranch := defaultBranch
		if len(args) > 0 {
			targetBranch = args[0]
		}
		mergeAllWorktrees(targetBranch)
	} else {
		// Single branch merge
		sourceBranch := args[0]
		targetBranch := defaultBranch
		if len(args) > 1 {
			targetBranch = args[1]
		}
		mergeBranch(sourceBranch, targetBranch)
	}
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
	if !HasCommitsBetween(scriptDir, targetBranch, sourceBranch) {
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

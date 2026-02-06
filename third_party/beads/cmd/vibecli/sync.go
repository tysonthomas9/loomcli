package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var syncAll bool

var syncCmd = &cobra.Command{
	Use:               "sync <worktree> [branch]",
	Short:             "Sync worktree with integration branch",
	GroupID:           "git",
	ValidArgsFunction: worktreeThenBranchCompletion,
	Long: `Sync worktree(s) with an integration branch.

Merges the integration branch INTO the worktree branch, updating
the worktree with the latest changes. If conflicts occur, Claude
is launched to resolve them.

Arguments:
  worktree    Worktree name (e.g., falcon)
  branch      Source branch to sync from (default: feature/web-ui)

Flags:
  -a, --all    Sync all worktrees

Examples:
  vibecli sync falcon                         # Sync falcon with feature/web-ui
  vibecli sync falcon main                    # Sync falcon with main
  vibecli sync --all                          # Sync all worktrees
  vibecli sync --all main                     # Sync all worktrees with main`,
	Args: func(cmd *cobra.Command, args []string) error {
		if syncAll {
			if len(args) > 1 {
				return fmt.Errorf("--all flag accepts at most 1 argument (source branch)")
			}
			return nil
		}
		if len(args) < 1 {
			return fmt.Errorf("requires worktree argument (or use --all)")
		}
		return nil
	},
	Run: runSync,
}

func init() {
	syncCmd.Flags().BoolVarP(&syncAll, "all", "a", false, "Sync all worktrees")
	rootCmd.AddCommand(syncCmd)
}

func runSync(cmd *cobra.Command, args []string) {
	defaultBranch := GetDefaultBranch()

	if syncAll {
		// Sync all worktrees
		sourceBranch := defaultBranch
		if len(args) > 0 {
			sourceBranch = args[0]
		}
		syncAllWorktrees(sourceBranch)
	} else {
		// Single worktree sync
		worktreeName := args[0]
		sourceBranch := defaultBranch
		if len(args) > 1 {
			sourceBranch = args[1]
		}
		syncWorktree(worktreeName, sourceBranch)
	}
}

func syncAllWorktrees(sourceBranch string) {
	fmt.Println("=========================================")
	fmt.Printf("Syncing all worktrees <- %s\n", sourceBranch)
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

	// Sync each worktree
	for _, wt := range worktrees {
		syncWorktree(wt.Name, sourceBranch)
		fmt.Println("")
	}

	fmt.Println("=========================================")
	fmt.Println("All worktrees synced!")
	fmt.Println("=========================================")
}

func syncWorktree(worktreeName, sourceBranch string) {
	worktreePath, err := ResolveWorktreePath(worktreeName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return
	}

	fmt.Println("=========================================")
	fmt.Printf("Syncing: %s <- %s\n", worktreeName, sourceBranch)
	fmt.Println("=========================================")

	// Get current branch
	currentBranch, err := GetCurrentBranch(worktreePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting current branch: %v\n", err)
		return
	}

	// Fetch latest
	if err := GitFetch(worktreePath); err != nil {
		fmt.Fprintf(os.Stderr, "Error fetching: %v\n", err)
		return
	}

	// Attempt merge
	mergeMsg := fmt.Sprintf("Sync with %s\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>", sourceBranch)
	if err := GitMergeOrigin(worktreePath, sourceBranch, mergeMsg); err != nil {
		// Check for conflicts
		conflicts, conflictErr := GetConflictedFiles(worktreePath)
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
		if err := InvokeClaudeForConflicts(worktreePath, sourceBranch, currentBranch, conflicts); err != nil {
			fmt.Fprintf(os.Stderr, "Error resolving conflicts: %v\n", err)
			return
		}
		return
	}

	fmt.Println("✓ Sync completed successfully (no conflicts)")

	// Push
	if err := GitPush(worktreePath, currentBranch); err != nil {
		fmt.Fprintf(os.Stderr, "Error pushing: %v\n", err)
		return
	}

	fmt.Printf("✓ Pushed to origin/%s\n", currentBranch)
}

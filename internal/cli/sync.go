package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var syncAll bool

var syncCmd = &cobra.Command{
	Use:               "sync <worktree> <branch>",
	Short:             "Sync worktree with integration branch",
	GroupID:           "git",
	ValidArgsFunction: worktreeThenBranchCompletion,
	Long: `Sync worktree(s) with an integration branch.

Merges the integration branch INTO the worktree branch, updating
the worktree with the latest changes. If conflicts occur, Claude
is launched to resolve them.

Arguments:
  worktree    Worktree name (e.g., falcon)
  branch      Source branch to sync from (required)

Flags:
  -a, --all    Sync all worktrees

Examples:
  loom sync falcon main                    # Sync falcon with main
  loom sync falcon feature/web-ui          # Sync falcon with feature/web-ui
  loom sync --all main                     # Sync all worktrees with main`,
	Args: func(cmd *cobra.Command, args []string) error {
		if syncAll {
			if len(args) != 1 {
				return fmt.Errorf("--all flag requires exactly 1 argument (source branch)")
			}
			return nil
		}
		if len(args) != 2 {
			return fmt.Errorf("requires exactly 2 arguments: <worktree> <branch>")
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
	if syncAll {
		// Sync all worktrees with the specified branch
		syncAllWorktrees(args[0])
	} else {
		// Single worktree sync
		syncWorktree(args[0], args[1])
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

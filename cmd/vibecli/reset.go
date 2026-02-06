package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

var (
	resetAll   bool
	resetForce bool
)

var resetCmd = &cobra.Command{
	Use:               "reset <worktree> [branch]",
	Short:             "Hard reset worktree to a specific branch",
	GroupID:           "git",
	ValidArgsFunction: worktreeThenBranchCompletion,
	Long: `Hard reset worktree(s) to a specific branch.

WARNING: This discards ALL local changes!

This command will:
  1. Discard all local changes (git reset --hard, git clean -fd)
  2. Reset to the target branch (origin/branch)
  3. Force push the worktree branch to match

Arguments:
  worktree    Worktree name (e.g., falcon)
  branch      Target branch to reset to (default: feature/web-ui)

Flags:
  -a, --all      Reset all worktrees
  -f, --force    Skip confirmation prompt

Examples:
  vibecli reset falcon                        # Reset falcon to feature/web-ui
  vibecli reset falcon main                   # Reset falcon to main
  vibecli reset --all                         # Reset all worktrees (with confirmation)
  vibecli reset --all --force                 # Reset all worktrees (no confirmation)`,
	Args: func(cmd *cobra.Command, args []string) error {
		if resetAll {
			if len(args) > 1 {
				return fmt.Errorf("--all flag accepts at most 1 argument (target branch)")
			}
			return nil
		}
		if len(args) < 1 {
			return fmt.Errorf("requires worktree argument (or use --all)")
		}
		return nil
	},
	Run: runReset,
}

func init() {
	resetCmd.Flags().BoolVarP(&resetAll, "all", "a", false, "Reset all worktrees")
	resetCmd.Flags().BoolVarP(&resetForce, "force", "f", false, "Skip confirmation prompt")
	rootCmd.AddCommand(resetCmd)
}

func runReset(cmd *cobra.Command, args []string) {
	defaultBranch := GetDefaultBranch()

	if resetAll {
		// Reset all worktrees
		targetBranch := defaultBranch
		if len(args) > 0 {
			targetBranch = args[0]
		}
		resetAllWorktrees(targetBranch)
	} else {
		// Single worktree reset
		worktreeName := args[0]
		targetBranch := defaultBranch
		if len(args) > 1 {
			targetBranch = args[1]
		}
		resetWorktree(worktreeName, targetBranch, !resetForce)
	}
}

func resetAllWorktrees(targetBranch string) {
	fmt.Println("=========================================")
	fmt.Printf("Resetting ALL worktrees -> %s\n", targetBranch)
	fmt.Println("=========================================")
	fmt.Println("")
	fmt.Println("⚠ WARNING: This will discard ALL local changes in ALL worktrees!")
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

	// List what will be reset
	for _, wt := range worktrees {
		fmt.Printf("  - %s (%s)\n", wt.Name, wt.Branch)
	}
	fmt.Println("")

	// Confirm unless --force
	if !resetForce {
		if !confirmAction("Are you sure?") {
			fmt.Println("Aborted.")
			return
		}
		fmt.Println("")
	}

	// Reset each worktree
	for _, wt := range worktrees {
		resetWorktree(wt.Name, targetBranch, false) // Don't ask for each one
		fmt.Println("")
	}

	fmt.Println("=========================================")
	fmt.Printf("All worktrees reset to %s!\n", targetBranch)
	fmt.Println("=========================================")
}

func resetWorktree(worktreeName, targetBranch string, askConfirm bool) {
	worktreePath, err := ResolveWorktreePath(worktreeName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return
	}

	fmt.Println("=========================================")
	fmt.Printf("Resetting: %s -> %s\n", worktreeName, targetBranch)
	fmt.Println("=========================================")

	// Confirm if needed
	if askConfirm {
		fmt.Println("")
		fmt.Printf("⚠ WARNING: This will discard ALL local changes in '%s'!\n", worktreeName)
		if !confirmAction("Are you sure?") {
			fmt.Println("Aborted.")
			return
		}
		fmt.Println("")
	}

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

	// Discard local changes
	fmt.Println("Discarding local changes...")
	if err := GitReset(worktreePath, "HEAD"); err != nil {
		fmt.Fprintf(os.Stderr, "Error resetting: %v\n", err)
		return
	}
	if err := GitClean(worktreePath); err != nil {
		fmt.Fprintf(os.Stderr, "Error cleaning: %v\n", err)
		return
	}

	// Reset to target branch
	if err := GitReset(worktreePath, "origin/"+targetBranch); err != nil {
		fmt.Fprintf(os.Stderr, "Error resetting to %s: %v\n", targetBranch, err)
		return
	}

	// Force push
	if err := GitPushForce(worktreePath, currentBranch); err != nil {
		fmt.Fprintf(os.Stderr, "Error force pushing: %v\n", err)
		return
	}

	fmt.Printf("✓ Reset complete: %s is now at origin/%s\n", worktreeName, targetBranch)
	fmt.Printf("  Branch: %s (force pushed)\n", currentBranch)
}

func confirmAction(prompt string) bool {
	reader := bufio.NewReader(os.Stdin)
	fmt.Printf("%s (y/N) ", prompt)
	response, err := reader.ReadString('\n')
	if err != nil {
		return false
	}
	response = strings.TrimSpace(strings.ToLower(response))
	return response == "y" || response == "yes"
}

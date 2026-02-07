package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"

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

In workspace mode, --all resets all repos in the workspace. Each repo
resets to its own configured integration branch (DefaultBranch) unless
a target branch is explicitly provided.

Arguments:
  worktree    Worktree name (e.g., falcon)
  branch      Target branch to reset to (default: integration branch)

Flags:
  -a, --all      Reset all worktrees
  -f, --force    Skip confirmation prompt, override lock protection,
                 and allow force-push to protected branches (main/master)

Safety:
  Force-pushing to main/master is blocked by default.
  Use --force to override this protection.

Examples:
  loom reset falcon                        # Reset falcon to integration branch
  loom reset falcon main                   # Reset falcon to main
  loom reset --all                         # Reset all worktrees to their integration branches
  loom reset --all --force                 # Reset all worktrees (no confirmation)`,
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
	resetCmd.Flags().BoolVarP(&resetForce, "force", "f", false, "Skip confirmation and allow force-push to protected branches (main/master)")
	rootCmd.AddCommand(resetCmd)
}

func runReset(cmd *cobra.Command, args []string) {
	defaultBranch := GetDefaultBranch()

	if resetAll {
		// Reset all worktrees
		targetBranch := defaultBranch
		explicitBranch := len(args) > 0
		if explicitBranch {
			targetBranch = args[0]
		}
		resetAllWorktrees(targetBranch, explicitBranch)
	} else {
		// Single worktree reset
		worktreeName := args[0]
		targetBranch := defaultBranch
		if len(args) > 1 {
			targetBranch = args[1]
		}
		if !resetWorktree(worktreeName, targetBranch, !resetForce) {
			os.Exit(1)
		}
	}
}

func resetAllWorktrees(targetBranch string, explicitTarget bool) {
	worktrees, err := DiscoverWorktrees()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error discovering worktrees: %v\n", err)
		os.Exit(1)
	}

	if len(worktrees) == 0 {
		fmt.Println("No worktrees found.")
		return
	}

	// Determine per-worktree target branches.
	// In workspace mode, each repo may have its own DefaultBranch.
	// An explicit targetBranch argument overrides per-repo defaults.
	type resetTarget struct {
		wt     WorktreeInfo
		branch string
	}
	var targets []resetTarget
	perRepoBranches := false
	for _, wt := range worktrees {
		branch := targetBranch
		if !explicitTarget && wt.Repo != nil && wt.Repo.DefaultBranch != "" {
			branch = wt.Repo.DefaultBranch
			if branch != targetBranch {
				perRepoBranches = true
			}
		}
		targets = append(targets, resetTarget{wt: wt, branch: branch})
	}

	fmt.Println("=========================================")
	if perRepoBranches {
		fmt.Println("Resetting ALL worktrees -> per-repo integration branches")
	} else {
		fmt.Printf("Resetting ALL worktrees -> %s\n", targetBranch)
	}
	fmt.Println("=========================================")
	fmt.Println("")
	fmt.Println("⚠ WARNING: This will discard ALL local changes in ALL worktrees!")
	fmt.Println("")

	// List what will be reset
	for _, t := range targets {
		fmt.Printf("  - %s (%s) -> %s\n", t.wt.Name, t.wt.Branch, t.branch)
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
	var failed []string
	for _, t := range targets {
		if !resetWorktree(t.wt.Name, t.branch, false) {
			failed = append(failed, t.wt.Name)
		}
		fmt.Println("")
	}

	fmt.Println("=========================================")
	if len(failed) > 0 {
		fmt.Fprintf(os.Stderr, "Failed to reset %d worktree(s): %v\n", len(failed), failed)
		fmt.Println("=========================================")
		os.Exit(1)
	}
	if perRepoBranches {
		fmt.Println("All worktrees reset to their integration branches!")
	} else {
		fmt.Printf("All worktrees reset to %s!\n", targetBranch)
	}
	fmt.Println("=========================================")
}

func resetWorktree(worktreeName, targetBranch string, askConfirm bool) bool {
	worktreePath, err := ResolveWorktreePath(worktreeName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return false
	}

	// Check for active agent lock before destructive operation
	lockInfo, running, checkErr := CheckLock(worktreePath)
	if checkErr != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not check agent lock: %v\n", checkErr)
	} else if running {
		duration := time.Since(lockInfo.StartedAt).Round(time.Second)
		taskInfo := ""
		if lockInfo.TaskID != "" {
			taskInfo = fmt.Sprintf(" on task %s", lockInfo.TaskID)
		}
		if !resetForce {
			fmt.Fprintf(os.Stderr, "Error: Agent '%s' (PID %d) is actively working%s in worktree '%s' (running %s)\n",
				lockInfo.AgentName, lockInfo.PID, taskInfo, worktreeName, duration)
			fmt.Fprintf(os.Stderr, "Use --force to reset anyway (will destroy uncommitted work)\n")
			return false
		}
		fmt.Fprintf(os.Stderr, "Warning: Agent '%s' (PID %d) is actively working%s in worktree '%s' (running %s)\n",
			lockInfo.AgentName, lockInfo.PID, taskInfo, worktreeName, duration)
		fmt.Fprintf(os.Stderr, "Proceeding with --force...\n")
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
			return true // User abort is not an error
		}
		fmt.Println("")
	}

	// Get current branch
	currentBranch, err := GetCurrentBranch(worktreePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting current branch: %v\n", err)
		return false
	}

	// Fetch latest
	if err := GitFetch(worktreePath); err != nil {
		fmt.Fprintf(os.Stderr, "Error fetching: %v\n", err)
		return false
	}

	// Discard local changes
	fmt.Println("Discarding local changes...")
	if err := GitReset(worktreePath, "HEAD"); err != nil {
		fmt.Fprintf(os.Stderr, "Error resetting: %v\n", err)
		return false
	}
	if err := GitClean(worktreePath); err != nil {
		fmt.Fprintf(os.Stderr, "Error cleaning: %v\n", err)
		return false
	}

	// Reset to target branch
	if err := GitReset(worktreePath, "origin/"+targetBranch); err != nil {
		fmt.Fprintf(os.Stderr, "Error resetting to %s: %v\n", targetBranch, err)
		return false
	}

	// Refuse to force-push protected branches unless --force was used
	if isProtectedBranch(currentBranch) && !resetForce {
		fmt.Fprintf(os.Stderr, "Error: refusing to force-push to protected branch '%s'.\n", currentBranch)
		fmt.Fprintf(os.Stderr, "Use --force to override this protection.\n")
		return false
	}
	if isProtectedBranch(currentBranch) && resetForce {
		fmt.Fprintf(os.Stderr, "Warning: force-pushing to protected branch '%s'!\n", currentBranch)
	}

	// Force push
	if err := GitPushForce(worktreePath, currentBranch); err != nil {
		fmt.Fprintf(os.Stderr, "Error force pushing: %v\n", err)
		return false
	}

	fmt.Printf("✓ Reset complete: %s is now at origin/%s\n", worktreeName, targetBranch)
	fmt.Printf("  Branch: %s (force pushed)\n", currentBranch)
	return true
}

// isProtectedBranch returns true if the branch is main or master.
func isProtectedBranch(branch string) bool {
	return branch == "main" || branch == "master"
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

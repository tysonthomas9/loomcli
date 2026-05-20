package git

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli"
)

var (
	resetAll   bool
	resetForce bool
	resetPush  bool
)

var resetCmd = &cobra.Command{
	Use:               "reset <worktree> [branch]",
	Short:             "Hard reset worktree to a specific branch",
	GroupID:           "git",
	ValidArgsFunction: cli.WorktreeThenBranchCompletion,
	Long: `Hard reset worktree(s) to a specific branch.

WARNING: This discards ALL local changes!

This command will:
  1. Discard all local changes (git reset --hard, git clean -fd)
  2. Reset to the target branch (origin/branch)
  3. Force push only if --push is specified (local-only by default)

In workspace mode, --all resets all repos in the workspace. Each repo
resets to its own configured integration branch (DefaultBranch) unless
a target branch is explicitly provided.

Arguments:
  worktree    Worktree name (e.g., falcon)
  branch      Target branch to reset to (default: integration branch)

Flags:
  -a, --all      Reset all worktrees
  -p, --push     Force-push to remote after resetting locally
  -f, --force    Skip confirmation prompt, override lock protection,
                 and allow force-push to protected branches (main/master)

Safety:
  By default, only local state is reset. The remote branch is NOT updated.
  Use --push to force-push the reset to origin.
  Force-pushing to main/master requires both --push and --force.

Examples:
  loom reset falcon                        # Reset falcon locally (no push)
  loom reset falcon main                   # Reset falcon to main (local only)
  loom reset falcon --push                 # Reset falcon and force-push to origin
  loom reset --all                         # Reset all worktrees locally
  loom reset --all --push                  # Reset all worktrees and force-push`,
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
	resetCmd.Flags().BoolVarP(&resetPush, "push", "p", false, "Force-push to remote after resetting locally")
	resetCmd.Flags().BoolVarP(&resetForce, "force", "f", false, "Skip confirmation and allow force-push to protected branches (main/master)")
	cli.RegisterCommand(resetCmd)
}

func runReset(cmd *cobra.Command, args []string) {
	deps := cli.GetDeps(cmd)
	defaultBranch := cli.GetDefaultBranch()

	if resetAll {
		// Reset all worktrees
		targetBranch := defaultBranch
		explicitBranch := len(args) > 0
		if explicitBranch {
			targetBranch = args[0]
		}
		if err := resetAllWorktrees(deps, targetBranch, explicitBranch); err != nil {
			exitProcess(1)
		}
	} else {
		// Single worktree reset
		worktreeName := args[0]
		targetBranch := defaultBranch
		if len(args) > 1 {
			targetBranch = args[1]
		}
		if !resetWorktree(deps, worktreeName, targetBranch, !resetForce) {
			exitProcess(1)
		}
	}
}

// resetTarget pairs a worktree with its reset target branch.
type resetTarget struct {
	wt     cli.WorktreeInfo
	branch string
}

func resetAllWorktrees(deps *cli.Deps, targetBranch string, explicitTarget bool) error {
	worktrees, err := cli.DiscoverWorktrees()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error discovering worktrees: %v\n", err)
		return fmt.Errorf("error discovering worktrees: %w", err)
	}
	if len(worktrees) == 0 {
		fmt.Println("No worktrees found.")
		return nil
	}

	targets, perRepoBranches := buildResetTargets(worktrees, targetBranch, explicitTarget)
	printResetPlan(targets, targetBranch, perRepoBranches)

	if !resetForce {
		if !ConfirmAction("Are you sure?") {
			fmt.Println("Aborted.")
			return nil
		}
		fmt.Println("")
	}

	failed := executeResetAll(deps, targets)
	return printResetSummary(failed, targetBranch, perRepoBranches)
}

// buildResetTargets resolves each worktree's target branch.
func buildResetTargets(worktrees []cli.WorktreeInfo, targetBranch string, explicitTarget bool) ([]resetTarget, bool) {
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
	return targets, perRepoBranches
}

func printResetPlan(targets []resetTarget, targetBranch string, perRepoBranches bool) {
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
	for _, t := range targets {
		fmt.Printf("  - %s (%s) -> %s\n", t.wt.Name, t.wt.Branch, t.branch)
	}
	fmt.Println("")
}

func executeResetAll(deps *cli.Deps, targets []resetTarget) []string {
	var failed []string
	for _, t := range targets {
		if !resetWorktree(deps, t.wt.Name, t.branch, false) {
			failed = append(failed, t.wt.Name)
		}
		fmt.Println("")
	}
	return failed
}

func printResetSummary(failed []string, targetBranch string, perRepoBranches bool) error {
	fmt.Println("=========================================")
	if len(failed) > 0 {
		fmt.Fprintf(os.Stderr, "Failed to reset %d worktree(s): %v\n", len(failed), failed)
		fmt.Println("=========================================")
		return fmt.Errorf("failed to reset %d worktree(s): %v", len(failed), failed)
	}
	if perRepoBranches {
		fmt.Println("All worktrees reset to their integration branches!")
	} else {
		fmt.Printf("All worktrees reset to %s!\n", targetBranch)
	}
	fmt.Println("=========================================")
	return nil
}

func resetWorktree(deps *cli.Deps, worktreeName, targetBranch string, askConfirm bool) bool {
	worktreePath, err := cli.ResolveWorktreePath(worktreeName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return false
	}

	if !checkResetLock(worktreePath, worktreeName) {
		return false
	}

	fmt.Println("=========================================")
	fmt.Printf("Resetting: %s -> %s\n", worktreeName, targetBranch)
	fmt.Println("=========================================")

	if askConfirm {
		fmt.Println("")
		fmt.Printf("⚠ WARNING: This will discard ALL local changes in '%s'!\n", worktreeName)
		if !ConfirmAction("Are you sure?") {
			fmt.Println("Aborted.")
			return true
		}
		fmt.Println("")
	}

	currentBranch, err := getCurrentBranchViaDeps(deps, worktreePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting current branch: %v\n", err)
		return false
	}

	if err := executeReset(deps, worktreePath, targetBranch); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return false
	}

	return resetFinalizePush(deps, worktreePath, worktreeName, targetBranch, currentBranch)
}

// checkResetLock checks for an active agent lock and returns false if the reset should be blocked.
func checkResetLock(worktreePath, worktreeName string) bool {
	lockInfo, running, checkErr := cli.CheckLock(worktreePath)
	if checkErr != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not check agent lock: %v\n", checkErr)
		return true
	}
	if !running {
		return true
	}

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
	return true
}

// executeReset performs the fetch, discard, and reset-to-target steps.
func executeReset(deps *cli.Deps, worktreePath, targetBranch string) error {
	if err := gitFetch(deps, worktreePath); err != nil {
		return fmt.Errorf("fetching: %v", err)
	}

	fmt.Println("Discarding local changes...")
	if err := gitReset(deps, worktreePath, "HEAD"); err != nil {
		return fmt.Errorf("resetting: %v", err)
	}
	if err := gitClean(deps, worktreePath); err != nil {
		return fmt.Errorf("cleaning: %v", err)
	}

	if err := gitReset(deps, worktreePath, "origin/"+targetBranch); err != nil {
		return fmt.Errorf("resetting to %s: %v", targetBranch, err)
	}
	return nil
}

// resetFinalizePush handles the optional force-push after a reset.
func resetFinalizePush(deps *cli.Deps, worktreePath, worktreeName, targetBranch, currentBranch string) bool {
	if !resetPush {
		fmt.Printf("✓ Reset complete: %s is now at origin/%s\n", worktreeName, targetBranch)
		fmt.Printf("  Remote branch not updated. Use --push to force-push to origin.\n")
		return true
	}

	if isProtectedBranch(currentBranch) && !resetForce {
		fmt.Fprintf(os.Stderr, "Error: refusing to force-push to protected branch '%s'.\n", currentBranch)
		fmt.Fprintf(os.Stderr, "Use --force to override this protection.\n")
		return false
	}
	if isProtectedBranch(currentBranch) && resetForce {
		fmt.Fprintf(os.Stderr, "Warning: force-pushing to protected branch '%s'!\n", currentBranch)
	}

	if err := gitPushForce(deps, worktreePath, currentBranch); err != nil {
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

func ConfirmAction(prompt string) bool {
	reader := bufio.NewReader(os.Stdin)
	fmt.Printf("%s (y/N) ", prompt)
	response, err := reader.ReadString('\n')
	if err != nil {
		return false
	}
	response = strings.TrimSpace(strings.ToLower(response))
	return response == "y" || response == "yes"
}

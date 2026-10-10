package git

import (
	"bufio"
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli"
)

var (
	resetAll   bool
	resetForce bool
	resetYes   bool
)

var resetCmd = &cobra.Command{
	Use:               "reset <worktree> [branch]",
	Short:             "Hard reset worktree to a specific branch",
	GroupID:           "git",
	Hidden:            true, // plumbing for tests and repair (S3)
	ValidArgsFunction: cli.WorktreeThenBranchCompletion,
	Long: `Hard reset worktree(s) to a specific branch.

WARNING: This discards local changes after a complete capture.

This command will:
  1. Check branch protection and the running agent
  2. Capture local changes in a workspace WIP ref
  3. Reset to the target branch (origin/branch)

In workspace mode, --all resets all repos in the workspace. Each repo
resets to its own configured integration branch (DefaultBranch) unless
a target branch is explicitly provided.

Arguments:
  worktree    Worktree name (e.g., falcon)
  branch      Target branch to reset to (default: integration branch)

Flags:
  -a, --all      Reset all worktrees
  -f, --force    Stop a running agent after confirmation
  -y, --yes      Confirm every worktree in non-interactive mode

Safety:
  Protected branches cannot be reset. The remote branch is never updated.

Examples:
  loom reset falcon                        # Capture and reset falcon locally
  loom reset falcon main                   # Reset falcon to main locally
  loom reset --all                         # Reset all worktrees locally
`,
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
	resetCmd.Flags().BoolVarP(&resetForce, "force", "f", false, "Stop running agent after confirmation")
	resetCmd.Flags().BoolVarP(&resetYes, "yes", "y", false, "Confirm every worktree without prompting")
	cli.RegisterCommand(resetCmd)
}

func runReset(_ *cobra.Command, args []string) {
	defaultBranch := cli.GetDefaultBranch()

	if resetAll {
		// Reset all worktrees
		targetBranch := defaultBranch
		explicitBranch := len(args) > 0
		if explicitBranch {
			targetBranch = args[0]
		}
		if err := resetAllWorktrees(targetBranch, explicitBranch); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	} else {
		// Single worktree reset
		worktreeName := args[0]
		targetBranch := defaultBranch
		if len(args) > 1 {
			targetBranch = args[1]
		}
		if !resetWorktree(worktreeName, targetBranch, true) {
			os.Exit(1)
		}
	}
}

// resetTarget pairs a worktree with its reset target branch.
type resetTarget struct {
	wt     cli.WorktreeInfo
	branch string
}

func resetAllWorktrees(targetBranch string, explicitTarget bool) error {
	return resetAllWorktreesWithConfirmation(targetBranch, explicitTarget, newConfirmationSession(resetYes))
}

func resetAllWorktreesWithConfirmation(targetBranch string, explicitTarget bool, confirmation *confirmationSession) error {
	worktrees, err := cli.DiscoverWorktrees()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error discovering worktrees: %v\n", err)
		return fmt.Errorf("error discovering worktrees: %w", err)
	}
	if len(worktrees) == 0 {
		fmt.Println("No worktrees found.")
		return nil
	}
	names := make([]string, 0, len(worktrees))
	for _, wt := range worktrees {
		names = append(names, wt.Name)
	}
	if err := confirmation.requireInteractive("loom reset --all", names); err != nil {
		return err
	}

	targets, perRepoBranches := buildResetTargets(worktrees, targetBranch, explicitTarget)
	printResetPlan(targets, targetBranch, perRepoBranches)

	failed, skipped, err := executeResetAll(targets, confirmation)
	if err != nil {
		return err
	}
	return printResetSummary(failed, skipped, targetBranch, perRepoBranches)
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
	fmt.Println("⚠ WARNING: This will discard local changes in ALL worktrees after capture!")
	fmt.Println("")
	for _, t := range targets {
		fmt.Printf("  - %s (%s) -> %s\n", t.wt.Name, t.wt.Branch, t.branch)
	}
	fmt.Println("")
}

func executeResetAll(targets []resetTarget, confirmation *confirmationSession) ([]string, []string, error) {
	var failed []string
	var skipped []string
	for _, t := range targets {
		fmt.Printf("Workspace %s -> %s\n", t.wt.Name, t.branch)
		if err := printUnsavedWork(cli.GetDeps(nil), []cli.WorktreeInfo{t.wt}, t.branch); err != nil {
			return failed, skipped, err
		}
		if err := printResetIgnored(t.wt.Path); err != nil {
			return failed, skipped, err
		}
		if !confirmation.confirm("Reset " + t.wt.Name + " after capture?") {
			fmt.Printf("Skipped %s.\n\n", t.wt.Name)
			skipped = append(skipped, t.wt.Name)
			continue
		}
		if !resetWorktree(t.wt.Name, t.branch, false) {
			failed = append(failed, t.wt.Name)
		}
		fmt.Println("")
	}
	return failed, skipped, nil
}

func printResetSummary(failed, skipped []string, targetBranch string, perRepoBranches bool) error {
	fmt.Println("=========================================")
	if len(failed) > 0 {
		fmt.Fprintf(os.Stderr, "Failed to reset %d worktree(s): %v\n", len(failed), failed)
		fmt.Println("=========================================")
		return fmt.Errorf("failed to reset %d worktree(s): %v", len(failed), failed)
	}
	if len(skipped) > 0 {
		fmt.Printf("Skipped %d worktree(s): %v\n", len(skipped), skipped)
		return nil
	}
	if perRepoBranches {
		fmt.Println("All worktrees reset to their integration branches!")
	} else {
		fmt.Printf("All worktrees reset to %s!\n", targetBranch)
	}
	fmt.Println("=========================================")
	return nil
}

func resetWorktree(worktreeName, targetBranch string, askConfirm bool) bool {
	worktreePath, err := cli.ResolveWorktreePath(worktreeName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return false
	}
	branch, err := cli.GetCurrentBranch(worktreePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting branch: %v\n", err)
		return false
	}
	if isProtectedBranch(branch) || branch == targetBranch {
		fmt.Fprintf(os.Stderr, "Error: protected branch %s\n", branch)
		return false
	}

	fmt.Println("=========================================")
	fmt.Printf("Resetting: %s -> %s\n", worktreeName, targetBranch)
	fmt.Println("=========================================")

	if askConfirm {
		fmt.Println("")
		fmt.Printf("⚠ WARNING: This will discard local changes in '%s' after capture!\n", worktreeName)
		if err := printResetIgnored(worktreePath); err != nil {
			fmt.Fprintf(os.Stderr, "Error listing ignored files: %v\n", err)
			return false
		}
		if !ConfirmAction("Are you sure?") {
			fmt.Println("Aborted.")
			return true
		}
		fmt.Println("")
	}

	result, err := ResetWorktreeResult(worktreePath, worktreeName, targetBranch, resetForce, false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return false
	}
	fmt.Printf("✓ %s\n", result.Message)
	if result.CaptureRef != "" {
		fmt.Printf("  Capture: %s\n", result.CaptureRef)
	}
	return true
}

func printResetIgnored(path string) error {
	entries, err := ListResetIgnored(context.Background(), path)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return nil
	}
	fmt.Println("Ignored paths that will be removed:")
	for _, entry := range entries {
		fmt.Printf("  %s (%d bytes)\n", entry.Path, entry.Size)
	}
	return nil
}

// isProtectedBranch recognizes the conventional trunks used by Loom.
func isProtectedBranch(branch string) bool {
	return branch == "main" || branch == "master" || branch == "v5"
}

func ConfirmAction(prompt string) bool {
	return confirmFrom(bufio.NewReader(os.Stdin), os.Stdout, prompt)
}

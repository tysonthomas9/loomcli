package git

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/loomgit/agentcapture"
)

var (
	resetAll   bool
	resetForce bool
)

var resetCmd = &cobra.Command{
	Use:               "reset <worktree> [branch]",
	Short:             "Hard reset worktree to a specific branch",
	GroupID:           "git",
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

	for _, target := range targets {
		fmt.Printf("%s:\n", target.wt.Name)
		if err := printResetIgnored(target.wt.Path); err != nil {
			return err
		}
	}
	if !ConfirmAction("Are you sure?") {
		fmt.Println("Aborted.")
		return nil
	}
	fmt.Println("")

	failed := executeResetAll(targets)
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
	fmt.Println("⚠ WARNING: This will discard local changes in ALL worktrees after capture!")
	fmt.Println("")
	for _, t := range targets {
		fmt.Printf("  - %s (%s) -> %s\n", t.wt.Name, t.wt.Branch, t.branch)
	}
	fmt.Println("")
}

func executeResetAll(targets []resetTarget) []string {
	var failed []string
	for _, t := range targets {
		if !resetWorktree(t.wt.Name, t.branch, false) {
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
	entries, err := agentcapture.ListIgnored(context.Background(), path)
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
	reader := bufio.NewReader(os.Stdin)
	fmt.Printf("%s (y/N) ", prompt)
	response, err := reader.ReadString('\n')
	if err != nil {
		return false
	}
	response = strings.TrimSpace(strings.ToLower(response))
	return response == "y" || response == "yes"
}

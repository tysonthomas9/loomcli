package agent

import (
	"fmt"
	"log"
	"os"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
)

var claimCmd = &cobra.Command{
	Use:   "claim <task-id>",
	Short: "Update the lock file with the task being worked on",
	Long: `Update the agent lock file to record which task is being worked on.

This command is called after an agent claims a task through Loom. It updates
the lock file so that 'loom monitor' can show which task each agent is working on.

Arguments:
  task-id    The Loom task ID (e.g., loomcli-487 or loomcli-abc.1)

Examples:
  loom claim loomcli-487         # Record that we're working on loomcli-487`,
	Args: cobra.ExactArgs(1),
	Run:  runClaim,
}

func init() {
	cli.RegisterCommand(claimCmd)
}

func runClaim(cmd *cobra.Command, args []string) {
	taskID := args[0]

	// Get the current working directory (should be in a worktree)
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting current directory: %v\n", err)
		cli.ExitWithFlush(1)
	}

	// Resolve the task title through the active issue backend.
	taskTitle := getTaskTitle(taskID)

	// Update the lock file
	if err := cli.UpdateLockTask(cwd, taskID, taskTitle); err != nil {
		fmt.Fprintf(os.Stderr, "Error updating lock: %v\n", err)
		cli.ExitWithFlush(1)
	}

	// Emit task_claimed event (best-effort)
	emitTaskClaimedEvent(taskID, taskTitle)

	// Clear stale checkpoint if it's for a different task
	lockDir := cli.ResolveLockDir(cwd)
	if cp, err := config.LoadCheckpoint(lockDir); err == nil && cp != nil && cp.TaskID != taskID {
		log.Printf("[claim] Clearing stale checkpoint (was for task %s, now claiming %s)", cp.TaskID, taskID)
		_ = config.ClearCheckpoint(lockDir)
	}

	fmt.Printf("Claimed task: %s\n", taskID)
	if taskTitle != "" {
		fmt.Printf("Title: %s\n", taskTitle)
	}
}

func getTaskTitle(taskID string) string {
	ctx, cancel := cmdstore.SignalContext()
	defer cancel()
	detail, err := cli.DefaultIssueBackend().Get(ctx, taskID)
	if err != nil || detail == nil {
		return ""
	}
	return detail.Title
}

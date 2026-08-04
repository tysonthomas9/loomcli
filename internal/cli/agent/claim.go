package agent

import (
	"fmt"
	"log"
	"os"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/events"
)

var claimCmd = &cobra.Command{
	Use:   "claim <task-id>",
	Short: "Update the lock file with the task being worked on",
	Long: `Update the agent lock file to record which task is being worked on.

This command is called after an agent claims a task through Loom. It updates
the lock file so that 'loom monitor' can show which task each agent is working on.
The lock-file update is bookkeeping for 'loom monitor'; failures do not affect
the actual task claim.

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

	// Update the lock file. Best-effort: the lock file is bookkeeping for
	// 'loom monitor' only — the actual task claim is owned by the daemon/
	// server, so a failure here must not read as a failed claim (and exiting
	// non-zero made harness sessions treat exactly that way).
	if err := cli.UpdateLockTask(cwd, taskID, taskTitle); err != nil {
		fmt.Fprintf(os.Stderr, "[claim] note: could not record the task in the agent lock file (%v) - "+
			"this is monitor bookkeeping only; the task claim itself is owned by the daemon/server and is unaffected\n", err)
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

// emitTaskClaimedEvent emits a task_claimed event via the process-wide
// AgentEventBus singleton. The singleton is subscribed to otelexport, so this
// emission produces a loom.task span under the active trace context.
//
// LOOM_EVENTS_DIR is honored by the singleton's lazy init in
// cli.initAgentEventBus, so the previous fallback behavior is preserved.
// When the bus is unavailable (mkdir failure on first use) emission is
// skipped silently — same best-effort contract as before.
func emitTaskClaimedEvent(taskID, taskTitle string) {
	bus := cli.AgentEventBus()
	if bus == nil {
		return
	}
	agentName := os.Getenv("LOOM_AGENT_NAME")
	evt, err := events.NewEvent(events.TaskClaimed, agentName, "", "", events.TaskClaimedData{TaskID: taskID, Title: taskTitle})
	if err != nil {
		return
	}
	if emitErr := bus.Emit(evt); emitErr != nil {
		log.Printf("[claim] Failed to emit task_claimed event: %v", emitErr)
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

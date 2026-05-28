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

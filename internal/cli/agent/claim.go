package agent

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/events"
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
		os.Exit(1)
	}

	// Resolve the task title through the active issue backend.
	taskTitle := getTaskTitle(taskID)

	// Update the lock file
	if err := cli.UpdateLockTask(cwd, taskID, taskTitle); err != nil {
		fmt.Fprintf(os.Stderr, "Error updating lock: %v\n", err)
		os.Exit(1)
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

// emitTaskClaimedEvent creates a temporary event bus and emits a task_claimed event.
// Uses LOOM_EVENTS_DIR env var, falling back to .loom/events relative to git toplevel.
func emitTaskClaimedEvent(taskID, taskTitle string) {
	eventsDir := os.Getenv("LOOM_EVENTS_DIR")
	if eventsDir == "" {
		// Fall back to .loom/events relative to git toplevel
		cmd := cli.GetDeps(nil).Exec.Run("", "git", "rev-parse", "--show-toplevel")
		if cmd.Err != nil {
			return
		}
		toplevel := strings.TrimSpace(cmd.Stdout)
		if toplevel == "" {
			return
		}
		eventsDir = filepath.Join(toplevel, ".loom", "events")
	}

	bus := events.NewBus(eventsDir)
	defer func() { _ = bus.Close() }()

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
	detail, err := cli.DefaultIssueBackend().Get(context.Background(), taskID)
	if err != nil || detail == nil {
		return ""
	}
	return detail.Title
}

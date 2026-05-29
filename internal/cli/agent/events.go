package agent

import (
	"os"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli/agentevents"
)

// emitTaskClaimedFromEnv emits a TaskClaimed event before InvokeInteractive
// so a loom.task span starts under the active trace. Used by single-task and
// daemon-mode plan/task paths that bypass the auto-mode loop.
func emitTaskClaimedFromEnv(agentName, taskID string) {
	agentevents.EmitClaimed(agentName, taskID, "", "agent")
}

func emitTaskClaimedEvent(taskID, taskTitle string) {
	agentevents.EmitClaimed(os.Getenv("LOOM_AGENT_NAME"), taskID, taskTitle, "claim")
}

func emitTaskLifecycleResult(agentName, worktreePath string, startedAt time.Time, invokeErr error) {
	agentevents.EmitLifecycleResult(agentName, worktreePath, startedAt, invokeErr)
}

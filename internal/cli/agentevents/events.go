package agentevents

import (
	"errors"
	"log/slog"
	"os/exec"
	"time"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/events"
)

func EmitClaimed(agentName, taskID, taskTitle, logPrefix string) {
	bus := cli.AgentEventBus()
	if bus == nil {
		return
	}
	evt, err := events.NewEvent(events.TaskClaimed, agentName, "", "", events.TaskClaimedData{
		TaskID: taskID,
		Title:  taskTitle,
	})
	if err != nil {
		return
	}
	if emitErr := bus.Emit(evt); emitErr != nil {
		slog.Warn("failed to emit task_claimed event", "source", logPrefix, "error", emitErr)
	}
}

func EmitLifecycleResult(agentName, worktreePath string, startedAt time.Time, invokeErr error) {
	bus := cli.AgentEventBus()
	if bus == nil {
		return
	}
	taskID := ""
	if info, lockErr := cli.ReadLockFile(worktreePath); lockErr == nil && info != nil {
		taskID = info.TaskID
	}
	if invokeErr == nil {
		emitCompleted(bus, agentName, taskID, time.Since(startedAt))
		return
	}
	emitFailed(bus, agentName, taskID, invokeErr)
}

type eventBus interface {
	Emit(events.Event) error
}

func emitCompleted(bus eventBus, agentName, taskID string, duration time.Duration) {
	evt, err := events.NewEvent(events.TaskCompleted, agentName, "", "", events.TaskCompletedData{
		TaskID:   taskID,
		Duration: events.Duration{Duration: duration},
	})
	if err != nil {
		return
	}
	if emitErr := bus.Emit(evt); emitErr != nil {
		slog.Warn("failed to emit task_completed event", "source", "agent", "error", emitErr)
	}
}

func emitFailed(bus eventBus, agentName, taskID string, invokeErr error) {
	exitCode := 1
	var exitErr *exec.ExitError
	if errors.As(invokeErr, &exitErr) {
		exitCode = exitErr.ExitCode()
	}
	ae := agenterr.ClassifyFromOutput(invokeErr.Error(), exitCode, cli.ResolveBackendName())
	evtData := events.TaskFailedData{
		TaskID:     taskID,
		Error:      invokeErr.Error(),
		ErrorClass: ae.Class.String(),
	}
	if ae.RetryAfter > 0 {
		evtData.RetryAfter = ae.RetryAfter.String()
	}
	evt, err := events.NewEvent(events.TaskFailed, agentName, "", "", evtData)
	if err != nil {
		return
	}
	if emitErr := bus.Emit(evt); emitErr != nil {
		slog.Warn("failed to emit task_failed event", "source", "agent", "error", emitErr)
	}
}

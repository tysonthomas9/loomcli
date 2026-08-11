package testutil

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/modules/execution"
)

// StaticTaskRunRecoveryPort satisfies unrelated composition tests without
// introducing a second persistence implementation. Tests that exercise
// recovery itself provide a behavior-specific fake instead.
type StaticTaskRunRecoveryPort struct {
	Result execution.RecoverStaleTaskRunsResult
	Err    error
}

func (port StaticTaskRunRecoveryPort) RecoverStaleChildTaskRuns(
	_ context.Context,
	command execution.RecoverStaleChildTaskRunsCommand,
) (execution.RecoverStaleTaskRunsResult, error) {
	if port.Err == nil && port.Result.WorkspaceKey == "" {
		return execution.RecoverStaleTaskRunsResult{
			WorkspaceKey: command.WorkspaceKey, StaleBefore: command.StaleBefore,
			RecoveredAt: command.ObservedAt, RecoveredTaskRunIDs: []string{},
		}, nil
	}
	return port.Result, port.Err
}

var _ execution.TaskRunStaleChildRecoveryPort = StaticTaskRunRecoveryPort{}

package execution

import (
	"context"
	"testing"
	"time"
)

type taskRunRecoveryPortStub struct {
	commands []RecoverStaleChildTaskRunsCommand
}

func (stub *taskRunRecoveryPortStub) RecoverStaleChildTaskRuns(_ context.Context, command RecoverStaleChildTaskRunsCommand) (RecoverStaleTaskRunsResult, error) {
	stub.commands = append(stub.commands, command)
	return RecoverStaleTaskRunsResult{
		WorkspaceKey: command.WorkspaceKey, StaleBefore: command.StaleBefore, RecoveredAt: command.ObservedAt,
		Recovered: 1, Released: 1, SkippedFresh: 2, RecoveredTaskRunIDs: []string{command.WorkspaceKey + "-task-run"},
	}, nil
}

func TestRecoverStaleChildTaskRunsIsBoundToExactParentOwnerAndReplays(t *testing.T) {
	port := &taskRunRecoveryPortStub{}
	service, issuer := newTestService(t, Dependencies{TaskRunRecovery: TaskRunRecoveryDependencies{ChildRecoveries: port}})
	parent := Owner{ResourceKind: ResourceDriverRun, ResourceID: "run-1", NodeID: "node-1", LeaseID: "lease-1", LeaseToken: "driver-token-1", FencingToken: 7}
	now := time.Now().UTC()
	command := RecoverStaleChildTaskRunsCommand{
		WorkspaceKey: "TEST", ParentOwner: parent, DriverRunID: parent.ResourceID,
		StaleBefore: now.Add(-20 * time.Minute), ErrorClass: "stale_task_run",
		ErrorMessage: "task run heartbeat is stale", ObservedAt: now,
	}
	command.RequestID = RecoverStaleChildTaskRunsRequestID(command.DriverRunID, command.StaleBefore)
	for range 2 {
		result, err := service.RecoverStaleChildTaskRuns(context.Background(), issueExecution(t, issuer, ActionRecoverStaleChildTaskRuns, parent), command)
		if err != nil {
			t.Fatalf("RecoverStaleChildTaskRuns: %v", err)
		}
		persisted := port.commands[len(port.commands)-1]
		if result.Recovered != 1 || persisted.DriverRunID != parent.ResourceID || persisted.RequestID != command.RequestID ||
			persisted.ParentOwner.LeaseToken != parent.LeaseToken {
			t.Fatalf("result=%+v commands=%+v", result, port.commands)
		}
	}
	foreign := parent
	foreign.ResourceID = "run-2"
	if _, err := service.RecoverStaleChildTaskRuns(context.Background(), issueExecution(t, issuer, ActionRecoverStaleChildTaskRuns, foreign), command); err != ErrFenceConflict {
		t.Fatalf("foreign parent error=%v, want fence conflict", err)
	}
	stale := parent
	stale.FencingToken--
	if _, err := service.RecoverStaleChildTaskRuns(context.Background(), issueExecution(t, issuer, ActionRecoverStaleChildTaskRuns, stale), command); err != ErrFenceConflict {
		t.Fatalf("stale parent error=%v, want fence conflict", err)
	}
	missingToken := parent
	missingToken.LeaseToken = ""
	missingTokenCommand := command
	missingTokenCommand.ParentOwner = missingToken
	if _, err := service.RecoverStaleChildTaskRuns(context.Background(), issueExecution(t, issuer, ActionRecoverStaleChildTaskRuns, missingToken), missingTokenCommand); err != ErrInvalid {
		t.Fatalf("missing-token parent error=%v, want invalid", err)
	}
}

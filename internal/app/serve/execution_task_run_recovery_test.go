package serve

import (
	"context"
	"testing"
	"time"

	workspaceowner "github.com/tysonthomas9/loomcli/internal/modules/workspace"

	"github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
)

type recoveryFleetTransportStub struct {
	fleetExecutionTransportStub
	command fleetdb.ExecutionDriverRunStaleTaskRecoveryCommand
}

func (stub *recoveryFleetTransportStub) RecoverStaleChildTaskRuns(_ context.Context, command fleetdb.ExecutionDriverRunStaleTaskRecoveryCommand) (*fleetdb.ExecutionDriverRunStaleTaskRecoveryResult, error) {
	stub.command = command
	return &fleetdb.ExecutionDriverRunStaleTaskRecoveryResult{
		WorkspaceKey: command.WorkspaceKey, DriverRunID: command.RunID, StaleBefore: command.StaleBefore,
		RecoveredAt: command.StaleBefore.Add(time.Minute), Recovered: 1, Released: 1,
		RecoveredTaskRunIDs: []string{"task-run-stale"},
	}, nil
}

func TestExecutionTaskRunRecoveryAdapterUsesFleetParentOwnerCommand(t *testing.T) {
	ctx := context.Background()
	state := memstore.New()
	if _, err := state.Workspaces().Create(ctx, workspaceowner.WorkspaceCreate{Key: "WS", Name: "workspace"}); err != nil {
		t.Fatal(err)
	}
	transport := &recoveryFleetTransportStub{}
	dependencies, err := NewExecutionTaskRunRecoveryDependencies(ExecutionTaskRunRecoveryDependencies{
		Workspaces: state.Workspaces(), ChildRecoveries: &executionTaskRunFleetRecoveryAdapter{transport: transport},
	})
	if err != nil {
		t.Fatal(err)
	}
	workspaces, err := dependencies.Scopes.ListTaskRunRecoveryWorkspaces(ctx)
	if err != nil || len(workspaces) != 1 || workspaces[0] != "WS" {
		t.Fatalf("workspaces=%v err=%v", workspaces, err)
	}
	now := time.Now().UTC()
	owner := execution.Owner{
		ResourceKind: execution.ResourceDriverRun, ResourceID: "run-1", NodeID: "node-1",
		LeaseID: "lease-1", LeaseToken: "raw-parent-token", FencingToken: 7,
	}
	result, err := dependencies.ChildRecoveries.RecoverStaleChildTaskRuns(ctx, execution.RecoverStaleChildTaskRunsCommand{
		WorkspaceKey: "WS", RequestID: "recover-1", DriverRunID: owner.ResourceID, ParentOwner: owner,
		StaleBefore: now.Add(-time.Hour), ErrorClass: "stale_task_run", ErrorMessage: "stale", ObservedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Recovered != 1 || transport.command.LeaseToken != owner.LeaseToken ||
		transport.command.RunID != owner.ResourceID || transport.command.FencingToken != owner.FencingToken {
		t.Fatalf("result=%+v command=%+v", result, transport.command)
	}
}

func TestExecutionTaskRunRecoveryRequiresOwnerFencedPort(t *testing.T) {
	state := memstore.New()
	_, err := NewExecutionTaskRunRecoveryDependencies(ExecutionTaskRunRecoveryDependencies{
		Workspaces: state.Workspaces(),
	})
	if err == nil {
		t.Fatal("recovery composition accepted a missing owner-fenced port")
	}
}

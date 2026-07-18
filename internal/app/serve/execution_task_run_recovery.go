package serve

import (
	"context"
	"fmt"
	"sort"

	"github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// ExecutionTaskRunRecoveryDependencies keeps workspace enumeration read-only
// and binds stale-child mutation to FleetDB's parent-owner-fenced command.
// LegacyDriverRuns is accepted only by explicitly opted-in test composition.
type ExecutionTaskRunRecoveryDependencies struct {
	Workspaces               store.WorkspaceStore
	Transport                fleetdb.ExecutionTransport
	LegacyDriverRuns         store.DriverRunStore
	AllowLegacyStoreAdapters bool
}

func NewExecutionTaskRunRecoveryDependencies(dependencies ExecutionTaskRunRecoveryDependencies) (execution.TaskRunRecoveryDependencies, error) {
	if dependencies.Workspaces == nil {
		return execution.TaskRunRecoveryDependencies{}, fmt.Errorf("compose stale child TaskRun recovery: workspace scope is required")
	}
	var recoveries execution.TaskRunStaleChildRecoveryPort
	if dependencies.Transport != nil {
		recoveries = &executionTaskRunFleetRecoveryAdapter{transport: dependencies.Transport}
	} else if dependencies.AllowLegacyStoreAdapters && dependencies.LegacyDriverRuns != nil {
		recoveries = &executionTaskRunLegacyRecoveryAdapter{driverRuns: dependencies.LegacyDriverRuns}
	} else {
		return execution.TaskRunRecoveryDependencies{}, fmt.Errorf("compose stale child TaskRun recovery: Fleet owner transport is required")
	}
	return execution.TaskRunRecoveryDependencies{
		Scopes:          &executionTaskRunRecoveryScopeAdapter{workspaces: dependencies.Workspaces},
		ChildRecoveries: recoveries,
	}, nil
}

type executionTaskRunRecoveryScopeAdapter struct {
	workspaces store.WorkspaceStore
}

func (adapter *executionTaskRunRecoveryScopeAdapter) ListTaskRunRecoveryWorkspaces(ctx context.Context) ([]string, error) {
	workspaces, err := adapter.workspaces.List(ctx)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(workspaces))
	for _, workspace := range workspaces {
		if workspace != nil && workspace.Key != "" {
			keys = append(keys, workspace.Key)
		}
	}
	sort.Strings(keys)
	return keys, nil
}

type executionTaskRunFleetRecoveryAdapter struct {
	transport fleetdb.ExecutionTransport
}

func (adapter *executionTaskRunFleetRecoveryAdapter) RecoverStaleChildTaskRuns(ctx context.Context, command execution.RecoverStaleChildTaskRunsCommand) (execution.RecoverStaleTaskRunsResult, error) {
	result, err := adapter.transport.RecoverStaleChildTaskRuns(ctx, fleetdb.ExecutionDriverRunStaleTaskRecoveryCommand{
		WorkspaceKey: command.WorkspaceKey, RequestID: command.RequestID, RunID: command.DriverRunID,
		NodeID: command.ParentOwner.NodeID, LeaseID: command.ParentOwner.LeaseID,
		LeaseToken: command.ParentOwner.LeaseToken, FencingToken: command.ParentOwner.FencingToken,
		StaleBefore: command.StaleBefore, ErrorClass: command.ErrorClass, ErrorMessage: command.ErrorMessage,
	})
	if err != nil {
		return execution.RecoverStaleTaskRunsResult{}, mapFleetExecutionPortError(err)
	}
	if result == nil {
		return execution.RecoverStaleTaskRunsResult{}, execution.ErrConflict
	}
	return execution.RecoverStaleTaskRunsResult{
		WorkspaceKey: command.WorkspaceKey, StaleBefore: result.StaleBefore, RecoveredAt: result.RecoveredAt,
		Recovered: result.Recovered, Released: result.Released, SkippedFresh: result.SkippedFresh,
		RecoveredTaskRunIDs: append([]string(nil), result.RecoveredTaskRunIDs...),
	}, nil
}

// executionTaskRunLegacyRecoveryAdapter is a test-only parity seam. Production
// composition never reaches the tokenless DriverRunStore command.
type executionTaskRunLegacyRecoveryAdapter struct {
	driverRuns store.DriverRunStore
}

func (adapter *executionTaskRunLegacyRecoveryAdapter) RecoverStaleChildTaskRuns(ctx context.Context, command execution.RecoverStaleChildTaskRunsCommand) (execution.RecoverStaleTaskRunsResult, error) {
	result, err := adapter.driverRuns.RecoverStaleTaskRuns(ctx, command.WorkspaceKey, command.DriverRunID, store.StaleTaskRunRecovery{
		StaleBefore: command.StaleBefore, ErrorClass: command.ErrorClass, ErrorMessage: command.ErrorMessage,
	})
	if err != nil {
		return execution.RecoverStaleTaskRunsResult{}, err
	}
	if result == nil {
		return execution.RecoverStaleTaskRunsResult{}, execution.ErrConflict
	}
	return execution.RecoverStaleTaskRunsResult{
		WorkspaceKey: result.WorkspaceKey, StaleBefore: result.StaleBefore, RecoveredAt: result.RecoveredAt,
		Recovered: result.Recovered, Released: result.Released, SkippedFresh: result.SkippedFresh,
		RecoveredTaskRunIDs: append([]string(nil), result.RecoveredTaskRunIDs...),
	}, nil
}

var _ execution.TaskRunRecoveryScopePort = (*executionTaskRunRecoveryScopeAdapter)(nil)
var _ execution.TaskRunStaleChildRecoveryPort = (*executionTaskRunFleetRecoveryAdapter)(nil)
var _ execution.TaskRunStaleChildRecoveryPort = (*executionTaskRunLegacyRecoveryAdapter)(nil)

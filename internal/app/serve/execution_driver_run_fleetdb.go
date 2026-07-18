package serve

import (
	"context"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
)

// fleetDriverRunCommandPort owns every migrated DriverRun owner command. Raw
// lease tokens cross only this in-process boundary and FleetDB's
// X-Lease-Token header; snapshots returned to the module never obtain one
// unless the module needs it transiently to validate the just-issued command.
type fleetDriverRunCommandPort struct {
	transport fleetdb.ExecutionTransport
	now       func() time.Time
}

func newFleetDriverRunCommandPort(transport fleetdb.ExecutionTransport) (*fleetDriverRunCommandPort, error) {
	if transport == nil {
		return nil, execution.ErrUnavailable
	}
	return &fleetDriverRunCommandPort{transport: transport, now: time.Now}, nil
}

func (adapter *fleetDriverRunCommandPort) ClaimDriverRun(ctx context.Context, command execution.ClaimDriverRunCommand) (*execution.DriverRun, error) {
	run, err := adapter.transport.ClaimDriverRun(ctx, fleetdb.ExecutionDriverRunClaimCommand{
		WorkspaceKey: command.WorkspaceKey, RequestID: command.RequestID, RunID: command.RunID,
		NodeID: command.NodeID, LeaseID: command.LeaseID, LeaseToken: command.LeaseToken,
	})
	if err != nil {
		return nil, mapFleetExecutionPortError(err)
	}
	snapshot, err := executionDriverRunSnapshot(run)
	if err != nil {
		return nil, err
	}
	snapshot.Owner.LeaseToken = command.LeaseToken
	return snapshot, nil
}

func (adapter *fleetDriverRunCommandPort) HeartbeatDriverRun(ctx context.Context, command execution.DriverRunHeartbeatCommand) (*execution.DriverRun, error) {
	run, err := adapter.transport.HeartbeatDriverRun(ctx, fleetdb.ExecutionDriverRunHeartbeatCommand{
		WorkspaceKey: command.WorkspaceKey, RunID: command.Owner.ResourceID, NodeID: command.Owner.NodeID,
		LeaseID: command.Owner.LeaseID, LeaseToken: command.Owner.LeaseToken, FencingToken: command.Owner.FencingToken,
	})
	if err != nil {
		return nil, mapFleetExecutionPortError(err)
	}
	snapshot, err := executionDriverRunSnapshot(run)
	if err != nil {
		return nil, err
	}
	snapshot.Owner.LeaseToken = command.Owner.LeaseToken
	return snapshot, nil
}

func (adapter *fleetDriverRunCommandPort) ClaimDriverRunWorkItem(
	ctx context.Context,
	command execution.ClaimDriverRunWorkItemCommand,
) (execution.DriverRunWorkItemMutationResult, error) {
	result, err := adapter.transport.ClaimDriverRunWorkItem(ctx, fleetdb.ExecutionDriverRunWorkItemClaimCommand{
		WorkspaceKey: command.WorkspaceKey, CommandID: command.RequestID, RunID: command.Owner.ResourceID,
		TaskID: command.WorkItemID, NodeID: command.Owner.NodeID, LeaseID: command.Owner.LeaseID,
		LeaseToken: command.Owner.LeaseToken, FencingToken: command.Owner.FencingToken,
		ClaimTTL: command.ClaimTTL, ClaimedAt: command.ClaimedAt,
	})
	if err != nil {
		return execution.DriverRunWorkItemMutationResult{}, mapFleetExecutionPortError(err)
	}
	return executionDriverRunWorkItemMutationResult(result)
}

func (adapter *fleetDriverRunCommandPort) ReleaseDriverRunWorkItem(
	ctx context.Context,
	command execution.ReleaseDriverRunWorkItemCommand,
) (execution.DriverRunWorkItemMutationResult, error) {
	result, err := adapter.transport.ReleaseDriverRunWorkItem(ctx, fleetdb.ExecutionDriverRunWorkItemReleaseCommand{
		WorkspaceKey: command.WorkspaceKey, CommandID: command.RequestID, RunID: command.Owner.ResourceID,
		TaskID: command.WorkItemID, NodeID: command.Owner.NodeID, LeaseID: command.Owner.LeaseID,
		LeaseToken: command.Owner.LeaseToken, FencingToken: command.Owner.FencingToken,
		ClaimActionID: command.ClaimActionID, ReleasedAt: command.ReleasedAt,
	})
	if err != nil {
		return execution.DriverRunWorkItemMutationResult{}, mapFleetExecutionPortError(err)
	}
	return executionDriverRunWorkItemMutationResult(result)
}

func executionDriverRunWorkItemMutationResult(
	result *fleetdb.ExecutionDriverRunWorkItemResult,
) (execution.DriverRunWorkItemMutationResult, error) {
	if result == nil || result.Issue == nil || result.Action == nil {
		return execution.DriverRunWorkItemMutationResult{}, execution.ErrConflict
	}
	issue, action := result.Issue, result.Action
	return execution.DriverRunWorkItemMutationResult{
		WorkItem: &execution.DriverRunWorkItem{
			WorkspaceKey: issue.Workspace, WorkItemID: issue.ID, Title: issue.Title, Status: issue.Status,
			Priority: issue.Priority, IssueType: issue.Type, Assignee: issue.Assignee,
			Labels: append([]string(nil), issue.Labels...), SourceRepo: issue.Repo, ParentID: issue.ParentID,
			UpdatedAt: issue.UpdatedAt,
		},
		Action: &execution.DriverRunWorkItemAction{
			WorkspaceKey: action.WorkspaceKey, ActionID: action.ActionID, IdempotencyKey: action.IdempotencyKey,
			ActionType: action.ActionType, TargetRef: action.TargetRef, RequestedBy: action.RequestedBy,
			Status: action.Status, RequestRef: action.RequestRef, ResponseRef: action.ResponseRef,
			CreatedAt: action.CreatedAt, AppliedAt: action.AppliedAt,
		},
		Replay: result.Replayed,
	}, nil
}

func (adapter *fleetDriverRunCommandPort) SuspendDriverRun(ctx context.Context, workspace string, owner execution.Owner, instanceKey string) (*execution.DriverRun, error) {
	run, err := adapter.transport.SuspendDriverRun(ctx, fleetdb.ExecutionDriverRunSuspendCommand{
		WorkspaceKey: workspace, RunID: owner.ResourceID, NodeID: owner.NodeID,
		LeaseID: owner.LeaseID, LeaseToken: owner.LeaseToken, FencingToken: owner.FencingToken,
		AwaitInstanceKey: instanceKey,
	})
	if err != nil {
		return nil, mapFleetExecutionPortError(err)
	}
	return executionDriverRunSnapshot(run)
}

func (adapter *fleetDriverRunCommandPort) FinalizeDriverRun(ctx context.Context, command execution.FinalizeDriverRunCommand) (*execution.DriverRun, error) {
	status, err := legacyDriverRunStatus(command.Status)
	if err != nil {
		return nil, err
	}
	run, err := adapter.transport.FinalizeDriverRun(ctx, fleetdb.ExecutionDriverRunFinalizeCommand{
		WorkspaceKey: command.WorkspaceKey, RequestID: command.RequestID, RunID: command.Owner.ResourceID,
		NodeID: command.Owner.NodeID, LeaseID: command.Owner.LeaseID, LeaseToken: command.Owner.LeaseToken,
		FencingToken: command.Owner.FencingToken, Status: status, Summary: command.Summary,
		ErrorClass: command.ErrorClass, Output: cloneExecutionStringMap(command.Output),
	})
	if err != nil {
		return nil, mapFleetExecutionPortError(err)
	}
	return executionDriverRunSnapshot(run)
}

func (adapter *fleetDriverRunCommandPort) StartChildDriverRun(ctx context.Context, command execution.StartChildDriverRunCommand) (execution.StartChildDriverRunResult, error) {
	result, err := adapter.transport.StartChildDriverRun(ctx, fleetdb.ExecutionDriverRunChildStartCommand{
		WorkspaceKey: command.WorkspaceKey, RequestID: command.RequestID, ParentRunID: command.Owner.ResourceID,
		NodeID: command.Owner.NodeID, LeaseID: command.Owner.LeaseID, LeaseToken: command.Owner.LeaseToken,
		FencingToken: command.Owner.FencingToken, ChildKey: command.ChildKey, ChildRunID: command.ChildRunID,
		DriverID: command.DriverID, DriverVersionID: command.DriverVersionID, Payload: append([]byte(nil), command.Payload...),
		MaxDepth: command.MaxDepth, RequestedAt: adapter.now().UTC(),
	})
	if err != nil {
		return execution.StartChildDriverRunResult{}, mapFleetExecutionPortError(err)
	}
	if result == nil {
		return execution.StartChildDriverRunResult{}, execution.ErrConflict
	}
	parent, err := executionDriverRunSnapshot(result.Parent)
	if err != nil {
		return execution.StartChildDriverRunResult{}, err
	}
	child, err := executionDriverRunSnapshot(result.Child)
	if err != nil {
		return execution.StartChildDriverRunResult{}, err
	}
	if !result.Replay {
		parent.Owner.LeaseToken = command.Owner.LeaseToken
	}
	return execution.StartChildDriverRunResult{
		Parent: parent, Child: child, ParentDepth: result.ParentDepth, ChildDepth: result.ChildDepth,
		ActionID: result.ActionID, Replay: result.Replay,
	}, nil
}

func (adapter *fleetDriverRunCommandPort) CascadeChildDriverRuns(ctx context.Context, command execution.CascadeChildDriverRunsCommand) (execution.CascadeChildDriverRunsResult, error) {
	transportCommand := fleetdb.ExecutionDriverRunCascadeCommand{
		WorkspaceKey: command.WorkspaceKey, RequestID: command.RequestID, ParentRunID: command.ParentRunID,
		ParentStatus: legacyDriverRunStatusValue(command.ParentStatus), Reason: command.Reason,
		ErrorClass: command.ErrorClass, CascadedAt: command.CascadedAt, MaxDepth: command.MaxDepth,
		SystemRecovery: command.SystemRecovery,
	}
	if !command.SystemRecovery {
		transportCommand.NodeID = command.Owner.NodeID
		transportCommand.LeaseID = command.Owner.LeaseID
		transportCommand.LeaseToken = command.Owner.LeaseToken
		transportCommand.FencingToken = command.Owner.FencingToken
	}
	result, err := adapter.transport.CascadeChildDriverRuns(ctx, transportCommand)
	if err != nil {
		return execution.CascadeChildDriverRunsResult{}, mapFleetExecutionPortError(err)
	}
	if result == nil || result.Committed == nil {
		return execution.CascadeChildDriverRunsResult{}, execution.ErrConflict
	}
	cancelled, err := executionDriverRunSnapshots(result.CancelledRuns)
	if err != nil {
		return execution.CascadeChildDriverRunsResult{}, err
	}
	requested, err := executionDriverRunSnapshots(result.CancelRequestedRuns)
	if err != nil {
		return execution.CascadeChildDriverRunsResult{}, err
	}
	return execution.CascadeChildDriverRunsResult{
		CancelledRuns: cancelled, CancelRequestedRuns: requested,
		Committed: &execution.CascadeChildDriverRunsCommit{
			WorkspaceKey: result.Committed.WorkspaceKey, ParentRunID: result.Committed.ParentRunID,
			ParentStatus: execution.DriverRunStatus(result.Committed.ParentStatus), Reason: result.Committed.Reason,
			ErrorClass: result.Committed.ErrorClass, CascadedAt: result.Committed.CascadedAt, MaxDepth: result.Committed.MaxDepth,
			CancelledRunIDs:       append([]string(nil), result.Committed.CancelledRunIDs...),
			CancelRequestedRunIDs: append([]string(nil), result.Committed.CancelRequestedRunIDs...),
		},
		ActionID: result.ActionID, Replay: result.Replay,
	}, nil
}

func executionDriverRunSnapshots(runs []*domain.DriverRun) ([]*execution.DriverRun, error) {
	out := make([]*execution.DriverRun, 0, len(runs))
	for _, run := range runs {
		snapshot, err := executionDriverRunSnapshot(run)
		if err != nil {
			return nil, err
		}
		out = append(out, snapshot)
	}
	return out, nil
}

func legacyDriverRunStatusValue(status execution.DriverRunStatus) domain.DriverRunStatus {
	return domain.DriverRunStatus(status)
}

// executionDriverAwaitFleetPort keeps await registration/satisfied reads and
// system resume on their existing atomic await adapter while routing the only
// live-owner mutation, suspend, through the raw-token Fleet transport.
type executionDriverAwaitFleetPort struct {
	queries     execution.DriverAwaitPort
	suspensions *fleetDriverRunCommandPort
}

func (adapter *executionDriverAwaitFleetPort) RegisterAndCheckDriverAwait(ctx context.Context, workspace string, registration execution.DriverAwaitRegistration) (*execution.DriverAwaitRegistrationResult, error) {
	return adapter.queries.RegisterAndCheckDriverAwait(ctx, workspace, registration)
}

func (adapter *executionDriverAwaitFleetPort) GetSatisfiedDriverAwait(ctx context.Context, workspace, instanceKey string) (*execution.DriverAwaitInstance, error) {
	return adapter.queries.GetSatisfiedDriverAwait(ctx, workspace, instanceKey)
}

func (adapter *executionDriverAwaitFleetPort) SuspendDriverRun(ctx context.Context, workspace string, owner execution.Owner, instanceKey string) (*execution.DriverRun, error) {
	return adapter.suspensions.SuspendDriverRun(ctx, workspace, owner, instanceKey)
}

func (adapter *executionDriverAwaitFleetPort) ResumeAwaitingDriverRun(ctx context.Context, workspace, runID, instanceKey, eventID string) (*execution.DriverRun, error) {
	return adapter.queries.ResumeAwaitingDriverRun(ctx, workspace, runID, instanceKey, eventID)
}

var _ execution.DriverAwaitPort = (*executionDriverAwaitFleetPort)(nil)

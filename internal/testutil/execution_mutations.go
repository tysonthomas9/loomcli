package testutil

import (
	"context"
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/modules/execution"
)

// TaskRunMutationAdapter gives focused tests a consumer-owned Execution port
// over their in-memory TaskRun repository. Production composition never uses
// this package and always binds the Fleet transport adapter.
type TaskRunMutationAdapter struct {
	TaskRuns execution.TaskRunStore
}

func (adapter TaskRunMutationAdapter) Heartbeat(
	ctx context.Context,
	command execution.HeartbeatCommand,
) (execution.HeartbeatResult, error) {
	if adapter.TaskRuns == nil {
		return execution.HeartbeatResult{}, execution.ErrUnavailable
	}
	run, err := adapter.TaskRuns.Heartbeat(ctx, command.WorkspaceKey, command.Owner.ResourceID, execution.TaskRunHeartbeat{
		NodeID: command.Owner.NodeID, LeaseID: command.Owner.LeaseID, LeaseToken: command.Owner.LeaseToken,
		FencingToken: command.Owner.FencingToken, RuntimeMetadata: cloneExecutionMetadata(command.RuntimeMetadata),
		LogsRef: command.LogsRef, ArtifactsRef: command.ArtifactsRef, HeartbeatAt: command.At,
	})
	if err != nil {
		return execution.HeartbeatResult{}, err
	}
	owner, err := taskRunOwner(command.Owner.LeaseToken, run)
	if err != nil {
		return execution.HeartbeatResult{}, err
	}
	return execution.HeartbeatResult{Owner: owner}, nil
}

func (adapter TaskRunMutationAdapter) AppendLog(
	ctx context.Context,
	command execution.AppendLogCommand,
) (execution.LogEntry, error) {
	if adapter.TaskRuns == nil {
		return execution.LogEntry{}, execution.ErrUnavailable
	}
	entry, err := adapter.TaskRuns.AppendLog(ctx, command.WorkspaceKey, command.Owner.ResourceID, execution.TaskRunLogAppend{
		RequestID: command.RequestID, NodeID: command.Owner.NodeID, LeaseID: command.Owner.LeaseID,
		LeaseToken: command.Owner.LeaseToken, FencingToken: command.Owner.FencingToken,
		Stream: command.Stream, Text: command.Text, Timestamp: command.Timestamp,
	})
	if err != nil {
		return execution.LogEntry{}, err
	}
	if entry == nil {
		return execution.LogEntry{}, execution.ErrConflict
	}
	return execution.LogEntry{
		TaskRunID: entry.TaskRunID, Sequence: entry.Sequence, Stream: entry.Stream,
		Text: entry.Text, Timestamp: entry.Timestamp,
	}, nil
}

func (adapter TaskRunMutationAdapter) Finalize(
	ctx context.Context,
	command execution.FinalizeCommand,
) (execution.FinalizeResult, error) {
	if adapter.TaskRuns == nil {
		return execution.FinalizeResult{}, execution.ErrUnavailable
	}
	status, err := taskRunStatus(command.Classification.Status)
	if err != nil {
		return execution.FinalizeResult{}, err
	}
	run, err := adapter.TaskRuns.Complete(ctx, command.WorkspaceKey, command.Owner.ResourceID, execution.TaskRunComplete{
		CompletionID: command.RequestID, NodeID: command.Owner.NodeID, LeaseID: command.Owner.LeaseID,
		LeaseToken: command.Owner.LeaseToken, FencingToken: command.Owner.FencingToken, Status: status,
		ExitCode: command.ExitCode, LogsRef: command.LogsRef, ArtifactsRef: command.ArtifactsRef,
		RequiredArtifactIDs: append([]string(nil), command.RequiredArtifactIDs...), RequireArtifacts: command.RequireArtifacts,
		InputTokens: command.InputTokens, OutputTokens: command.OutputTokens, CacheReadTokens: command.CacheReadTokens,
		CacheWriteTokens: command.CacheWriteTokens, EstimatedCostUSD: command.EstimatedCostUSD,
		RuntimeMetadata: cloneExecutionMetadata(command.RuntimeMetadata), ErrorClass: command.Classification.ErrorClass,
		ErrorMessage: command.Classification.Summary, CloseTask: command.CloseWorkItem,
		CloseReason: command.CloseReason, FinishedAt: command.FinishedAt,
	})
	if err != nil {
		return execution.FinalizeResult{}, err
	}
	owner, err := taskRunOwner(command.Owner.LeaseToken, run)
	if err != nil {
		return execution.FinalizeResult{}, err
	}
	finishedAt := command.FinishedAt
	if run.FinishedAt != nil {
		finishedAt = *run.FinishedAt
	}
	return execution.FinalizeResult{Owner: owner, Status: command.Classification.Status, FinishedAt: finishedAt}, nil
}

func taskRunOwner(leaseToken string, run *execution.TaskRunRecord) (execution.Owner, error) {
	if run == nil || strings.TrimSpace(run.TaskRunID) == "" || strings.TrimSpace(run.NodeID) == "" ||
		strings.TrimSpace(run.LeaseID) == "" || run.FencingToken <= 0 {
		return execution.Owner{}, execution.ErrConflict
	}
	return execution.Owner{
		ResourceKind: execution.ResourceTaskRun, ResourceID: run.TaskRunID,
		NodeID: run.NodeID, LeaseID: run.LeaseID, LeaseToken: leaseToken, FencingToken: run.FencingToken,
	}, nil
}

func taskRunStatus(status execution.Status) (execution.TaskRunRecordStatus, error) {
	switch status {
	case execution.StatusSucceeded:
		return execution.TaskRunRecordCompleted, nil
	case execution.StatusFailed, execution.StatusBlocked:
		return execution.TaskRunRecordFailed, nil
	case execution.StatusCancelled:
		return execution.TaskRunRecordCancelled, nil
	default:
		return "", fmt.Errorf("unsupported terminal Execution status %q: %w", status, execution.ErrInvalid)
	}
}

func cloneExecutionMetadata(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

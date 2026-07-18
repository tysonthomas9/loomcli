package serve

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
)

type retryExhaustionFleetTransportStub struct {
	fleetExecutionTransportStub
	command fleetdb.ExecutionTaskRunRetryExhaustionCommand
	result  *fleetdb.ExecutionTaskRunRetryExhaustionResult
}

func (stub *retryExhaustionFleetTransportStub) ExhaustTaskRunRetries(
	_ context.Context,
	command fleetdb.ExecutionTaskRunRetryExhaustionCommand,
) (*fleetdb.ExecutionTaskRunRetryExhaustionResult, error) {
	stub.command = command
	return stub.result, nil
}

func TestFleetTaskRunRetryExhaustionPortUsesCommittedWorkItemIdentityWithoutCurrentIssue(t *testing.T) {
	transport := &retryExhaustionFleetTransportStub{result: retryExhaustionFleetResult(nil, false, false)}
	_, _, _, port, err := NewFleetTaskRunCommandPorts(transport)
	if err != nil {
		t.Fatal(err)
	}
	owner := execution.Owner{
		ResourceKind: execution.ResourceTaskRun, ResourceID: "task-run-1", NodeID: "node-1",
		LeaseID: "lease-1", LeaseToken: "raw-task-secret", FencingToken: 7,
	}
	result, err := port.ExhaustTaskRunRetries(context.Background(), execution.ExhaustTaskRunRetriesCommand{
		WorkspaceKey: "WS", RequestID: "exhaust-1", Owner: owner, Attempt: 3, MaxAttempts: 3,
		ErrorClass: "agent_failed", ErrorMessage: "retry budget exhausted", FinishedAt: retryExhaustionPortFinishedAt(),
	})
	if err != nil {
		t.Fatalf("ExhaustTaskRunRetries: %v", err)
	}
	if result.WorkItemID != "TASK-1" || result.Run.WorkItemID != "TASK-1" || result.WorkItemBlocked ||
		result.Committed == nil || result.Committed.WorkItemID != "TASK-1" || result.Committed.WorkItemBlocked {
		t.Fatalf("result = %+v", result)
	}
	if transport.command.TaskRunID != owner.ResourceID || transport.command.LeaseToken != owner.LeaseToken ||
		transport.command.FencingToken != owner.FencingToken {
		t.Fatalf("transport command = %+v", transport.command)
	}
}

func TestFleetTaskRunRetryExhaustionPortReportsCurrentProjectionSeparatelyFromCommittedOutcome(t *testing.T) {
	for _, test := range []struct {
		name             string
		issue            *fleetdb.ExecutionIssue
		committedBlocked bool
		replayed         bool
		wantCurrentBlock bool
	}{
		{
			name:             "exact generation freshly blocked",
			issue:            retryExhaustionFleetIssue("blocked"),
			committedBlocked: true,
			wantCurrentBlock: true,
		},
		{
			name:             "successor preserved",
			issue:            retryExhaustionFleetIssue("in_progress"),
			committedBlocked: false,
			wantCurrentBlock: false,
		},
		{
			name:             "replay observes later successor",
			issue:            retryExhaustionFleetIssue("in_progress"),
			committedBlocked: true,
			replayed:         true,
			wantCurrentBlock: false,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			transport := &retryExhaustionFleetTransportStub{
				result: retryExhaustionFleetResult(test.issue, test.committedBlocked, test.replayed),
			}
			_, _, _, port, err := NewFleetTaskRunCommandPorts(transport)
			if err != nil {
				t.Fatal(err)
			}
			result, err := port.ExhaustTaskRunRetries(context.Background(), retryExhaustionPortCommand())
			if err != nil {
				t.Fatalf("ExhaustTaskRunRetries: %v", err)
			}
			if result.WorkItemBlocked != test.wantCurrentBlock || result.Committed.WorkItemBlocked != test.committedBlocked ||
				result.Replay != test.replayed {
				t.Fatalf("result = %+v", result)
			}
		})
	}
}

func TestFleetTaskRunRetryExhaustionPortRejectsMismatchedWorkItemIdentity(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*fleetdb.ExecutionTaskRunRetryExhaustionResult)
	}{
		{
			name: "missing committed task id",
			edit: func(result *fleetdb.ExecutionTaskRunRetryExhaustionResult) {
				result.Committed.TaskID = ""
			},
		},
		{
			name: "commit differs from terminal task run",
			edit: func(result *fleetdb.ExecutionTaskRunRetryExhaustionResult) {
				result.Committed.TaskID = "TASK-2"
			},
		},
		{
			name: "current issue differs from commit",
			edit: func(result *fleetdb.ExecutionTaskRunRetryExhaustionResult) {
				result.Issue.ID = "TASK-2"
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := retryExhaustionFleetResult(retryExhaustionFleetIssue("in_progress"), false, false)
			test.edit(fixture)
			transport := &retryExhaustionFleetTransportStub{result: fixture}
			_, _, _, port, err := NewFleetTaskRunCommandPorts(transport)
			if err != nil {
				t.Fatal(err)
			}
			_, err = port.ExhaustTaskRunRetries(context.Background(), retryExhaustionPortCommand())
			if !errors.Is(err, execution.ErrConflict) {
				t.Fatalf("error = %v, want ErrConflict", err)
			}
		})
	}
}

func retryExhaustionFleetResult(issue *fleetdb.ExecutionIssue, issueBlocked, replayed bool) *fleetdb.ExecutionTaskRunRetryExhaustionResult {
	return &fleetdb.ExecutionTaskRunRetryExhaustionResult{
		TaskRun: &domain.TaskRun{
			WorkspaceKey: "WS", TaskRunID: "task-run-1", TaskID: "TASK-1", Status: domain.TaskRunFailed,
		},
		Issue: issue,
		Action: &fleetdb.ExecutionActionLedger{
			WorkspaceKey: "WS", ActionID: "task-run-exhaust:exhaust-1",
		},
		Committed: fleetdb.ExecutionTaskRunRetryExhaustionCommit{
			WorkspaceKey: "WS", TaskRunID: "task-run-1", TaskID: "TASK-1", Status: domain.TaskRunFailed,
			IssueBlocked: issueBlocked, Attempt: 3, MaxAttempts: 3,
			ErrorClass: "agent_failed", ErrorMessage: "retry budget exhausted", FinishedAt: retryExhaustionPortFinishedAt(),
		},
		Replayed: replayed,
	}
}

func retryExhaustionFleetIssue(status string) *fleetdb.ExecutionIssue {
	return &fleetdb.ExecutionIssue{
		Workspace: "WS", ID: "TASK-1", Status: status, UpdatedAt: retryExhaustionPortFinishedAt().Add(time.Second),
	}
}

func retryExhaustionPortCommand() execution.ExhaustTaskRunRetriesCommand {
	return execution.ExhaustTaskRunRetriesCommand{
		WorkspaceKey: "WS", RequestID: "exhaust-1",
		Owner: execution.Owner{
			ResourceKind: execution.ResourceTaskRun, ResourceID: "task-run-1", NodeID: "node-1",
			LeaseID: "lease-1", LeaseToken: "raw-task-secret", FencingToken: 7,
		},
		Attempt: 3, MaxAttempts: 3, ErrorClass: "agent_failed",
		ErrorMessage: "retry budget exhausted", FinishedAt: retryExhaustionPortFinishedAt(),
	}
}

func retryExhaustionPortFinishedAt() time.Time {
	return time.Date(2026, 7, 17, 12, 0, 0, 123456000, time.UTC)
}

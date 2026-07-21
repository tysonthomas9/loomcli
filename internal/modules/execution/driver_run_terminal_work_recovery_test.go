package execution

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type driverRunTerminalWorkRecoveryPortStub struct {
	command RecoverTerminalDriverRunWorkCommand
	result  RecoverTerminalDriverRunWorkResult
	err     error
	calls   int
}

func (stub *driverRunTerminalWorkRecoveryPortStub) RecoverTerminalDriverRunWork(
	_ context.Context,
	command RecoverTerminalDriverRunWorkCommand,
) (RecoverTerminalDriverRunWorkResult, error) {
	stub.calls++
	stub.command = command
	return stub.result, stub.err
}

func TestRecoverTerminalDriverRunWorkIsSystemOnlyAndUsesAtomicPort(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	command := RecoverTerminalDriverRunWorkCommand{
		WorkspaceKey: "TEST",
		DriverRunID:  "run-parent",
		ParentStatus: DriverRunFailed,
		Reason:       "parent heartbeat became stale",
		ErrorClass:   "stale_driver_run",
		RecoveredAt:  now,
	}
	command.RequestID = RecoverTerminalDriverRunWorkRequestID(command.DriverRunID, command.ParentStatus)
	port := &driverRunTerminalWorkRecoveryPortStub{result: RecoverTerminalDriverRunWorkResult{
		RecoveredTaskRunIDs:           []string{"task-run-a", "task-run-b"},
		ReleasedWorkItemIDs:           []string{"TASK-1"},
		PreservedSuccessorWorkItemIDs: []string{"TASK-2"},
		Committed: &RecoverTerminalDriverRunWorkCommit{
			WorkspaceKey: "TEST", DriverRunID: "run-parent", ParentStatus: DriverRunFailed,
			Reason: "parent heartbeat became stale", ErrorClass: "stale_driver_run", RecoveredAt: now,
			RecoveredTaskRunIDs: []string{"task-run-a", "task-run-b"}, ReleasedWorkItemIDs: []string{"TASK-1"},
			PreservedSuccessorWorkItemIDs: []string{"TASK-2"},
		},
		ActionID: "terminal-work-recovery-action",
	}}
	service, issuer := newDriverRunTestService(t, DriverRunDependencies{TerminalWorkRecovery: port})

	result, err := service.RecoverTerminalDriverRunWork(
		context.Background(),
		issueSystem(t, issuer, ActionRecoverTerminalDriverRunWork),
		command,
	)
	if err != nil {
		t.Fatalf("RecoverTerminalDriverRunWork: %v", err)
	}
	if port.calls != 1 || port.command != command || result.ActionID != port.result.ActionID || result.Replay {
		t.Fatalf("command=%+v result=%+v calls=%d", port.command, result, port.calls)
	}
	result.RecoveredTaskRunIDs[0] = "mutated"
	result.ReleasedWorkItemIDs[0] = "mutated"
	result.PreservedSuccessorWorkItemIDs[0] = "mutated"
	result.Committed.RecoveredTaskRunIDs[0] = "mutated"
	if port.result.RecoveredTaskRunIDs[0] != "task-run-a" || port.result.ReleasedWorkItemIDs[0] != "TASK-1" ||
		port.result.PreservedSuccessorWorkItemIDs[0] != "TASK-2" ||
		port.result.Committed.RecoveredTaskRunIDs[0] != "task-run-a" {
		t.Fatal("public result aliases the port receipt")
	}
	port.result.Replay = true
	port.result.Committed.RecoveredAt = now.Add(-time.Minute)
	if replay, err := service.RecoverTerminalDriverRunWork(
		context.Background(),
		issueSystem(t, issuer, ActionRecoverTerminalDriverRunWork),
		command,
	); err != nil || !replay.Replay || port.calls != 2 {
		t.Fatalf("replay=%+v calls=%d error=%v", replay, port.calls, err)
	}

	before := port.calls
	if _, err := service.RecoverTerminalDriverRunWork(
		context.Background(),
		issueSystem(t, issuer, ActionClaimDriverRun),
		command,
	); err == nil || port.calls != before {
		t.Fatalf("wrong-action error=%v calls=%d", err, port.calls)
	}
}

func TestRecoverTerminalDriverRunWorkRejectsDivergentCommitEnvelope(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	command := RecoverTerminalDriverRunWorkCommand{
		WorkspaceKey: "TEST", DriverRunID: "run-parent", ParentStatus: DriverRunFailed,
		Reason: "parent failed", ErrorClass: "parent_run_terminal", RecoveredAt: now,
	}
	command.RequestID = RecoverTerminalDriverRunWorkRequestID(command.DriverRunID, command.ParentStatus)
	tests := []struct {
		name   string
		mutate func(*RecoverTerminalDriverRunWorkCommit)
	}{
		{name: "workspace", mutate: func(commit *RecoverTerminalDriverRunWorkCommit) { commit.WorkspaceKey = "OTHER" }},
		{name: "DriverRun", mutate: func(commit *RecoverTerminalDriverRunWorkCommit) { commit.DriverRunID = "run-other" }},
		{name: "status", mutate: func(commit *RecoverTerminalDriverRunWorkCommit) { commit.ParentStatus = DriverRunCancelled }},
		{name: "reason", mutate: func(commit *RecoverTerminalDriverRunWorkCommit) { commit.Reason = "other" }},
		{name: "error class", mutate: func(commit *RecoverTerminalDriverRunWorkCommit) { commit.ErrorClass = "other" }},
		{name: "first response time", mutate: func(commit *RecoverTerminalDriverRunWorkCommit) { commit.RecoveredAt = now.Add(-time.Minute) }},
		{name: "identity sets", mutate: func(commit *RecoverTerminalDriverRunWorkCommit) { commit.ReleasedWorkItemIDs = nil }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			commit := &RecoverTerminalDriverRunWorkCommit{
				WorkspaceKey: "TEST", DriverRunID: "run-parent", ParentStatus: DriverRunFailed,
				Reason: "parent failed", ErrorClass: "parent_run_terminal", RecoveredAt: now,
				ReleasedWorkItemIDs: []string{"TASK-1"},
			}
			test.mutate(commit)
			port := &driverRunTerminalWorkRecoveryPortStub{result: RecoverTerminalDriverRunWorkResult{
				ReleasedWorkItemIDs: []string{"TASK-1"}, Committed: commit, ActionID: "action",
			}}
			service, issuer := newDriverRunTestService(t, DriverRunDependencies{TerminalWorkRecovery: port})
			if _, err := service.RecoverTerminalDriverRunWork(
				context.Background(),
				issueSystem(t, issuer, ActionRecoverTerminalDriverRunWork),
				command,
			); !errors.Is(err, ErrConflict) {
				t.Fatalf("error=%v, want conflict", err)
			}
		})
	}
}

func TestRecoverTerminalDriverRunWorkValidatesTerminalEnvelopeBeforePort(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	valid := RecoverTerminalDriverRunWorkCommand{
		WorkspaceKey: "TEST",
		DriverRunID:  "run-parent",
		ParentStatus: DriverRunFailed,
		Reason:       "parent failed",
		ErrorClass:   "parent_run_terminal",
		RecoveredAt:  now,
	}
	valid.RequestID = RecoverTerminalDriverRunWorkRequestID(valid.DriverRunID, valid.ParentStatus)
	port := &driverRunTerminalWorkRecoveryPortStub{result: RecoverTerminalDriverRunWorkResult{
		Committed: &RecoverTerminalDriverRunWorkCommit{
			WorkspaceKey: "TEST", DriverRunID: "run-parent", ParentStatus: DriverRunFailed,
			Reason: "parent failed", ErrorClass: "parent_run_terminal", RecoveredAt: now,
		},
		ActionID: "action-1",
	}}
	service, issuer := newDriverRunTestService(t, DriverRunDependencies{TerminalWorkRecovery: port})
	auth := issueSystem(t, issuer, ActionRecoverTerminalDriverRunWork)

	tests := []struct {
		name   string
		mutate func(*RecoverTerminalDriverRunWorkCommand)
	}{
		{name: "missing DriverRun", mutate: func(command *RecoverTerminalDriverRunWorkCommand) { command.DriverRunID = "" }},
		{name: "nonterminal parent", mutate: func(command *RecoverTerminalDriverRunWorkCommand) { command.ParentStatus = DriverRunRunning }},
		{name: "wrong request ID", mutate: func(command *RecoverTerminalDriverRunWorkCommand) { command.RequestID = "other" }},
		{name: "missing reason", mutate: func(command *RecoverTerminalDriverRunWorkCommand) { command.Reason = " " }},
		{name: "missing error class", mutate: func(command *RecoverTerminalDriverRunWorkCommand) { command.ErrorClass = " " }},
		{name: "missing recovery time", mutate: func(command *RecoverTerminalDriverRunWorkCommand) { command.RecoveredAt = time.Time{} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			command := valid
			test.mutate(&command)
			if _, err := service.RecoverTerminalDriverRunWork(context.Background(), auth, command); !errors.Is(err, ErrInvalid) {
				t.Fatalf("error=%v, want invalid", err)
			}
		})
	}
	if port.calls != 0 {
		t.Fatalf("invalid commands reached port %d times", port.calls)
	}

	service, issuer = newDriverRunTestService(t, DriverRunDependencies{})
	if _, err := service.RecoverTerminalDriverRunWork(
		context.Background(),
		issueSystem(t, issuer, ActionRecoverTerminalDriverRunWork),
		valid,
	); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("missing port error=%v, want unavailable", err)
	}
}

func TestRecoverTerminalDriverRunWorkRejectsNoncanonicalOrUnsafeReceipt(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	command := RecoverTerminalDriverRunWorkCommand{
		WorkspaceKey: "TEST",
		DriverRunID:  "run-parent",
		ParentStatus: DriverRunCancelled,
		Reason:       "parent cancelled",
		ErrorClass:   "parent_run_terminal",
		RecoveredAt:  now,
	}
	command.RequestID = RecoverTerminalDriverRunWorkRequestID(command.DriverRunID, command.ParentStatus)
	baseCommit := RecoverTerminalDriverRunWorkCommit{
		WorkspaceKey: "TEST", DriverRunID: "run-parent", ParentStatus: DriverRunCancelled,
		Reason: "parent cancelled", ErrorClass: "parent_run_terminal", RecoveredAt: now,
	}
	tests := []struct {
		name   string
		result RecoverTerminalDriverRunWorkResult
	}{
		{name: "missing action", result: RecoverTerminalDriverRunWorkResult{}},
		{name: "duplicate TaskRun", result: RecoverTerminalDriverRunWorkResult{
			RecoveredTaskRunIDs: []string{"task-1", "task-1"},
			Committed:           &RecoverTerminalDriverRunWorkCommit{}, ActionID: "action",
		}},
		{name: "unsorted Work Items", result: RecoverTerminalDriverRunWorkResult{
			ReleasedWorkItemIDs: []string{"TASK-2", "TASK-1"},
			Committed:           &RecoverTerminalDriverRunWorkCommit{}, ActionID: "action",
		}},
		{name: "blank successor", result: RecoverTerminalDriverRunWorkResult{
			PreservedSuccessorWorkItemIDs: []string{" "},
			Committed:                     &RecoverTerminalDriverRunWorkCommit{}, ActionID: "action",
		}},
		{name: "released successor", result: RecoverTerminalDriverRunWorkResult{
			ReleasedWorkItemIDs: []string{"TASK-1"}, PreservedSuccessorWorkItemIDs: []string{"TASK-1"},
			Committed: &RecoverTerminalDriverRunWorkCommit{}, ActionID: "action",
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.result.Committed != nil {
				commit := baseCommit
				commit.RecoveredTaskRunIDs = append([]string(nil), test.result.RecoveredTaskRunIDs...)
				commit.ReleasedWorkItemIDs = append([]string(nil), test.result.ReleasedWorkItemIDs...)
				commit.PreservedSuccessorWorkItemIDs = append([]string(nil), test.result.PreservedSuccessorWorkItemIDs...)
				test.result.Committed = &commit
			}
			port := &driverRunTerminalWorkRecoveryPortStub{result: test.result}
			service, issuer := newDriverRunTestService(t, DriverRunDependencies{TerminalWorkRecovery: port})
			if _, err := service.RecoverTerminalDriverRunWork(
				context.Background(),
				issueSystem(t, issuer, ActionRecoverTerminalDriverRunWork),
				command,
			); !errors.Is(err, ErrConflict) {
				t.Fatalf("error=%v, want conflict", err)
			}
		})
	}
}

func TestRecoverTerminalDriverRunWorkRequestIDIsDeterministicAndBounded(t *testing.T) {
	short := RecoverTerminalDriverRunWorkRequestID("run-1", DriverRunFailed)
	if short != RecoverTerminalDriverRunWorkRequestID("run-1", DriverRunFailed) || len(short) > driverRunWorkItemRequestIDMaxLength {
		t.Fatalf("short identity=%q", short)
	}
	if short == RecoverTerminalDriverRunWorkRequestID("run-1", DriverRunCancelled) ||
		short == RecoverTerminalDriverRunWorkRequestID("run-2", DriverRunFailed) {
		t.Fatalf("request identity does not bind the parent/status pair: %q", short)
	}
	long := RecoverTerminalDriverRunWorkRequestID(strings.Repeat("r", 500), DriverRunFailed)
	if len(long) > driverRunWorkItemRequestIDMaxLength {
		t.Fatalf("long identity length=%d value=%q", len(long), long)
	}
}

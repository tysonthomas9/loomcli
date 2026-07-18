package execution

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

type taskRunRequestPortStub struct {
	command       RequestTaskRunCommand
	result        RequestTaskRunResult
	replayCommand RequestTaskRunCommand
	replayResult  RequestTaskRunResult
	replayErr     error
	replayCalls   int
}

type taskRunSchedulingPortStub struct {
	query  TaskRunSchedulingQuery
	result TaskRunSchedulingResult
	calls  int
}

func (stub *taskRunSchedulingPortStub) CheckTaskRunScheduling(_ context.Context, query TaskRunSchedulingQuery) (TaskRunSchedulingResult, error) {
	stub.calls++
	stub.query = query
	return stub.result, nil
}

func (stub *taskRunRequestPortStub) RequestTaskRun(_ context.Context, command RequestTaskRunCommand) (RequestTaskRunResult, error) {
	stub.command = command
	return stub.result, nil
}

func (stub *taskRunRequestPortStub) ReplayTaskRunRequest(_ context.Context, command RequestTaskRunCommand) (RequestTaskRunResult, error) {
	stub.replayCalls++
	stub.replayCommand = command
	if stub.replayErr != nil {
		return RequestTaskRunResult{}, stub.replayErr
	}
	if stub.replayResult.Run == nil {
		return RequestTaskRunResult{}, ErrTaskRunRequestReplayNotFound
	}
	return stub.replayResult, nil
}

type taskRunClaimPortStub struct {
	command ClaimTaskRunCommand
	result  ClaimTaskRunResult
}

type taskRunRequeuePortStub struct {
	command RequeueTaskRunCommand
	result  RequeueTaskRunResult
}

func (stub *taskRunRequeuePortStub) RequeueTaskRun(_ context.Context, command RequeueTaskRunCommand) (RequeueTaskRunResult, error) {
	stub.command = command
	return stub.result, nil
}

type taskRunRetryExhaustionPortStub struct {
	command ExhaustTaskRunRetriesCommand
	result  ExhaustTaskRunRetriesResult
}

func (stub *taskRunRetryExhaustionPortStub) ExhaustTaskRunRetries(_ context.Context, command ExhaustTaskRunRetriesCommand) (ExhaustTaskRunRetriesResult, error) {
	stub.command = command
	return stub.result, nil
}

func (stub *taskRunClaimPortStub) ClaimTaskRun(_ context.Context, command ClaimTaskRunCommand) (ClaimTaskRunResult, error) {
	stub.command = command
	return stub.result, nil
}

func TestRequestTaskRunIsBoundToParentExecutionAuthority(t *testing.T) {
	now := time.Now().UTC()
	parent := Owner{ResourceKind: ResourceDriverRun, ResourceID: "run-1", NodeID: "node-1", LeaseID: "lease-1", LeaseToken: "driver-token-1", FencingToken: 7}
	taskRunID := "task-run-1"
	stepID := RequestedDriverStepID(parent.ResourceID, taskRunID)
	requestID := RequestTaskRunRequestID(parent.ResourceID, taskRunID)
	port := &taskRunRequestPortStub{result: RequestTaskRunResult{
		Run: &TaskRun{
			WorkspaceKey: "TEST", TaskRunID: taskRunID, DriverRunID: "run-1", DriverStepID: stepID, WorkItemID: "TASK-1", Status: StatusQueued,
			ProviderProfile: "daytona", SandboxPlacement: Placement{Provider: "daytona"},
			RuntimeMetadata: map[string]string{"execution_request_id": requestID}, Input: json.RawMessage(`{"prompt":"review"}`),
		},
		Step:     &TaskRunDriverStep{WorkspaceKey: "TEST", StepID: stepID, DriverRunID: "run-1", TaskRunID: taskRunID, Status: "queued"},
		ActionID: "action-1", ClaimActionID: DriverRunWorkItemClaimActionID(ClaimDriverRunWorkItemRequestID("run-1", "TASK-1")),
	}}
	scheduling := &taskRunSchedulingPortStub{result: TaskRunSchedulingResult{Schedulable: true}}
	service, issuer := newTestService(t, Dependencies{TaskRuns: TaskRunDependencies{Requests: port, Scheduling: scheduling}})
	command := RequestTaskRunCommand{
		WorkspaceKey: "TEST", RequestID: requestID, ParentOwner: parent,
		TaskRunID: taskRunID, DriverRunID: "run-1", WorkItemID: "TASK-1",
		ClaimActionID:   DriverRunWorkItemClaimActionID(ClaimDriverRunWorkItemRequestID("run-1", "TASK-1")),
		ProviderProfile: "daytona", RequiredCapabilities: []string{"repo"},
		SandboxPlacement: Placement{Provider: "daytona"},
		Input:            json.RawMessage(`{"prompt":"review"}`), RequestedAt: now,
	}
	run, err := service.RequestTaskRun(context.Background(), issueExecution(t, issuer, ActionRequestTaskRun, parent), command)
	if err != nil {
		t.Fatalf("RequestTaskRun: %v", err)
	}
	if run.TaskRunID != command.TaskRunID || port.command.DriverStepID != stepID || run.DriverStepID != stepID {
		t.Fatalf("run=%+v command=%+v", run, port.command)
	}
	if scheduling.query.ProviderProfile != "daytona" || len(scheduling.query.RequiredFeatures) != 1 || scheduling.query.RequiredFeatures[0] != "repo" {
		t.Fatalf("scheduling query = %+v", scheduling.query)
	}
	port.result.ClaimActionID = "driver-run-work-item-claim:other"
	if _, err := service.RequestTaskRun(context.Background(), issueExecution(t, issuer, ActionRequestTaskRun, parent), command); !errors.Is(err, ErrConflict) {
		t.Fatalf("divergent claim action error=%v, want conflict", err)
	}
	port.result.ClaimActionID = command.ClaimActionID
	foreign := parent
	foreign.ResourceID = "run-2"
	_, err = service.RequestTaskRun(context.Background(), issueExecution(t, issuer, ActionRequestTaskRun, foreign), command)
	if !errors.Is(err, ErrFenceConflict) {
		t.Fatalf("foreign parent error = %v, want fence conflict", err)
	}
	port.result.Replay = true
	port.result.Run.RunnerRef = "divergent-on-replay"
	if _, err := service.RequestTaskRun(context.Background(), issueExecution(t, issuer, ActionRequestTaskRun, parent), command); !errors.Is(err, ErrConflict) {
		t.Fatalf("divergent request replay error=%v, want conflict", err)
	}
}

func TestRequestTaskRunFailsUnschedulableBeforePersistence(t *testing.T) {
	now := time.Now().UTC()
	parent := Owner{ResourceKind: ResourceDriverRun, ResourceID: "run-1", NodeID: "node-1", LeaseID: "lease-1", LeaseToken: "driver-token-1", FencingToken: 7}
	requests := &taskRunRequestPortStub{}
	scheduling := &taskRunSchedulingPortStub{result: TaskRunSchedulingResult{ReasonCode: "no_live_capable_node"}}
	service, issuer := newTestService(t, Dependencies{TaskRuns: TaskRunDependencies{Requests: requests, Scheduling: scheduling}})
	_, err := service.RequestTaskRun(context.Background(), issueExecution(t, issuer, ActionRequestTaskRun, parent), RequestTaskRunCommand{
		WorkspaceKey: "TEST", RequestID: RequestTaskRunRequestID("run-1", "task-run-1"), ParentOwner: parent,
		TaskRunID: "task-run-1", DriverRunID: "run-1", DriverStepID: "step-1", WorkItemID: "TASK-1",
		ClaimActionID:   DriverRunWorkItemClaimActionID(ClaimDriverRunWorkItemRequestID("run-1", "TASK-1")),
		ProviderProfile: "daytona", RequestedAt: now,
	})
	if !errors.Is(err, ErrUnschedulable) || requests.command.TaskRunID != "" {
		t.Fatalf("error=%v persisted=%+v", err, requests.command)
	}
}

func TestRequestTaskRunReplayBypassesSchedulingDriftAfterCommittedResponseLoss(t *testing.T) {
	now := time.Now().UTC()
	parent := Owner{ResourceKind: ResourceDriverRun, ResourceID: "run-1", NodeID: "driver-node", LeaseID: "driver-lease", LeaseToken: "driver-token-1", FencingToken: 7}
	taskRunID := "task-run-1"
	stepID := RequestedDriverStepID(parent.ResourceID, taskRunID)
	requestID := RequestTaskRunRequestID(parent.ResourceID, taskRunID)
	command := RequestTaskRunCommand{
		WorkspaceKey: "TEST", RequestID: requestID, ParentOwner: parent,
		TaskRunID: taskRunID, DriverRunID: parent.ResourceID, DriverStepID: stepID,
		WorkItemID: "TASK-1", ClaimActionID: DriverRunWorkItemClaimActionID(ClaimDriverRunWorkItemRequestID(parent.ResourceID, "TASK-1")),
		ProviderProfile: "daytona",
		RuntimeMetadata: map[string]string{"requested_by": "driver"}, RequestedAt: now,
	}
	committed := RequestTaskRunResult{
		Run: &TaskRun{
			WorkspaceKey: "TEST", TaskRunID: taskRunID, DriverRunID: parent.ResourceID,
			DriverStepID: stepID, WorkItemID: "TASK-1", ProviderProfile: "daytona", Status: StatusQueued,
			RuntimeMetadata: map[string]string{"requested_by": "driver", "execution_request_id": requestID},
		},
		Step: &TaskRunDriverStep{
			WorkspaceKey: "TEST", StepID: stepID, DriverRunID: parent.ResourceID,
			TaskRunID: taskRunID, Status: "queued",
		},
		ActionID: "request-action", ClaimActionID: command.ClaimActionID,
	}
	requests := &taskRunRequestPortStub{result: committed}
	scheduling := &taskRunSchedulingPortStub{result: TaskRunSchedulingResult{Schedulable: true}}
	service, issuer := newTestService(t, Dependencies{TaskRuns: TaskRunDependencies{Requests: requests, Scheduling: scheduling}})
	auth := issueExecution(t, issuer, ActionRequestTaskRun, parent)
	if _, err := service.RequestTaskRun(context.Background(), auth, command); err != nil {
		t.Fatalf("first request: %v", err)
	}
	if scheduling.calls != 1 {
		t.Fatalf("first request scheduling calls=%d, want 1", scheduling.calls)
	}

	// Model a lost first response followed by live lifecycle advancement and
	// loss of all schedulable capacity. Fleet replays the immutable original
	// queued receipt before live preflight, rather than today's projection.
	replayed := committed
	replayed.Replay = true
	replayed.Run = cloneTaskRun(committed.Run)
	replayed.Step = cloneTaskRunDriverStep(committed.Step)
	requests.replayResult = replayed
	scheduling.result = TaskRunSchedulingResult{ReasonCode: "no_live_capable_node"}
	run, err := service.RequestTaskRun(context.Background(), auth, command)
	if err != nil || run.Status != StatusQueued {
		t.Fatalf("replay after scheduling drift run=%+v err=%v", run, err)
	}
	if scheduling.calls != 1 {
		t.Fatalf("replay ran live scheduling: calls=%d, want 1", scheduling.calls)
	}
}

func TestRequestTaskRunReplayAcceptsSuccessorParentOwnerAndPortRejectsStaleOwner(t *testing.T) {
	now := time.Now().UTC()
	oldOwner := Owner{ResourceKind: ResourceDriverRun, ResourceID: "run-1", NodeID: "old-node", LeaseID: "old-lease", LeaseToken: "old-token", FencingToken: 7}
	successor := Owner{ResourceKind: ResourceDriverRun, ResourceID: "run-1", NodeID: "new-node", LeaseID: "new-lease", LeaseToken: "new-token", FencingToken: 8}
	taskRunID := "task-run-1"
	stepID := RequestedDriverStepID(successor.ResourceID, taskRunID)
	requestID := RequestTaskRunRequestID(successor.ResourceID, taskRunID)
	replay := RequestTaskRunResult{
		Run: &TaskRun{
			WorkspaceKey: "TEST", TaskRunID: taskRunID, DriverRunID: successor.ResourceID,
			DriverStepID: stepID, WorkItemID: "TASK-1", Status: StatusQueued,
			RuntimeMetadata: map[string]string{"execution_request_id": requestID},
		},
		Step: &TaskRunDriverStep{
			WorkspaceKey: "TEST", StepID: stepID, DriverRunID: successor.ResourceID,
			TaskRunID: taskRunID, Status: "queued",
		},
		ActionID: "request-action", ClaimActionID: DriverRunWorkItemClaimActionID(ClaimDriverRunWorkItemRequestID(successor.ResourceID, "TASK-1")), Replay: true,
	}
	requests := &taskRunRequestPortStub{replayResult: replay}
	service, issuer := newTestService(t, Dependencies{TaskRuns: TaskRunDependencies{Requests: requests}})
	command := RequestTaskRunCommand{
		WorkspaceKey: "TEST", RequestID: requestID, ParentOwner: successor,
		TaskRunID: taskRunID, DriverRunID: successor.ResourceID, DriverStepID: stepID,
		WorkItemID: "TASK-1", ClaimActionID: DriverRunWorkItemClaimActionID(ClaimDriverRunWorkItemRequestID(successor.ResourceID, "TASK-1")), RequestedAt: now,
	}
	if _, err := service.RequestTaskRun(context.Background(), issueExecution(t, issuer, ActionRequestTaskRun, successor), command); err != nil {
		t.Fatalf("successor owner replay: %v", err)
	}
	if requests.replayCommand.ParentOwner != successor {
		t.Fatalf("replay port owner=%+v, want successor %+v", requests.replayCommand.ParentOwner, successor)
	}
	// The authoritative port validates the supplied owner against the current
	// parent in the same replay transaction; a self-consistent but stale
	// predecessor authority cannot reuse the semantic receipt.
	requests.replayResult = RequestTaskRunResult{}
	requests.replayErr = ErrFenceConflict
	command.ParentOwner = oldOwner
	if _, err := service.RequestTaskRun(context.Background(), issueExecution(t, issuer, ActionRequestTaskRun, oldOwner), command); !errors.Is(err, ErrFenceConflict) {
		t.Fatalf("stale predecessor replay error=%v, want fence conflict", err)
	}
}

func TestRequestTaskRunReplayRequiresImmutableOriginalQueuedSnapshot(t *testing.T) {
	now := time.Now().UTC()
	parent := Owner{ResourceKind: ResourceDriverRun, ResourceID: "run-1", NodeID: "driver-node", LeaseID: "driver-lease", LeaseToken: "driver-token-1", FencingToken: 7}
	taskRunID := "task-run-1"
	stepID := RequestedDriverStepID(parent.ResourceID, taskRunID)
	requestID := RequestTaskRunRequestID(parent.ResourceID, taskRunID)
	command := RequestTaskRunCommand{
		WorkspaceKey: "TEST", RequestID: requestID, ParentOwner: parent,
		TaskRunID: taskRunID, DriverRunID: parent.ResourceID, DriverStepID: stepID, WorkItemID: "TASK-1",
		ClaimActionID:   DriverRunWorkItemClaimActionID(ClaimDriverRunWorkItemRequestID(parent.ResourceID, "TASK-1")),
		WorkerProfileID: "profile-1", Runner: "agent", RunnerRef: "runner-ref", RunnerKind: "workflow",
		RunnerEntrypoint: "run", RunnerVersionID: "runner-version-1", ProviderProfile: "daytona",
		TargetNodeID: "task-node", RunnerPlacement: Placement{Provider: "requested"},
		SandboxPlacement: Placement{Provider: "daytona"}, RuntimeMetadata: map[string]string{"requested_by": "driver"},
		Input: json.RawMessage(`{"prompt":"review"}`), RequestedAt: now,
	}
	port := &taskRunRequestPortStub{result: RequestTaskRunResult{
		Run: &TaskRun{
			WorkspaceKey: "TEST", TaskRunID: taskRunID, DriverRunID: parent.ResourceID, DriverStepID: stepID, WorkItemID: "TASK-1",
			WorkerProfileID: command.WorkerProfileID, Runner: command.Runner, RunnerRef: command.RunnerRef,
			RunnerKind: command.RunnerKind, RunnerEntrypoint: command.RunnerEntrypoint,
			RunnerVersionID: command.RunnerVersionID, ProviderProfile: command.ProviderProfile,
			TargetNodeID: command.TargetNodeID, Status: StatusQueued,
			RunnerPlacement:  command.RunnerPlacement,
			SandboxPlacement: command.SandboxPlacement,
			RuntimeMetadata: map[string]string{
				"requested_by": "driver", "execution_request_id": requestID,
			},
			Input: append(json.RawMessage(nil), command.Input...),
		},
		Step:     &TaskRunDriverStep{WorkspaceKey: "TEST", StepID: stepID, DriverRunID: parent.ResourceID, TaskRunID: taskRunID, Status: "queued", ActionLedgerID: "request-action"},
		ActionID: "request-action", ClaimActionID: command.ClaimActionID, Replay: true,
	}}
	scheduling := &taskRunSchedulingPortStub{result: TaskRunSchedulingResult{Schedulable: true}}
	service, issuer := newTestService(t, Dependencies{TaskRuns: TaskRunDependencies{Requests: port, Scheduling: scheduling}})
	auth := issueExecution(t, issuer, ActionRequestTaskRun, parent)

	run, err := service.RequestTaskRun(context.Background(), auth, command)
	if err != nil || run.Status != StatusQueued || run.Owner.LeaseToken != "" {
		t.Fatalf("queued receipt replay run=%+v err=%v", run, err)
	}
	port.result.Run.Status = StatusRunning
	port.result.Step.Status = "running"
	if _, err := service.RequestTaskRun(context.Background(), auth, command); !errors.Is(err, ErrConflict) {
		t.Fatalf("mutable lifecycle replay error=%v, want conflict", err)
	}
	port.result.Run.Status = StatusQueued
	port.result.Step.Status = "queued"
	port.result.Run.RunnerVersionID = "divergent-version"
	if _, err := service.RequestTaskRun(context.Background(), auth, command); !errors.Is(err, ErrConflict) {
		t.Fatalf("divergent immutable replay error=%v, want conflict", err)
	}
	port.result.Run.RunnerVersionID = command.RunnerVersionID
	port.result.Run.TargetNodeID = "divergent-target"
	if _, err := service.RequestTaskRun(context.Background(), auth, command); !errors.Is(err, ErrConflict) {
		t.Fatalf("divergent target replay error=%v, want conflict", err)
	}
	port.result.Run.TargetNodeID = command.TargetNodeID
	port.result.Run.SandboxPlacement = Placement{Provider: "divergent-sandbox"}
	if _, err := service.RequestTaskRun(context.Background(), auth, command); !errors.Is(err, ErrConflict) {
		t.Fatalf("divergent sandbox replay error=%v, want conflict", err)
	}
	port.result.Run.SandboxPlacement = command.SandboxPlacement
	delete(port.result.Run.RuntimeMetadata, "execution_request_id")
	if _, err := service.RequestTaskRun(context.Background(), auth, command); !errors.Is(err, ErrConflict) {
		t.Fatalf("missing request metadata replay error=%v, want conflict", err)
	}
}

func TestRequestTaskRunFirstApplicationStillRequiresQueuedProjection(t *testing.T) {
	now := time.Now().UTC()
	parent := Owner{ResourceKind: ResourceDriverRun, ResourceID: "run-1", NodeID: "driver-node", LeaseID: "driver-lease", LeaseToken: "driver-token-1", FencingToken: 7}
	requestID := RequestTaskRunRequestID(parent.ResourceID, "task-run-1")
	stepID := RequestedDriverStepID(parent.ResourceID, "task-run-1")
	port := &taskRunRequestPortStub{result: RequestTaskRunResult{
		Run: &TaskRun{
			WorkspaceKey: "TEST", TaskRunID: "task-run-1", DriverRunID: parent.ResourceID,
			DriverStepID: stepID, WorkItemID: "TASK-1", Status: StatusRunning,
			RuntimeMetadata: map[string]string{"execution_request_id": requestID},
		},
		Step:     &TaskRunDriverStep{WorkspaceKey: "TEST", StepID: stepID, DriverRunID: parent.ResourceID, TaskRunID: "task-run-1", Status: "running"},
		ActionID: "request-action",
	}}
	service, issuer := newTestService(t, Dependencies{TaskRuns: TaskRunDependencies{
		Requests: port, Scheduling: &taskRunSchedulingPortStub{result: TaskRunSchedulingResult{Schedulable: true}},
	}})
	_, err := service.RequestTaskRun(context.Background(), issueExecution(t, issuer, ActionRequestTaskRun, parent), RequestTaskRunCommand{
		WorkspaceKey: "TEST", RequestID: requestID, ParentOwner: parent,
		TaskRunID: "task-run-1", DriverRunID: parent.ResourceID, DriverStepID: stepID,
		WorkItemID: "TASK-1", ClaimActionID: DriverRunWorkItemClaimActionID(ClaimDriverRunWorkItemRequestID(parent.ResourceID, "TASK-1")), RequestedAt: now,
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("non-replay running projection error=%v, want conflict", err)
	}
}

func TestClaimTaskRunKeepsLeaseCredentialPrivate(t *testing.T) {
	now := time.Now().UTC()
	owner := Owner{ResourceKind: ResourceTaskRun, ResourceID: "task-run-1", NodeID: "node-1", LeaseID: "lease-1", LeaseToken: "secret", FencingToken: 9}
	port := &taskRunClaimPortStub{result: ClaimTaskRunResult{Run: &TaskRun{
		WorkspaceKey: "TEST", TaskRunID: "task-run-1", Status: StatusRunning, Owner: owner,
	}, ActionID: "claim-action-1"}}
	service, issuer := newTestService(t, Dependencies{TaskRuns: TaskRunDependencies{Claims: port}})
	result, err := service.ClaimTaskRun(context.Background(), issueSystem(t, issuer, ActionClaimTaskRun), ClaimTaskRunCommand{
		WorkspaceKey: "TEST", RequestID: "claim-1", TaskRunID: "task-run-1", NodeID: "node-1",
		LeaseID: "lease-1", LeaseToken: "secret", LeaseTTL: time.Minute, ClaimedAt: now,
	})
	if err != nil {
		t.Fatalf("ClaimTaskRun: %v", err)
	}
	if port.command.LeaseToken != "secret" || result.Run.Owner.LeaseToken != "" {
		t.Fatalf("command token=%q public owner=%+v", port.command.LeaseToken, result.Run.Owner)
	}
	wire, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(wire), "secret") || strings.Contains(string(wire), "LeaseToken") {
		t.Fatalf("claim result exposed credential: %s", wire)
	}
}

func TestClaimAndRequeueRequireAtomicLinkedDriverStepProjection(t *testing.T) {
	now := time.Now().UTC()
	nextEligibleAt := now.Add(time.Second)
	owner := Owner{ResourceKind: ResourceTaskRun, ResourceID: "task-run-1", NodeID: "node-1", LeaseID: "lease-1", LeaseToken: "secret", FencingToken: 9}
	claim := &taskRunClaimPortStub{result: ClaimTaskRunResult{
		Run:      &TaskRun{WorkspaceKey: "TEST", TaskRunID: "task-run-1", DriverRunID: "run-1", DriverStepID: "step-1", WorkItemID: "TASK-1", Status: StatusRunning, Owner: owner},
		Step:     &TaskRunDriverStep{WorkspaceKey: "TEST", StepID: "step-1", DriverRunID: "run-1", TaskRunID: "task-run-1", Status: "running"},
		ActionID: "claim-action-1",
	}}
	requeue := &taskRunRequeuePortStub{result: RequeueTaskRunResult{
		Run:  &TaskRun{WorkspaceKey: "TEST", TaskRunID: "task-run-1", DriverRunID: "run-1", DriverStepID: "step-1", WorkItemID: "TASK-1", Status: StatusQueued},
		Step: &TaskRunDriverStep{WorkspaceKey: "TEST", StepID: "step-1", DriverRunID: "run-1", TaskRunID: "task-run-1", Status: "queued"},
		Committed: &RequeueTaskRunCommit{
			WorkspaceKey: "TEST", TaskRunID: "task-run-1", DriverRunID: "run-1", DriverStepID: "step-1",
			WorkItemID: "TASK-1", TaskRunStatus: StatusQueued, DriverStepStatus: "queued",
			RequeuedAt: now, NextEligibleAt: nextEligibleAt,
		},
		ActionID: "requeue-action-1",
	}}
	service, issuer := newTestService(t, Dependencies{TaskRuns: TaskRunDependencies{Claims: claim, Requeues: requeue}})
	if _, err := service.ClaimTaskRun(context.Background(), issueSystem(t, issuer, ActionClaimTaskRun), ClaimTaskRunCommand{
		WorkspaceKey: "TEST", RequestID: "claim-1", TaskRunID: "task-run-1", NodeID: "node-1",
		LeaseID: "lease-1", LeaseToken: "secret", LeaseTTL: time.Minute, ClaimedAt: now,
	}); err != nil {
		t.Fatalf("ClaimTaskRun: %v", err)
	}
	if _, err := service.RequeueTaskRun(context.Background(), issueExecution(t, issuer, ActionRequeueTaskRun, owner), RequeueTaskRunCommand{
		WorkspaceKey: "TEST", RequestID: "requeue-1", Owner: owner, RequeuedAt: now, NextEligibleAt: nextEligibleAt,
	}); err != nil {
		t.Fatalf("RequeueTaskRun: %v", err)
	}
	claim.result.Step.Status = "queued"
	if _, err := service.ClaimTaskRun(context.Background(), issueSystem(t, issuer, ActionClaimTaskRun), ClaimTaskRunCommand{
		WorkspaceKey: "TEST", RequestID: "claim-2", TaskRunID: "task-run-1", NodeID: "node-1",
		LeaseID: "lease-1", LeaseToken: "secret", LeaseTTL: time.Minute, ClaimedAt: now,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("claim without running step error=%v, want conflict", err)
	}
	requeue.result.Step.Status = "running"
	if _, err := service.RequeueTaskRun(context.Background(), issueExecution(t, issuer, ActionRequeueTaskRun, owner), RequeueTaskRunCommand{
		WorkspaceKey: "TEST", RequestID: "requeue-2", Owner: owner, RequeuedAt: now,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("requeue without queued step error=%v, want conflict", err)
	}
}

func TestRequeueTaskRunReplayAcceptsLaterReclaimWithCommittedProof(t *testing.T) {
	now := time.Now().UTC()
	nextEligibleAt := now.Add(time.Second)
	owner := Owner{ResourceKind: ResourceTaskRun, ResourceID: "task-run-1", NodeID: "node-1", LeaseID: "lease-1", LeaseToken: "secret", FencingToken: 9}
	command := RequeueTaskRunCommand{
		WorkspaceKey: "TEST", RequestID: "requeue-1", Owner: owner,
		RuntimeMetadata: map[string]string{"scheduler_attempt": "1"}, LogsRef: "logs://1",
		ErrorClass: "agent_failed", ErrorMessage: "retrying", RequeuedAt: now, NextEligibleAt: nextEligibleAt,
	}
	port := &taskRunRequeuePortStub{result: RequeueTaskRunResult{
		Run: &TaskRun{
			WorkspaceKey: "TEST", TaskRunID: "task-run-1", DriverRunID: "run-1", DriverStepID: "step-1",
			WorkItemID: "TASK-1", Status: StatusRunning,
			Owner: Owner{ResourceKind: ResourceTaskRun, ResourceID: "task-run-1", NodeID: "node-2", LeaseID: "lease-2", FencingToken: 10},
		},
		Step: &TaskRunDriverStep{
			WorkspaceKey: "TEST", StepID: "step-1", DriverRunID: "run-1", TaskRunID: "task-run-1", Status: "running",
		},
		Committed: &RequeueTaskRunCommit{
			WorkspaceKey: "TEST", TaskRunID: "task-run-1", DriverRunID: "run-1", DriverStepID: "step-1",
			WorkItemID: "TASK-1", TaskRunStatus: StatusQueued, DriverStepStatus: "queued",
			RuntimeMetadata: map[string]string{"scheduler_attempt": "1"}, LogsRef: "logs://1",
			ErrorClass: "agent_failed", ErrorMessage: "retrying", RequeuedAt: now, NextEligibleAt: nextEligibleAt,
		},
		ActionID: "requeue-action", Replay: true,
	}}
	service, issuer := newTestService(t, Dependencies{TaskRuns: TaskRunDependencies{Requeues: port}})
	result, err := service.RequeueTaskRun(context.Background(), issueExecution(t, issuer, ActionRequeueTaskRun, owner), command)
	if err != nil || result.Run.Status != StatusRunning || result.Run.Owner.LeaseToken != "" {
		t.Fatalf("reclaimed replay result=%+v err=%v", result, err)
	}
	retryCommand := command
	retryCommand.RequeuedAt = now.Add(time.Minute)
	if _, err := service.RequeueTaskRun(context.Background(), issueExecution(t, issuer, ActionRequeueTaskRun, owner), retryCommand); err != nil {
		t.Fatalf("requeue replay with regenerated wall clock: %v", err)
	}
	port.result.Run.Status = StatusFailed
	port.result.Run.Owner = Owner{}
	if _, err := service.RequeueTaskRun(context.Background(), issueExecution(t, issuer, ActionRequeueTaskRun, owner), command); err != nil {
		t.Fatalf("terminal replay with lagging step: %v", err)
	}
	port.result.Step.Status = "completed"
	if _, err := service.RequeueTaskRun(context.Background(), issueExecution(t, issuer, ActionRequeueTaskRun, owner), command); !errors.Is(err, ErrConflict) {
		t.Fatalf("contradictory replay projection error=%v, want conflict", err)
	}
	port.result.Step.Status = "failed"
	port.result.Committed.NextEligibleAt = nextEligibleAt.Add(time.Second)
	if _, err := service.RequeueTaskRun(context.Background(), issueExecution(t, issuer, ActionRequeueTaskRun, owner), command); !errors.Is(err, ErrConflict) {
		t.Fatalf("divergent committed requeue error=%v, want conflict", err)
	}
}

func TestExhaustTaskRunRetriesAcceptsCertifiedBlockedOrPreservedWorkItemOutcome(t *testing.T) {
	now := time.Now().UTC()
	owner := Owner{ResourceKind: ResourceTaskRun, ResourceID: "task-run-1", NodeID: "node-1", LeaseID: "lease-1", LeaseToken: "secret", FencingToken: 9}
	command := ExhaustTaskRunRetriesCommand{
		WorkspaceKey: "TEST", RequestID: "exhaust-1", Owner: owner, Attempt: 3, MaxAttempts: 3,
		ErrorClass: "agent_failed", ErrorMessage: "retry budget exhausted", FinishedAt: now,
	}
	port := &taskRunRetryExhaustionPortStub{result: ExhaustTaskRunRetriesResult{
		Run:        &TaskRun{WorkspaceKey: "TEST", TaskRunID: "task-run-1", WorkItemID: "TASK-1", Status: StatusFailed, Owner: owner},
		WorkItemID: "TASK-1", WorkItemBlocked: true,
		Committed: &ExhaustTaskRunRetriesCommit{
			WorkspaceKey: "TEST", TaskRunID: "task-run-1", WorkItemID: "TASK-1",
			TaskRunStatus: StatusFailed, WorkItemBlocked: true, Attempt: command.Attempt,
			MaxAttempts: command.MaxAttempts, ErrorClass: command.ErrorClass,
			ErrorMessage: command.ErrorMessage, FinishedAt: command.FinishedAt,
		},
		ActionID: "exhaust-action-1",
	}}
	service, issuer := newTestService(t, Dependencies{TaskRuns: TaskRunDependencies{RetryExhaustion: port}})
	result, err := service.ExhaustTaskRunRetries(context.Background(), issueExecution(t, issuer, ActionExhaustTaskRunRetries, owner), command)
	if err != nil {
		t.Fatalf("ExhaustTaskRunRetries: %v", err)
	}
	if !result.WorkItemBlocked || result.WorkItemID != "TASK-1" || result.Run.Owner.LeaseToken != "" || port.command.Owner.LeaseToken != "secret" {
		t.Fatalf("result=%+v command=%+v", result, port.command)
	}

	port.result.WorkItemBlocked = false
	_, err = service.ExhaustTaskRunRetries(context.Background(), issueExecution(t, issuer, ActionExhaustTaskRunRetries, owner), command)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("TaskRun-only terminal result error = %v, want conflict", err)
	}
	port.result.Replay = true
	retryCommand := command
	retryCommand.FinishedAt = now.Add(time.Minute)
	if _, err = service.ExhaustTaskRunRetries(context.Background(), issueExecution(t, issuer, ActionExhaustTaskRunRetries, owner), retryCommand); err != nil {
		t.Fatalf("replay after Work Item movement: %v", err)
	}
	port.result.Replay = false
	port.result.Committed.WorkItemBlocked = false
	if result, err = service.ExhaustTaskRunRetries(context.Background(), issueExecution(t, issuer, ActionExhaustTaskRunRetries, owner), command); err != nil {
		t.Fatalf("fresh preserved-successor outcome: %v", err)
	}
	if result.WorkItemBlocked || result.Committed.WorkItemBlocked {
		t.Fatalf("preserved result=%+v, want current and committed block false", result)
	}
}

func TestExhaustTaskRunRetriesRejectsDivergentCommittedIdentity(t *testing.T) {
	now := time.Now().UTC()
	owner := Owner{ResourceKind: ResourceTaskRun, ResourceID: "task-run-1", NodeID: "node-1", LeaseID: "lease-1", LeaseToken: "secret", FencingToken: 9}
	command := ExhaustTaskRunRetriesCommand{
		WorkspaceKey: "TEST", RequestID: "exhaust-1", Owner: owner, Attempt: 3, MaxAttempts: 3,
		ErrorClass: "agent_failed", ErrorMessage: "retry budget exhausted", FinishedAt: now,
	}
	baseResult := ExhaustTaskRunRetriesResult{
		Run:        &TaskRun{WorkspaceKey: "TEST", TaskRunID: "task-run-1", WorkItemID: "TASK-1", Status: StatusFailed},
		WorkItemID: "TASK-1", WorkItemBlocked: false,
		Committed: &ExhaustTaskRunRetriesCommit{
			WorkspaceKey: "TEST", TaskRunID: "task-run-1", WorkItemID: "TASK-1",
			TaskRunStatus: StatusFailed, WorkItemBlocked: false, Attempt: 3, MaxAttempts: 3,
			ErrorClass: "agent_failed", ErrorMessage: "retry budget exhausted", FinishedAt: now,
		},
		ActionID: "exhaust-action-1",
	}
	for _, test := range []struct {
		name string
		edit func(*ExhaustTaskRunRetriesResult)
	}{
		{
			name: "public work item differs from terminal run",
			edit: func(result *ExhaustTaskRunRetriesResult) {
				result.WorkItemID = "TASK-2"
			},
		},
		{
			name: "committed work item differs from terminal run",
			edit: func(result *ExhaustTaskRunRetriesResult) {
				result.Committed.WorkItemID = "TASK-2"
			},
		},
		{
			name: "commit belongs to another task run",
			edit: func(result *ExhaustTaskRunRetriesResult) {
				result.Committed.TaskRunID = "task-run-2"
			},
		},
		{
			name: "commit reports nonterminal task run",
			edit: func(result *ExhaustTaskRunRetriesResult) {
				result.Committed.TaskRunStatus = StatusRunning
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := cloneExhaustTaskRunRetriesResultForTest(baseResult)
			test.edit(&result)
			port := &taskRunRetryExhaustionPortStub{result: result}
			service, issuer := newTestService(t, Dependencies{TaskRuns: TaskRunDependencies{RetryExhaustion: port}})
			_, err := service.ExhaustTaskRunRetries(
				context.Background(), issueExecution(t, issuer, ActionExhaustTaskRunRetries, owner), command,
			)
			if !errors.Is(err, ErrConflict) {
				t.Fatalf("error = %v, want ErrConflict", err)
			}
		})
	}
}

func cloneExhaustTaskRunRetriesResultForTest(result ExhaustTaskRunRetriesResult) ExhaustTaskRunRetriesResult {
	copy := result
	if result.Run != nil {
		run := *result.Run
		copy.Run = &run
	}
	if result.Committed != nil {
		committed := *result.Committed
		copy.Committed = &committed
	}
	return copy
}

func TestExhaustTaskRunRetriesCanonicalizesArtifactsAndRejectsNonFiniteUsage(t *testing.T) {
	now := time.Now().UTC()
	owner := Owner{ResourceKind: ResourceTaskRun, ResourceID: "task-run-1", NodeID: "node-1", LeaseID: "lease-1", LeaseToken: "secret", FencingToken: 9}
	originalIDs := []string{"artifact-b", " artifact-a ", "artifact-b"}
	command := ExhaustTaskRunRetriesCommand{
		WorkspaceKey: "TEST", RequestID: "exhaust-1", Owner: owner, Attempt: 2, MaxAttempts: 2,
		RequiredArtifactIDs: originalIDs, ErrorClass: "agent_failed", ErrorMessage: "exhausted", FinishedAt: now,
	}
	port := &taskRunRetryExhaustionPortStub{result: ExhaustTaskRunRetriesResult{
		Run:        &TaskRun{WorkspaceKey: "TEST", TaskRunID: "task-run-1", WorkItemID: "TASK-1", Status: StatusFailed},
		WorkItemID: "TASK-1", WorkItemBlocked: true,
		Committed: &ExhaustTaskRunRetriesCommit{
			WorkspaceKey: "TEST", TaskRunID: "task-run-1", WorkItemID: "TASK-1", TaskRunStatus: StatusFailed,
			WorkItemBlocked: true, Attempt: 2, MaxAttempts: 2,
			RequiredArtifactIDs: []string{"artifact-a", "artifact-b"}, ErrorClass: "agent_failed",
			ErrorMessage: "exhausted", FinishedAt: now,
		},
		ActionID: "exhaust-action",
	}}
	service, issuer := newTestService(t, Dependencies{TaskRuns: TaskRunDependencies{RetryExhaustion: port}})
	auth := issueExecution(t, issuer, ActionExhaustTaskRunRetries, owner)
	if _, err := service.ExhaustTaskRunRetries(context.Background(), auth, command); err != nil {
		t.Fatalf("canonical exhaustion: %v", err)
	}
	if !slices.Equal(port.command.RequiredArtifactIDs, []string{"artifact-a", "artifact-b"}) ||
		!slices.Equal(originalIDs, []string{"artifact-b", " artifact-a ", "artifact-b"}) {
		t.Fatalf("canonical command=%v caller=%v", port.command.RequiredArtifactIDs, originalIDs)
	}
	for _, cost := range []float64{math.NaN(), math.Inf(1)} {
		invalid := command
		invalid.EstimatedCostUSD = cost
		if _, err := service.ExhaustTaskRunRetries(context.Background(), auth, invalid); !errors.Is(err, ErrInvalid) {
			t.Fatalf("non-finite cost %v error=%v, want invalid", cost, err)
		}
	}
}

type convergenceSourceStub struct {
	record *TerminalTaskRunRecord
}

func (stub *convergenceSourceStub) GetTerminalTaskRun(_ context.Context, _, _ string) (*TerminalTaskRunRecord, error) {
	copy := *stub.record
	return &copy, nil
}

func (stub *convergenceSourceStub) ListTaskRunConvergenceCandidates(_ context.Context, _ TaskRunConvergenceCandidateQuery) (TaskRunConvergenceCandidatePage, error) {
	return TaskRunConvergenceCandidatePage{TaskRunIDs: []string{stub.record.TaskRunID}}, nil
}

type convergenceEventPortStub struct {
	writes map[string]TaskRunTerminalEvent
	calls  int
}

func (stub *convergenceEventPortStub) EnsureTaskRunTerminalEvent(_ context.Context, event TaskRunTerminalEvent) error {
	stub.calls++
	if stub.writes == nil {
		stub.writes = map[string]TaskRunTerminalEvent{}
	}
	stub.writes[event.EventID] = event
	return nil
}

type convergenceStepPortStub struct {
	failures int
	writes   map[string]DriverStepTerminalProjection
}

func (stub *convergenceStepPortStub) RepairTerminalDriverStep(_ context.Context, step DriverStepTerminalProjection) (RepairTerminalDriverStepResult, error) {
	if stub.failures > 0 {
		stub.failures--
		return RepairTerminalDriverStepResult{}, errors.New("injected step write failure")
	}
	if stub.writes == nil {
		stub.writes = map[string]DriverStepTerminalProjection{}
	}
	stub.writes[step.StepID] = step
	return RepairTerminalDriverStepResult{
		WorkspaceKey: step.WorkspaceKey, StepID: step.StepID, DriverRunID: step.DriverRunID,
		TaskRunID: step.TaskRunID, Status: step.Status, OutputRef: step.OutputRef,
	}, nil
}

type convergenceLeadStub struct{}

func (convergenceLeadStub) ResolveEpicLead(context.Context, string, string) (string, error) {
	return "lead", nil
}

type convergenceNotificationPortStub struct {
	writes map[string]LeadTaskNotification
}

func (stub *convergenceNotificationPortStub) EnsureLeadTaskNotification(_ context.Context, notification LeadTaskNotification) error {
	if stub.writes == nil {
		stub.writes = map[string]LeadTaskNotification{}
	}
	stub.writes[notification.DedupeKey] = notification
	return nil
}

type convergenceAuthorityResolverStub struct {
	issuer *authority.Issuer
}

func (stub convergenceAuthorityResolverStub) ResolveExecutionSystemAuthority(_ context.Context, workspace string, action authority.Action, componentID string) (authority.SystemAuthority, error) {
	principal, err := stub.issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject: componentID, Class: authority.ClassSystem, Workspace: workspace,
		Actions: []authority.Action{action}, ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		return authority.SystemAuthority{}, err
	}
	return stub.issuer.IssueSystem(principal, workspace, action, "test convergence runtime")
}

func TestTaskRunConvergenceRepairsLostStepAndNotificationAfterRestart(t *testing.T) {
	now := time.Now().UTC()
	source := &convergenceSourceStub{record: &TerminalTaskRunRecord{
		WorkspaceKey: "TEST", TaskRunID: "task-run-1", DriverRunID: "run-1", DriverStepID: "step-1",
		WorkItemID: "TASK-1", EpicID: "EPIC-1", Status: StatusSucceeded, Attempt: 1,
		LogsRef: "logs://1", ArtifactsRef: "artifacts://1", FinishedAt: now,
		ParentOwner: Owner{ResourceKind: ResourceDriverRun, ResourceID: "run-1", NodeID: "node-1", LeaseID: "lease-1", LeaseToken: "driver-token-1", FencingToken: 3},
	}}
	events := &convergenceEventPortStub{}
	steps := &convergenceStepPortStub{failures: 1}
	notifications := &convergenceNotificationPortStub{}
	service, issuer := newTestService(t, Dependencies{Convergence: TaskRunConvergenceDependencies{
		Source: source, Events: events, DriverSteps: steps, LeadResolver: convergenceLeadStub{}, Notifications: notifications,
	}})
	newPass := func() *TaskRunConvergencePass {
		return &TaskRunConvergencePass{
			WorkspaceKey: "TEST", Source: source, API: service,
			Authorities: convergenceAuthorityResolverStub{issuer: issuer},
		}
	}
	if err := newPass().RunOnce(context.Background()); err == nil {
		t.Fatal("first pass succeeded, want injected step failure")
	}
	if len(events.writes) != 1 || len(steps.writes) != 0 || len(notifications.writes) != 0 {
		t.Fatalf("after fault events=%v steps=%v notifications=%v", events.writes, steps.writes, notifications.writes)
	}
	// Reconstruct the runtime pass to model a serve restart. The durable
	// terminal record is scanned again and every already-written leg replays.
	if err := newPass().RunOnce(context.Background()); err != nil {
		t.Fatalf("restart convergence: %v", err)
	}
	if len(events.writes) != 1 || len(steps.writes) != 1 || len(notifications.writes) != 1 {
		t.Fatalf("after restart events=%v steps=%v notifications=%v", events.writes, steps.writes, notifications.writes)
	}
	for _, event := range events.writes {
		wire, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(wire), "lease") || strings.Contains(string(wire), "token") {
			t.Fatalf("terminal event exposed credential-shaped field: %s", wire)
		}
	}
}

func TestTerminalDriverStepProjectionMapsCancelledToSkipped(t *testing.T) {
	projection := terminalDriverStepProjection("converge-1", &TerminalTaskRunRecord{
		WorkspaceKey: "TEST", TaskRunID: "task-run-1", DriverRunID: "run-1", DriverStepID: "step-1",
		Status: StatusCancelled, LogsRef: "logs://cancelled",
	})
	if projection.Status != "skipped" || projection.OutputRef != "logs://cancelled" {
		t.Fatalf("projection=%+v", projection)
	}
}

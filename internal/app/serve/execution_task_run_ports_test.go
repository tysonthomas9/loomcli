package serve

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/execution"

	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"

	workspaceowner "github.com/tysonthomas9/loomcli/internal/modules/workspace"

	"github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
)

type atomicTaskRunPortStub struct{}

func (*atomicTaskRunPortStub) ReplayTaskRunRequest(context.Context, execution.RequestTaskRunCommand) (execution.RequestTaskRunResult, error) {
	return execution.RequestTaskRunResult{}, execution.ErrUnavailable
}

func (*atomicTaskRunPortStub) RequestTaskRun(context.Context, execution.RequestTaskRunCommand) (execution.RequestTaskRunResult, error) {
	return execution.RequestTaskRunResult{}, execution.ErrUnavailable
}

func (*atomicTaskRunPortStub) ClaimTaskRun(context.Context, execution.ClaimTaskRunCommand) (execution.ClaimTaskRunResult, error) {
	return execution.ClaimTaskRunResult{}, execution.ErrUnavailable
}

func (*atomicTaskRunPortStub) UpdateTaskRunWorkItemDesign(context.Context, execution.UpdateTaskRunWorkItemDesignCommand) (execution.UpdateTaskRunWorkItemDesignResult, error) {
	return execution.UpdateTaskRunWorkItemDesignResult{}, execution.ErrUnavailable
}

func (*atomicTaskRunPortStub) RequeueTaskRun(context.Context, execution.RequeueTaskRunCommand) (execution.RequeueTaskRunResult, error) {
	return execution.RequeueTaskRunResult{}, execution.ErrUnavailable
}

func (*atomicTaskRunPortStub) ExhaustTaskRunRetries(context.Context, execution.ExhaustTaskRunRetriesCommand) (execution.ExhaustTaskRunRetriesResult, error) {
	return execution.ExhaustTaskRunRetriesResult{}, execution.ErrUnavailable
}

func TestExecutionTaskRunPortsRequireAtomicCommandsWithoutStoreFallback(t *testing.T) {
	st := memstore.New()
	commands := &atomicTaskRunPortStub{}
	taskRuns, _, err := NewExecutionTaskRunPorts(ExecutionTaskRunPortDependencies{
		TaskRuns:      st.TaskRuns(),
		TaskRunEvents: st.TaskRunEvents(),
		Requests:      commands, Claims: commands, WorkItemDesign: commands, Requeues: commands, RetryExhaustion: commands,
		Nodes: st.Nodes(), WorkerProfiles: st.WorkerProfiles(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if taskRuns.Requests != commands || taskRuns.Claims != commands || taskRuns.WorkItemDesign != commands || taskRuns.Requeues != commands || taskRuns.RetryExhaustion != commands {
		t.Fatalf("TaskRun command ports were not retained exactly: %+v", taskRuns)
	}
}

func TestExecutionWorkerNodePortsPreserveDrainAndOwnSchedulingReads(t *testing.T) {
	ctx := context.Background()
	st, _ := setupExecutionTaskRunParent(t, ctx)
	commands := &atomicTaskRunPortStub{}
	_, workers, err := NewExecutionTaskRunPorts(ExecutionTaskRunPortDependencies{
		TaskRuns:      st.TaskRuns(),
		TaskRunEvents: st.TaskRunEvents(),
		Requests:      commands, Claims: commands, WorkItemDesign: commands, Requeues: commands, RetryExhaustion: commands,
		Nodes: st.Nodes(), WorkerProfiles: st.WorkerProfiles(),
	})
	if err != nil {
		t.Fatal(err)
	}
	command := execution.RegisterWorkerNodeCommand{
		WorkspaceKey: "WS", RequestID: "register-node-1", NodeID: "worker-node-1", OwnerActor: "loom",
		RuntimeProvider: "local", Labels: []string{"task-worker"}, Capabilities: []string{"local-noop", "repo"},
		ToolInventory: []string{"loom"}, TTL: time.Minute, RegisteredAt: time.Now().UTC(),
	}
	if _, err := workers.Registration.RegisterWorkerNode(ctx, command); err != nil {
		t.Fatal(err)
	}
	if _, err := workers.Drain.SetWorkerNodeDrain(ctx, execution.SetWorkerNodeDrainCommand{
		WorkspaceKey: "WS", RequestID: "drain-node-1", NodeID: command.NodeID,
		DrainState: execution.WorkerNodeDraining, ChangedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	// Registration refreshes inventory but must not silently reactivate a
	// deliberately draining worker.
	node, err := workers.Registration.RegisterWorkerNode(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	if node.DrainState != execution.WorkerNodeDraining {
		t.Fatalf("registration reset drain state to %q", node.DrainState)
	}
	if _, err := workers.Heartbeats.HeartbeatWorkerNode(ctx, execution.HeartbeatWorkerNodeCommand{
		WorkspaceKey: "WS", RequestID: "heartbeat-node-1", NodeID: command.NodeID,
		TTL: time.Minute, HeartbeatAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	adapter := workers.Registration.(*executionTaskRunPortsAdapter)
	scheduling, err := adapter.CheckTaskRunScheduling(ctx, execution.TaskRunSchedulingQuery{
		WorkspaceKey: "WS", TargetNodeID: command.NodeID, ProviderProfile: "local-noop", RequiredFeatures: []string{"repo"},
	})
	if err != nil || scheduling.Schedulable || scheduling.ReasonCode != "no_live_capable_node" {
		t.Fatalf("draining scheduling=%+v err=%v", scheduling, err)
	}
	if _, err := workers.Drain.SetWorkerNodeDrain(ctx, execution.SetWorkerNodeDrainCommand{
		WorkspaceKey: "WS", RequestID: "activate-node-1", NodeID: command.NodeID,
		DrainState: execution.WorkerNodeActive, ChangedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	scheduling, err = adapter.CheckTaskRunScheduling(ctx, execution.TaskRunSchedulingQuery{
		WorkspaceKey: "WS", TargetNodeID: command.NodeID, ProviderProfile: "local-noop", RequiredFeatures: []string{"repo"},
	})
	if err != nil || !scheduling.Schedulable {
		t.Fatalf("active scheduling=%+v err=%v", scheduling, err)
	}
}

type fleetExecutionTransportStub struct {
	claim                 fleetdb.ExecutionClaimAndStartCommand
	workItemDesign        fleetdb.ExecutionTaskRunWorkItemDesignCommand
	workItemDesignResult  *fleetdb.ExecutionTaskRunWorkItemDesignResult
	workItemDesignErr     error
	workItemClaim         fleetdb.ExecutionDriverRunWorkItemClaimCommand
	workItemRelease       fleetdb.ExecutionDriverRunWorkItemReleaseCommand
	workItemHandoff       fleetdb.ExecutionDriverRunReviewWorkItemHandoffCommand
	workItemClaimResult   *fleetdb.ExecutionDriverRunWorkItemResult
	workItemReleaseResult *fleetdb.ExecutionDriverRunWorkItemResult
	workItemHandoffResult *fleetdb.ExecutionDriverRunWorkItemResult
	terminalWork          fleetdb.ExecutionTerminalDriverRunWorkRecoveryCommand
}

func (*fleetExecutionTransportStub) RequestTaskRun(context.Context, fleetdb.ExecutionTaskRunRequestCommand) (*fleetdb.ExecutionTaskRunRequestResult, error) {
	return nil, nil
}

func (stub *fleetExecutionTransportStub) UpdateTaskRunWorkItemDesign(_ context.Context, command fleetdb.ExecutionTaskRunWorkItemDesignCommand) (*fleetdb.ExecutionTaskRunWorkItemDesignResult, error) {
	stub.workItemDesign = command
	return stub.workItemDesignResult, stub.workItemDesignErr
}

func (stub *fleetExecutionTransportStub) ClaimAndStartTaskRun(_ context.Context, command fleetdb.ExecutionClaimAndStartCommand) (*fleetdb.ExecutionClaimAndStartResult, error) {
	stub.claim = command
	taskRunID := command.TaskRunID
	if taskRunID == "" {
		taskRunID = "task-run-next"
	}
	return &fleetdb.ExecutionClaimAndStartResult{
		TaskRun: &execution.TaskRunRecord{
			WorkspaceKey: command.WorkspaceKey, TaskRunID: taskRunID, DriverRunID: "run-1", DriverStepID: "step-1",
			TaskID: "TASK-1", Status: execution.TaskRunRecordRunning, NodeID: command.NodeID, LeaseID: command.LeaseID, FencingToken: 7,
		},
		DriverStep: &execution.DriverStepRecord{
			WorkspaceKey: command.WorkspaceKey, StepID: "step-1", DriverRunID: "run-1",
			TaskRunID: taskRunID, Status: execution.DriverStepRunning, ActionLedgerID: "task-run-start:" + command.CommandID,
		},
		Issue: &fleetdb.ExecutionIssue{ID: "TASK-1"},
		Action: &fleetdb.ExecutionActionLedger{
			WorkspaceKey: command.WorkspaceKey, ActionID: "task-run-start:" + command.CommandID,
			ActionType: "start_task_run", IdempotencyKey: "task-run-start:" + command.CommandID, TargetRef: taskRunID,
		},
		Replayed: true,
	}, nil
}

func (*fleetExecutionTransportStub) HeartbeatTaskRun(context.Context, string, string, execution.TaskRunHeartbeat) (*execution.TaskRunRecord, error) {
	return nil, nil
}

func (*fleetExecutionTransportStub) RequeueTaskRun(context.Context, string, string, execution.TaskRunRequeue) (*execution.TaskRunRecord, error) {
	return nil, nil
}

func (*fleetExecutionTransportStub) CompleteTaskRun(context.Context, string, string, execution.TaskRunComplete) (*execution.TaskRunRecord, error) {
	return nil, nil
}

func (*fleetExecutionTransportStub) AppendTaskRunLog(context.Context, string, string, execution.TaskRunLogAppend) (*execution.TaskRunLogEntry, error) {
	return nil, nil
}

func (*fleetExecutionTransportStub) RequeueTaskRunAndResetStep(context.Context, fleetdb.ExecutionTaskRunRequeueCommand) (*fleetdb.ExecutionTaskRunRequeueResult, error) {
	return nil, nil
}

func (*fleetExecutionTransportStub) ExhaustTaskRunRetries(context.Context, fleetdb.ExecutionTaskRunRetryExhaustionCommand) (*fleetdb.ExecutionTaskRunRetryExhaustionResult, error) {
	return nil, nil
}

func (*fleetExecutionTransportStub) ClaimDriverRun(context.Context, fleetdb.ExecutionDriverRunClaimCommand) (*execution.DriverRunRecord, error) {
	return nil, nil
}

func (*fleetExecutionTransportStub) HeartbeatDriverRun(context.Context, fleetdb.ExecutionDriverRunHeartbeatCommand) (*execution.DriverRunRecord, error) {
	return nil, nil
}

func (stub *fleetExecutionTransportStub) ClaimDriverRunWorkItem(_ context.Context, command fleetdb.ExecutionDriverRunWorkItemClaimCommand) (*fleetdb.ExecutionDriverRunWorkItemResult, error) {
	stub.workItemClaim = command
	return stub.workItemClaimResult, nil
}

func (stub *fleetExecutionTransportStub) ReleaseDriverRunWorkItem(_ context.Context, command fleetdb.ExecutionDriverRunWorkItemReleaseCommand) (*fleetdb.ExecutionDriverRunWorkItemResult, error) {
	stub.workItemRelease = command
	return stub.workItemReleaseResult, nil
}

func (stub *fleetExecutionTransportStub) HandoffDriverRunReviewWorkItem(_ context.Context, command fleetdb.ExecutionDriverRunReviewWorkItemHandoffCommand) (*fleetdb.ExecutionDriverRunWorkItemResult, error) {
	stub.workItemHandoff = command
	return stub.workItemHandoffResult, nil
}

func (*fleetExecutionTransportStub) SuspendDriverRun(context.Context, fleetdb.ExecutionDriverRunSuspendCommand) (*execution.DriverRunRecord, error) {
	return nil, nil
}

func (*fleetExecutionTransportStub) FinalizeDriverRun(context.Context, fleetdb.ExecutionDriverRunFinalizeCommand) (*execution.DriverRunRecord, error) {
	return nil, nil
}

func (*fleetExecutionTransportStub) RecoverStaleChildTaskRuns(context.Context, fleetdb.ExecutionDriverRunStaleTaskRecoveryCommand) (*fleetdb.ExecutionDriverRunStaleTaskRecoveryResult, error) {
	return nil, nil
}

func (*fleetExecutionTransportStub) StartChildDriverRun(context.Context, fleetdb.ExecutionDriverRunChildStartCommand) (*fleetdb.ExecutionDriverRunChildStartResult, error) {
	return nil, nil
}

func (*fleetExecutionTransportStub) CascadeChildDriverRuns(context.Context, fleetdb.ExecutionDriverRunCascadeCommand) (*fleetdb.ExecutionDriverRunCascadeResult, error) {
	return nil, nil
}

func (stub *fleetExecutionTransportStub) RecoverTerminalDriverRunWork(
	_ context.Context,
	command fleetdb.ExecutionTerminalDriverRunWorkRecoveryCommand,
) (*fleetdb.ExecutionTerminalDriverRunWorkRecoveryResult, error) {
	stub.terminalWork = command
	appliedAt := command.RecoveredAt
	return &fleetdb.ExecutionTerminalDriverRunWorkRecoveryResult{
		WorkspaceKey: command.WorkspaceKey, DriverRunID: command.DriverRunID,
		ParentStatus: command.ParentStatus, Reason: command.Reason, ErrorClass: command.ErrorClass,
		RecoveredAt: command.RecoveredAt, RecoveredTaskRunIDs: []string{}, ReleasedWorkItemIDs: []string{},
		PreservedSuccessorWorkItemIDs: []string{}, ActionID: command.RequestID,
		Action: &fleetdb.ExecutionActionLedger{
			WorkspaceKey: command.WorkspaceKey, ActionID: command.RequestID,
			ActionType: "recover_terminal_driver_run_work", Status: "applied",
			CreatedAt: command.RecoveredAt, AppliedAt: &appliedAt,
		},
	}, nil
}

func TestFleetTaskRunWorkItemDesignPortForwardsOnlyOwnerCommand(t *testing.T) {
	transport := &fleetExecutionTransportStub{workItemDesignResult: &fleetdb.ExecutionTaskRunWorkItemDesignResult{
		Committed: fleetdb.ExecutionTaskRunWorkItemDesignCommit{TaskID: "TASK-1"},
		Action:    &fleetdb.ExecutionActionLedger{ActionID: "task-run-work-item-design-update:design-1"},
		Replayed:  true,
	}}
	_, _, port, _, _, err := NewFleetTaskRunCommandPorts(transport)
	if err != nil {
		t.Fatal(err)
	}
	design := "# Plan"
	format := "markdown"
	owner := execution.Owner{
		ResourceKind: execution.ResourceTaskRun, ResourceID: "task-run-1", NodeID: "node-1",
		LeaseID: "lease-1", LeaseToken: "raw-task-secret", FencingToken: 7,
	}
	result, err := port.UpdateTaskRunWorkItemDesign(context.Background(), execution.UpdateTaskRunWorkItemDesignCommand{
		WorkspaceKey: "WS", RequestID: "design-1", Owner: owner, Design: &design, DesignFormat: &format,
	})
	if err != nil {
		t.Fatalf("UpdateTaskRunWorkItemDesign: %v", err)
	}
	if result.WorkItemID != "TASK-1" || result.ActionID != "task-run-work-item-design-update:design-1" || !result.Replay {
		t.Fatalf("result = %+v", result)
	}
	command := transport.workItemDesign
	if command.WorkspaceKey != "WS" || command.CommandID != "design-1" || command.TaskRunID != owner.ResourceID ||
		command.NodeID != owner.NodeID || command.LeaseID != owner.LeaseID || command.LeaseToken != owner.LeaseToken ||
		command.FencingToken != owner.FencingToken || command.Design != design || command.DesignFormat == nil || *command.DesignFormat != format {
		t.Fatalf("transport command = %+v", command)
	}
}

func TestFleetTaskRunClaimPortUsesAtomicTransportAndRetainsTokenOnlyInternally(t *testing.T) {
	transport := &fleetExecutionTransportStub{}
	port, err := NewFleetTaskRunClaimPort(transport)
	if err != nil {
		t.Fatal(err)
	}
	result, err := port.ClaimTaskRun(context.Background(), execution.ClaimTaskRunCommand{
		WorkspaceKey: "WS", RequestID: "claim-1", TaskRunID: "task-run-1", NodeID: "node-1",
		LeaseID: "lease-1", LeaseToken: "secret", LeaseTTL: time.Minute, ClaimedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if transport.claim.LeaseToken != "secret" || result.Run.Owner.LeaseToken != "secret" ||
		result.Step == nil || result.Step.TaskRunID != "task-run-1" || !result.Replay {
		t.Fatalf("transport=%+v result=%+v", transport.claim, result)
	}
}

func TestFleetTaskRunClaimPortPreservesClaimNextAndLinkedStep(t *testing.T) {
	transport := &fleetExecutionTransportStub{}
	port, err := NewFleetTaskRunClaimPort(transport)
	if err != nil {
		t.Fatal(err)
	}
	result, err := port.ClaimTaskRun(context.Background(), execution.ClaimTaskRunCommand{
		WorkspaceKey: "WS", RequestID: "claim-next-1", NodeID: "node-1",
		LeaseID: "lease-1", LeaseToken: "secret", LeaseTTL: time.Minute, ClaimedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if transport.claim.TaskRunID != "" || result.Run == nil || result.Run.TaskRunID != "task-run-next" ||
		result.Step == nil || result.Step.TaskRunID != result.Run.TaskRunID || result.Step.Status != "running" {
		t.Fatalf("transport=%+v result=%+v", transport.claim, result)
	}
}

func TestFleetDriverRunWorkItemPortForwardsOpaqueOwnerAndClaimAction(t *testing.T) {
	now := time.Now().UTC()
	appliedAt := now
	claimRequestID := execution.ClaimDriverRunWorkItemRequestID("run-1", "TASK-1")
	claimActionID := execution.DriverRunWorkItemClaimActionID(claimRequestID)
	transport := &fleetExecutionTransportStub{workItemClaimResult: &fleetdb.ExecutionDriverRunWorkItemResult{
		Issue: &fleetdb.ExecutionIssue{Workspace: "WS", ID: "TASK-1", Status: "in_progress", Assignee: "driver-run:run-1", UpdatedAt: now},
		Action: &fleetdb.ExecutionActionLedger{
			WorkspaceKey: "WS", ActionID: claimActionID, IdempotencyKey: claimActionID,
			ActionType: "claim_work_item", TargetRef: "TASK-1", RequestedBy: "driver-run:run-1", Status: "applied",
			CreatedAt: now, AppliedAt: &appliedAt,
		},
	}}
	port, err := newFleetDriverRunCommandPort(transport)
	if err != nil {
		t.Fatal(err)
	}
	owner := execution.Owner{
		ResourceKind: execution.ResourceDriverRun, ResourceID: "run-1", NodeID: "node-1",
		LeaseID: "lease-1", LeaseToken: "raw-secret", FencingToken: 7,
	}
	result, err := port.ClaimDriverRunWorkItem(context.Background(), execution.ClaimDriverRunWorkItemCommand{
		WorkspaceKey: "WS", RequestID: claimRequestID, Owner: owner, WorkItemID: "TASK-1", ClaimTTL: time.Minute, ClaimedAt: now,
	})
	if err != nil || result.Action == nil || result.Action.ActionID != claimActionID {
		t.Fatalf("claim result=%+v err=%v", result, err)
	}
	if transport.workItemClaim.LeaseToken != "raw-secret" || transport.workItemClaim.FencingToken != 7 ||
		transport.workItemClaim.RunID != "run-1" || transport.workItemClaim.TaskID != "TASK-1" {
		t.Fatalf("claim transport command=%+v", transport.workItemClaim)
	}

	releaseAt := now.Add(time.Minute)
	transport.workItemReleaseResult = &fleetdb.ExecutionDriverRunWorkItemResult{
		Issue:  &fleetdb.ExecutionIssue{Workspace: "WS", ID: "TASK-1", Status: "open", UpdatedAt: releaseAt},
		Action: &fleetdb.ExecutionActionLedger{WorkspaceKey: "WS", ActionID: "release-action"},
	}
	releaseRequestID := execution.ReleaseDriverRunWorkItemRequestID("run-1", "TASK-1")
	if _, err := port.ReleaseDriverRunWorkItem(context.Background(), execution.ReleaseDriverRunWorkItemCommand{
		WorkspaceKey: "WS", RequestID: releaseRequestID, Owner: owner, WorkItemID: "TASK-1",
		ClaimActionID: claimActionID, ReleasedAt: releaseAt,
	}); err != nil {
		t.Fatal(err)
	}
	if transport.workItemRelease.LeaseToken != "raw-secret" || transport.workItemRelease.ClaimActionID != claimActionID ||
		transport.workItemRelease.CommandID != releaseRequestID {
		t.Fatalf("release transport command=%+v", transport.workItemRelease)
	}

	handoffAt := releaseAt.Add(time.Minute)
	taskRunID := "review-child-1"
	handoffRequestID := execution.HandoffDriverRunReviewWorkItemRequestID("run-1", "TASK-1", taskRunID)
	transport.workItemHandoffResult = &fleetdb.ExecutionDriverRunWorkItemResult{
		Issue: &fleetdb.ExecutionIssue{
			Workspace: "WS", ID: "TASK-1", Status: "closed", Assignee: "", UpdatedAt: handoffAt,
		},
		Action: &fleetdb.ExecutionActionLedger{
			WorkspaceKey: "WS", ActionID: execution.DriverRunReviewWorkItemHandoffActionID(handoffRequestID),
		},
	}
	if _, err := port.HandoffDriverRunReviewWorkItem(context.Background(), execution.HandoffDriverRunReviewWorkItemCommand{
		WorkspaceKey: "WS", RequestID: handoffRequestID, Owner: owner, WorkItemID: "TASK-1",
		ClaimActionID: claimActionID, TaskRunID: taskRunID, TargetStatus: "closed",
		Reason: "approved", HandedOffAt: handoffAt,
	}); err != nil {
		t.Fatal(err)
	}
	if transport.workItemHandoff.LeaseToken != "raw-secret" ||
		transport.workItemHandoff.ClaimActionID != claimActionID ||
		transport.workItemHandoff.TaskRunID != taskRunID ||
		transport.workItemHandoff.CommandID != handoffRequestID ||
		transport.workItemHandoff.TargetStatus != "closed" {
		t.Fatalf("handoff transport command=%+v", transport.workItemHandoff)
	}

	reviewTaskRunID := "triage-child-1"
	reviewRequestID := execution.HandoffDriverRunReviewWorkItemRequestID("run-1", "TASK-1", reviewTaskRunID)
	priority := 4
	externalRef := "local-branch:loom/TASK-1@" + strings.Repeat("a", 40)
	transport.workItemHandoffResult = &fleetdb.ExecutionDriverRunWorkItemResult{
		Issue: &fleetdb.ExecutionIssue{
			Workspace: "WS", ID: "TASK-1", Status: "review", Priority: priority,
			Labels: []string{"existing", "bug", "triaged"}, Assignee: "",
			ExternalRef: externalRef, UpdatedAt: handoffAt,
		},
		Action: &fleetdb.ExecutionActionLedger{
			WorkspaceKey: "WS", ActionID: execution.DriverRunReviewWorkItemHandoffActionID(reviewRequestID),
		},
		Comment: &fleetdb.ExecutionWorkItemComment{
			ID: "17", IssueID: "TASK-1", Author: "driver-run:run-1",
			Body: "Automated bug triage completed.", CreatedAt: handoffAt,
		},
	}
	result, err = port.HandoffDriverRunReviewWorkItem(context.Background(), execution.HandoffDriverRunReviewWorkItemCommand{
		WorkspaceKey: "WS", RequestID: reviewRequestID, Owner: owner, WorkItemID: "TASK-1",
		ClaimActionID: claimActionID, TaskRunID: reviewTaskRunID, TargetStatus: "review",
		Priority: &priority, Labels: []string{"bug", "triaged"},
		CommentBody: "Automated bug triage completed.", ExternalRef: &externalRef,
		HandedOffAt: handoffAt,
	})
	if err != nil || result.WorkItem == nil || result.WorkItem.Priority != priority ||
		result.Comment == nil || result.Comment.CommentID != "17" ||
		result.Comment.WorkItemID != "TASK-1" || result.Comment.Author != "driver-run:run-1" ||
		result.Comment.Body != "Automated bug triage completed." ||
		!result.Comment.CreatedAt.Equal(handoffAt) ||
		result.WorkItem.ExternalRef != externalRef ||
		!slices.Equal(result.WorkItem.Labels, []string{"existing", "bug", "triaged"}) {
		t.Fatalf("review handoff result=%+v err=%v", result, err)
	}
	if transport.workItemHandoff.Priority == nil || *transport.workItemHandoff.Priority != priority ||
		!slices.Equal(transport.workItemHandoff.Labels, []string{"bug", "triaged"}) ||
		transport.workItemHandoff.CommentBody != "Automated bug triage completed." ||
		transport.workItemHandoff.ExternalRef == nil ||
		*transport.workItemHandoff.ExternalRef != externalRef {
		t.Fatalf("review handoff transport command=%+v", transport.workItemHandoff)
	}
}

func TestFleetDriverRunPortForwardsTerminalWorkRecoveryEnvelope(t *testing.T) {
	recoveredAt := time.Date(2026, 7, 18, 12, 30, 0, 0, time.UTC)
	transport := &fleetExecutionTransportStub{}
	port, err := newFleetDriverRunCommandPort(transport)
	if err != nil {
		t.Fatal(err)
	}
	command := execution.RecoverTerminalDriverRunWorkCommand{
		WorkspaceKey: "WS", DriverRunID: "run-1", ParentStatus: execution.DriverRunFailed,
		Reason: "parent driver run became failed", ErrorClass: "parent_run_terminal", RecoveredAt: recoveredAt,
	}
	command.RequestID = execution.RecoverTerminalDriverRunWorkRequestID(command.DriverRunID, command.ParentStatus)
	result, err := port.RecoverTerminalDriverRunWork(t.Context(), command)
	if err != nil {
		t.Fatal(err)
	}
	if transport.terminalWork.RequestID != command.RequestID || transport.terminalWork.DriverRunID != command.DriverRunID ||
		transport.terminalWork.ParentStatus != execution.DriverRunFailed || !transport.terminalWork.RecoveredAt.Equal(recoveredAt) {
		t.Fatalf("terminal work transport command = %+v", transport.terminalWork)
	}
	if result.Committed == nil || result.ActionID != command.RequestID || result.Committed.DriverRunID != command.DriverRunID ||
		!result.Committed.RecoveredAt.Equal(recoveredAt) {
		t.Fatalf("terminal work result = %+v", result)
	}
}

func TestExecutionTaskRunSnapshotPreservesQueuedTargetNodeWithoutInventingOwner(t *testing.T) {
	snapshot, err := executionTaskRunSnapshot(&execution.TaskRunRecord{
		WorkspaceKey: "WS", TaskRunID: "task-run-targeted", DriverRunID: "run-1",
		TaskID: "TASK-1", Status: execution.TaskRunRecordQueued, TargetNodeID: "target-node-1",
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.TargetNodeID != "target-node-1" || snapshot.Owner != (execution.Owner{}) {
		t.Fatalf("queued snapshot target=%q owner=%+v", snapshot.TargetNodeID, snapshot.Owner)
	}
}

func setupExecutionTaskRunParent(t *testing.T, ctx context.Context) (*memstore.Store, *execution.DriverRunRecord) {
	t.Helper()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, workspaceowner.WorkspaceCreate{Key: "WS", Name: "workspace"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Drivers().Create(ctx, workflowcatalog.DriverCreate{
		WorkspaceKey: "WS", DriverID: "driver-1", Name: "driver", OwnerType: workflowcatalog.DriverOwnerSystem, Status: workflowcatalog.DriverStatusActive,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DriverVersions().Create(ctx, workflowcatalog.DriverVersionCreate{
		WorkspaceKey: "WS", VersionID: "version-1", DriverID: "driver-1", Version: 1,
		SourceDigest: "sha256:source", BundleDigest: "sha256:bundle", ValidationStatus: workflowcatalog.DriverVersionValidationPassed,
	}); err != nil {
		t.Fatal(err)
	}
	parent, err := st.DriverRuns().Create(ctx, execution.DriverRunCreate{
		WorkspaceKey: "WS", RunID: "run-1", DriverID: "driver-1", DriverVersionID: "version-1",
		SourceKind: "manual", SourceRef: "test", IdempotencyKey: "run-request-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	parent, err = st.DriverRuns().Claim(ctx, "WS", parent.RunID, "driver-node", "driver-lease")
	if err != nil {
		t.Fatal(err)
	}
	return st, parent
}

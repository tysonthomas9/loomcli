package driver

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// taskWorkerTestExecution is deliberately test-only. Production TaskWorker
// tests inject the same typed APIs as serve; this adapter keeps their compact
// memstore fixtures without restoring a Store fallback to TaskWorker itself.
type taskWorkerTestExecution struct {
	execution.DriverRunAPI
	store    store.Store
	outcomes RunOutcomePublisher
}

type taskWorkerTestAuthorities struct{}

type lostResponseTaskWorkerExecution struct {
	execution.TaskRunWorkerAPI
	commands     []execution.ClaimTaskRunCommand
	committed    execution.ClaimTaskRunResult
	sameEnvelope bool
}

func (adapter *lostResponseTaskWorkerExecution) ClaimTaskRun(ctx context.Context, auth authority.SystemAuthority, command execution.ClaimTaskRunCommand) (execution.ClaimTaskRunResult, error) {
	command = cloneTaskWorkerClaimCommand(command)
	adapter.commands = append(adapter.commands, command)
	if len(adapter.commands) == 1 {
		result, err := adapter.TaskRunWorkerAPI.ClaimTaskRun(ctx, auth, command)
		if err != nil {
			return execution.ClaimTaskRunResult{}, err
		}
		adapter.committed = result
		return execution.ClaimTaskRunResult{}, execution.ErrUnavailable
	}
	adapter.sameEnvelope = reflect.DeepEqual(adapter.commands[0], command)
	result := adapter.committed
	result.Replay = true
	return result, nil
}

type concurrentClaimTaskWorkerExecution struct {
	execution.TaskRunWorkerAPI
	entered chan struct{}
	release chan struct{}
	mu      sync.Mutex
	active  int
	max     int
}

func (adapter *concurrentClaimTaskWorkerExecution) ClaimTaskRun(context.Context, authority.SystemAuthority, execution.ClaimTaskRunCommand) (execution.ClaimTaskRunResult, error) {
	adapter.mu.Lock()
	adapter.active++
	if adapter.active > adapter.max {
		adapter.max = adapter.active
	}
	adapter.mu.Unlock()
	adapter.entered <- struct{}{}
	<-adapter.release
	adapter.mu.Lock()
	adapter.active--
	adapter.mu.Unlock()
	return execution.ClaimTaskRunResult{}, execution.ErrUnavailable
}

func (adapter *concurrentClaimTaskWorkerExecution) maxActive() int {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	return adapter.max
}

func (taskWorkerTestAuthorities) ResolveTaskRunAuthority(_ context.Context, workspace string, action authority.Action, owner execution.Owner) (authority.ExecutionAuthority, error) {
	principal, err := bridgeArtifactTestIssuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject: "task-run:" + owner.ResourceID, Class: authority.ClassExecution,
		Workspace: workspace, Actions: []authority.Action{action}, ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		return authority.ExecutionAuthority{}, err
	}
	return bridgeArtifactTestIssuer.IssueExecutionForOwner(principal, workspace, action, authority.ExecutionOwner{
		ResourceKind: authority.ExecutionResourceTaskRun, ResourceID: owner.ResourceID,
		NodeID: owner.NodeID, LeaseID: owner.LeaseID, FencingToken: owner.FencingToken,
	})
}

func (taskWorkerTestAuthorities) ResolveExecutionSystemAuthority(context.Context, string, authority.Action, string) (authority.SystemAuthority, error) {
	return authority.SystemAuthority{}, nil
}

func (taskWorkerTestAuthorities) ResolveDriverRunAuthority(context.Context, string, authority.Action, execution.Owner) (authority.ExecutionAuthority, error) {
	return authority.ExecutionAuthority{}, nil
}

type taskWorkerTestStore interface {
	store.Store
	bridgeArtifactFixtureStore
}

func wireTaskWorkerTestExecution(worker *TaskWorker, st taskWorkerTestStore) {
	adapter := taskWorkerTestExecution{store: st}
	worker.Execution = adapter
	worker.TaskRunAuthorities = taskWorkerTestAuthorities{}
	worker.ExecutionAuthorities = taskWorkerTestAuthorities{}
	worker.ExecutionComponentID = "execution-task-run-worker-1"
	worker.Convergence = adapter
	worker.Artifacts = testArtifactsAPI(st)
}

func wireExecutorTestExecution(executor *Executor, st store.Store) {
	adapter := taskWorkerTestExecution{store: st, outcomes: executor.RunOutcomes}
	outbox, ok := st.DriverRuns().(store.DriverRunOutcomeStore)
	if !ok {
		panic("test DriverRun store lacks durable outcomes")
	}
	queue, systemAuthorities, err := testRunOutcomeQueue(outbox)
	if err != nil {
		panic(err)
	}
	executor.Execution = adapter
	executor.RunOutcomeQueue = queue
	executor.TerminalWorkRecoveryQueue = noOpTerminalDriverRunWorkRecoveryQueue{}
	executor.ExecutionWorkers = adapter
	executor.ExecutionAuthorities = taskWorkerTestAuthorities{}
	executor.SystemAuthorities = systemAuthorities
}

func testExecutor(st store.Store, value Executor) *Executor {
	executor := &value
	if len(executor.RunTokenKey) == 0 {
		executor.RunTokenKey = bytes.Repeat([]byte{0x42}, 32)
	}
	wireExecutorTestExecution(executor, st)
	return executor
}

func (adapter taskWorkerTestExecution) ClaimDriverRun(ctx context.Context, _ authority.SystemAuthority, command execution.ClaimDriverRunCommand) (*execution.DriverRun, error) {
	run, err := adapter.store.DriverRuns().Claim(ctx, command.WorkspaceKey, command.RunID, command.NodeID, command.LeaseID)
	return testExecutionDriverRunSnapshot(run), err
}

func (adapter taskWorkerTestExecution) HeartbeatDriverRun(ctx context.Context, _ authority.ExecutionAuthority, command execution.DriverRunHeartbeatCommand) (*execution.DriverRun, error) {
	run, err := adapter.store.DriverRuns().Heartbeat(ctx, command.WorkspaceKey, command.Owner.ResourceID, command.Owner.NodeID, command.Owner.LeaseID, command.Owner.FencingToken)
	return testExecutionDriverRunSnapshot(run), err
}

func (adapter taskWorkerTestExecution) FinalizeDriverRun(ctx context.Context, _ authority.ExecutionAuthority, command execution.FinalizeDriverRunCommand) (*execution.DriverRun, error) {
	run, err := adapter.store.DriverRuns().Finish(ctx, command.WorkspaceKey, command.Owner.ResourceID, store.DriverRunFinish{
		NodeID: command.Owner.NodeID, LeaseID: command.Owner.LeaseID, FencingToken: command.Owner.FencingToken,
		Status: domain.DriverRunStatus(command.Status), Summary: command.Summary, ErrorClass: command.ErrorClass,
		Output: cloneStringMap(command.Output),
	})
	return testExecutionDriverRunSnapshot(run), err
}

func (adapter taskWorkerTestExecution) CascadeChildDriverRuns(
	ctx context.Context,
	_ authority.ExecutionAuthority,
	command execution.CascadeChildDriverRunsCommand,
) (execution.CascadeChildDriverRunsResult, error) {
	parent, err := adapter.store.DriverRuns().Get(ctx, command.WorkspaceKey, command.ParentRunID)
	if err != nil {
		return execution.CascadeChildDriverRunsResult{}, err
	}
	// Test-only compatibility: production uses Execution's single atomic
	// recursive cascade port. Legacy composition fixtures still exercise their
	// historical store shape behind this fake.
	cascadeCancelChildren(ctx, adapter.store, adapter.outcomes, parent, 0)
	return execution.CascadeChildDriverRunsResult{
		Committed: &execution.CascadeChildDriverRunsCommit{
			WorkspaceKey: command.WorkspaceKey, ParentRunID: command.ParentRunID,
			ParentStatus: command.ParentStatus, Reason: command.Reason, ErrorClass: command.ErrorClass,
			CascadedAt: command.CascadedAt, MaxDepth: command.MaxDepth,
		},
		ActionID: command.RequestID,
	}, nil
}

func (taskWorkerTestExecution) RecoverTerminalDriverRunWork(
	_ context.Context,
	_ authority.SystemAuthority,
	command execution.RecoverTerminalDriverRunWorkCommand,
) (execution.RecoverTerminalDriverRunWorkResult, error) {
	return execution.RecoverTerminalDriverRunWorkResult{ActionID: command.RequestID}, nil
}

func (adapter taskWorkerTestExecution) RecoverChildDriverRunCascade(
	ctx context.Context,
	_ authority.SystemAuthority,
	command execution.RecoverChildDriverRunCascadeCommand,
) (execution.CascadeChildDriverRunsResult, error) {
	parent, err := adapter.store.DriverRuns().Get(ctx, command.WorkspaceKey, command.ParentRunID)
	if err != nil {
		return execution.CascadeChildDriverRunsResult{}, err
	}
	cascadeCancelChildren(ctx, adapter.store, adapter.outcomes, parent, 0)
	return execution.CascadeChildDriverRunsResult{
		ActionID: command.RequestID,
		Committed: &execution.CascadeChildDriverRunsCommit{
			WorkspaceKey: command.WorkspaceKey, ParentRunID: command.ParentRunID,
			ParentStatus: command.ParentStatus, Reason: command.Reason,
			ErrorClass: command.ErrorClass, CascadedAt: command.CascadedAt,
			MaxDepth: command.MaxDepth,
		},
	}, nil
}

func (adapter taskWorkerTestExecution) RecoverDriverRuns(ctx context.Context, _ authority.SystemAuthority, command execution.RecoverDriverRunsCommand) (*execution.DriverRunRecoveryResult, error) {
	result, err := adapter.store.DriverRuns().RecoverStale(ctx, command.WorkspaceKey, store.StaleDriverRunRecovery{
		StaleBefore: command.ObservedAt.Add(-command.MaxAge), MaxAgeSeconds: int64(command.MaxAge / time.Second), ErrorClass: command.ErrorClass,
		Summary: command.Summary, Limit: command.Limit,
	})
	if err != nil {
		return nil, err
	}
	return &execution.DriverRunRecoveryResult{
		WorkspaceKey: result.WorkspaceKey, StaleBefore: result.StaleBefore, RecoveredAt: result.RecoveredAt,
		Recovered: result.Recovered, SkippedFresh: result.SkippedFresh,
		RecoveredRunIDs:    append([]string(nil), result.RecoveredRunIDs...),
		SkippedFreshRunIDs: append([]string(nil), result.SkippedFreshRunIDs...),
	}, nil
}

func (adapter taskWorkerTestExecution) ResolveDriverAwait(ctx context.Context, _ authority.SystemAuthority, command execution.ResolveDriverAwaitCommand) error {
	atomic, ok := adapter.store.Awaits().(store.AtomicAwaitStore)
	if !ok {
		return execution.ErrUnavailable
	}
	return atomic.ResolveAwaitAndResume(ctx, command.WorkspaceKey, command.InstanceKey, command.EventID, command.Payload, command.Actor)
}

func testExecutionDriverRunSnapshot(run *domain.DriverRun) *execution.DriverRun {
	if run == nil {
		return nil
	}
	return &execution.DriverRun{
		WorkspaceKey: run.WorkspaceKey, RunID: run.RunID, DriverID: run.DriverID, DriverVersionID: run.DriverVersionID,
		Entrypoint: run.Entrypoint, SourceKind: run.SourceKind, SourceRef: run.SourceRef, EpicID: run.EpicID,
		ParentRunID: run.ParentRunID, TriggerBindingID: run.TriggerBindingID, AgentServiceID: run.AgentServiceID,
		SubjectKey: run.SubjectKey, Status: execution.DriverRunStatus(run.Status),
		Owner:          execution.Owner{ResourceKind: execution.ResourceDriverRun, ResourceID: run.RunID, NodeID: run.NodeID, LeaseID: run.LeaseID, FencingToken: run.FencingToken},
		IdempotencyKey: run.IdempotencyKey, Payload: append([]byte(nil), run.Payload...), Output: cloneStringMap(run.Output),
		Summary: run.Summary, ErrorClass: run.ErrorClass, StartedAt: run.StartedAt, LastHeartbeat: run.LastHeartbeat,
		FinishedAt: run.FinishedAt, AwaitInstanceKey: run.AwaitInstanceKey, SuspendedAt: run.SuspendedAt,
		CancelRequestedAt: run.CancelRequestedAt, CancelRequestedReason: run.CancelRequestedReason,
		ResumeSourceEventID: run.ResumeSourceEventID, CreatedAt: run.CreatedAt, UpdatedAt: run.UpdatedAt,
	}
}

func (adapter taskWorkerTestExecution) ClaimTaskRun(ctx context.Context, _ authority.SystemAuthority, command execution.ClaimTaskRunCommand) (execution.ClaimTaskRunResult, error) {
	run, err := adapter.store.TaskRuns().ClaimQueued(ctx, command.WorkspaceKey, store.TaskRunClaim{
		TaskRunID: command.TaskRunID, NodeID: command.NodeID, RunnerID: command.RunnerID,
		LeaseID: command.LeaseID, LeaseToken: command.LeaseToken,
		SupportedProviders: command.SupportedProviders, Capabilities: command.Capabilities,
		WorkerProfileIDs: command.WorkerProfileIDs, RunnerPlacement: testLegacyPlacement(command.RunnerPlacement),
		SandboxPlacement: testLegacyPlacement(command.SandboxPlacement), ClaimedAt: command.ClaimedAt,
	})
	if err != nil {
		return execution.ClaimTaskRunResult{}, err
	}
	if err := adapter.projectStep(ctx, run, domain.DriverStepRunning); err != nil {
		return execution.ClaimTaskRunResult{}, err
	}
	return execution.ClaimTaskRunResult{Run: testExecutionTaskRun(run, command.LeaseToken), ActionID: command.RequestID}, nil
}

func (adapter taskWorkerTestExecution) Heartbeat(ctx context.Context, _ authority.ExecutionAuthority, command execution.HeartbeatCommand) (execution.HeartbeatResult, error) {
	run, err := adapter.store.TaskRuns().Heartbeat(ctx, command.WorkspaceKey, command.Owner.ResourceID, store.TaskRunHeartbeat{
		NodeID: command.Owner.NodeID, LeaseID: command.Owner.LeaseID, LeaseToken: command.Owner.LeaseToken,
		FencingToken: command.Owner.FencingToken, RuntimeMetadata: command.RuntimeMetadata, HeartbeatAt: command.At,
	})
	if err != nil {
		return execution.HeartbeatResult{}, err
	}
	return execution.HeartbeatResult{Owner: testTaskRunOwner(run, command.Owner.LeaseToken)}, nil
}

func (adapter taskWorkerTestExecution) RequeueTaskRun(ctx context.Context, _ authority.ExecutionAuthority, command execution.RequeueTaskRunCommand) (execution.RequeueTaskRunResult, error) {
	run, err := adapter.store.TaskRuns().Requeue(ctx, command.WorkspaceKey, command.Owner.ResourceID, store.TaskRunRequeue{
		NodeID: command.Owner.NodeID, LeaseID: command.Owner.LeaseID, LeaseToken: command.Owner.LeaseToken,
		FencingToken: command.Owner.FencingToken, RuntimeMetadata: command.RuntimeMetadata,
		LogsRef: command.LogsRef, ArtifactsRef: command.ArtifactsRef, ErrorClass: command.ErrorClass,
		ErrorMessage: command.ErrorMessage, RequeuedAt: command.RequeuedAt, NextEligibleAt: command.NextEligibleAt,
	})
	if err != nil {
		return execution.RequeueTaskRunResult{}, err
	}
	if err := adapter.projectStep(ctx, run, domain.DriverStepQueued); err != nil {
		return execution.RequeueTaskRunResult{}, err
	}
	return execution.RequeueTaskRunResult{
		Run: testExecutionTaskRun(run, ""), ActionID: command.RequestID,
		Committed: &execution.RequeueTaskRunCommit{
			WorkspaceKey: command.WorkspaceKey, TaskRunID: run.TaskRunID, DriverRunID: run.DriverRunID,
			DriverStepID: run.DriverStepID, WorkItemID: run.TaskID, TaskRunStatus: execution.StatusQueued,
			DriverStepStatus: string(domain.DriverStepQueued), RuntimeMetadata: cloneStringMap(command.RuntimeMetadata),
			LogsRef: command.LogsRef, ArtifactsRef: command.ArtifactsRef,
			ErrorClass: command.ErrorClass, ErrorMessage: command.ErrorMessage,
			RequeuedAt: command.RequeuedAt, NextEligibleAt: command.NextEligibleAt,
		},
	}, nil
}

func (adapter taskWorkerTestExecution) ExhaustTaskRunRetries(ctx context.Context, _ authority.ExecutionAuthority, command execution.ExhaustTaskRunRetriesCommand) (execution.ExhaustTaskRunRetriesResult, error) {
	run, err := adapter.store.TaskRuns().Finish(ctx, command.WorkspaceKey, command.Owner.ResourceID, store.TaskRunFinish{
		NodeID: command.Owner.NodeID, LeaseID: command.Owner.LeaseID, LeaseToken: command.Owner.LeaseToken,
		FencingToken: command.Owner.FencingToken, Status: domain.TaskRunFailed, ExitCode: command.ExitCode,
		LogsRef: command.LogsRef, ArtifactsRef: command.ArtifactsRef, InputTokens: command.InputTokens,
		OutputTokens: command.OutputTokens, CacheReadTokens: command.CacheReadTokens,
		CacheWriteTokens: command.CacheWriteTokens, EstimatedCostUSD: command.EstimatedCostUSD,
		RuntimeMetadata: command.RuntimeMetadata, ErrorClass: command.ErrorClass, ErrorMessage: command.ErrorMessage,
		FinishedAt: command.FinishedAt, BlockTask: true,
	})
	if err != nil {
		return execution.ExhaustTaskRunRetriesResult{}, err
	}
	if err := adapter.projectStep(ctx, run, domain.DriverStepFailed); err != nil {
		return execution.ExhaustTaskRunRetriesResult{}, err
	}
	return execution.ExhaustTaskRunRetriesResult{
		Run: testExecutionTaskRun(run, ""), WorkItemID: run.TaskID, WorkItemBlocked: true, ActionID: command.RequestID,
		Committed: &execution.ExhaustTaskRunRetriesCommit{
			WorkspaceKey: command.WorkspaceKey, TaskRunID: run.TaskRunID, WorkItemID: run.TaskID,
			TaskRunStatus: execution.StatusFailed, WorkItemBlocked: true, Attempt: command.Attempt,
		},
	}, nil
}

func (adapter taskWorkerTestExecution) Finalize(ctx context.Context, _ authority.ExecutionAuthority, command execution.FinalizeCommand) (execution.FinalizeResult, error) {
	status := domain.TaskRunCompleted
	if command.Classification.Status == execution.StatusCancelled {
		status = domain.TaskRunCancelled
	}
	run, err := adapter.store.TaskRuns().Complete(ctx, command.WorkspaceKey, command.Owner.ResourceID, store.TaskRunComplete{
		CompletionID: command.RequestID, NodeID: command.Owner.NodeID, LeaseID: command.Owner.LeaseID,
		LeaseToken: command.Owner.LeaseToken, FencingToken: command.Owner.FencingToken, Status: status,
		ExitCode: command.ExitCode, LogsRef: command.LogsRef, ArtifactsRef: command.ArtifactsRef,
		RequiredArtifactIDs: command.RequiredArtifactIDs, RequireArtifacts: command.RequireArtifacts,
		InputTokens: command.InputTokens, OutputTokens: command.OutputTokens,
		CacheReadTokens: command.CacheReadTokens, CacheWriteTokens: command.CacheWriteTokens,
		EstimatedCostUSD: command.EstimatedCostUSD, RuntimeMetadata: command.RuntimeMetadata,
		ErrorClass: command.Classification.ErrorClass, ErrorMessage: command.Classification.Summary,
		CloseTask: command.CloseWorkItem, CloseReason: command.CloseReason, FinishedAt: command.FinishedAt,
	})
	if err != nil {
		return execution.FinalizeResult{}, err
	}
	return execution.FinalizeResult{Owner: testTaskRunOwner(run, command.Owner.LeaseToken), Status: command.Classification.Status, FinishedAt: command.FinishedAt}, nil
}

func (adapter taskWorkerTestExecution) RegisterWorkerNode(ctx context.Context, _ authority.SystemAuthority, command execution.RegisterWorkerNodeCommand) (*execution.WorkerNode, error) {
	node, err := adapter.store.Nodes().Create(ctx, store.NodeCreate{
		WorkspaceKey: command.WorkspaceKey, NodeID: command.NodeID, OwnerActor: command.OwnerActor,
		RuntimeProvider: domain.RuntimeProvider(command.RuntimeProvider), Labels: command.Labels,
		Capabilities: command.Capabilities, ToolInventory: command.ToolInventory, Version: command.Version,
		Capacity: command.Capacity, DrainState: domain.NodeDrainActive, TTL: command.TTL,
	})
	if err != nil && errors.Is(err, domain.ErrAlreadyExists) {
		node, err = adapter.store.Nodes().Get(ctx, command.WorkspaceKey, command.NodeID)
	}
	return testExecutionWorkerNode(node), err
}

func (adapter taskWorkerTestExecution) HeartbeatWorkerNode(ctx context.Context, _ authority.SystemAuthority, command execution.HeartbeatWorkerNodeCommand) (*execution.WorkerNode, error) {
	node, err := adapter.store.Nodes().Heartbeat(ctx, command.WorkspaceKey, command.NodeID, command.TTL)
	return testExecutionWorkerNode(node), err
}

func (adapter taskWorkerTestExecution) SetWorkerNodeDrain(ctx context.Context, _ authority.SystemAuthority, command execution.SetWorkerNodeDrainCommand) (*execution.WorkerNode, error) {
	drain := domain.NodeDrainState(command.DrainState)
	node, err := adapter.store.Nodes().Update(ctx, command.WorkspaceKey, command.NodeID, store.NodeUpdate{DrainState: &drain})
	return testExecutionWorkerNode(node), err
}

func (adapter taskWorkerTestExecution) ConvergeTaskRun(ctx context.Context, _ authority.SystemAuthority, command execution.ConvergeTaskRunCommand) (execution.ConvergeTaskRunResult, error) {
	run, err := adapter.store.TaskRuns().Get(ctx, command.WorkspaceKey, command.TaskRunID)
	if err != nil {
		return execution.ConvergeTaskRunResult{}, err
	}
	if err := adapter.projectStep(ctx, run, driverStepStatusForTaskRun(run.Status)); err != nil {
		return execution.ConvergeTaskRunResult{}, err
	}
	return execution.ConvergeTaskRunResult{TaskRunID: run.TaskRunID, DriverStepEnsured: run.DriverStepID != ""}, nil
}

func (adapter taskWorkerTestExecution) RepairTerminalDriverStep(context.Context, authority.SystemAuthority, execution.DriverStepTerminalProjection) (execution.RepairTerminalDriverStepResult, error) {
	return execution.RepairTerminalDriverStepResult{}, execution.ErrUnavailable
}

func (adapter taskWorkerTestExecution) projectStep(ctx context.Context, run *domain.TaskRun, status domain.DriverStepStatus) error {
	if run == nil || run.DriverStepID == "" {
		return nil
	}
	parent, err := adapter.store.DriverRuns().Get(ctx, run.WorkspaceKey, run.DriverRunID)
	if err != nil {
		return err
	}
	output := firstNonEmpty(run.ArtifactsRef, run.LogsRef)
	_, err = adapter.store.DriverSteps().Update(ctx, run.WorkspaceKey, run.DriverStepID, store.DriverStepUpdate{
		Status: &status, TaskRunID: &run.TaskRunID, OutputRef: &output,
		NodeID: parent.NodeID, LeaseID: parent.LeaseID, FencingToken: parent.FencingToken,
	})
	return err
}

func testExecutionTaskRun(run *domain.TaskRun, leaseToken string) *execution.TaskRun {
	if run == nil {
		return nil
	}
	status := execution.Status(run.Status)
	if run.Status == domain.TaskRunCompleted {
		status = execution.StatusSucceeded
	}
	return &execution.TaskRun{
		WorkspaceKey: run.WorkspaceKey, TaskRunID: run.TaskRunID, DriverRunID: run.DriverRunID,
		DriverStepID: run.DriverStepID, WorkItemID: run.TaskID, WorkerProfileID: run.WorkerProfileID,
		Runner: run.Runner, RunnerRef: run.RunnerRef, RunnerKind: run.RunnerKind,
		RunnerEntrypoint: run.RunnerEntrypoint, RunnerVersionID: run.RunnerVersionID,
		ProviderProfile: run.ProviderProfile, TargetNodeID: run.TargetNodeID, Status: status, Owner: testTaskRunOwner(run, leaseToken),
		RunnerPlacement: executionTaskRunPlacement(run.RunnerPlacement), SandboxPlacement: executionTaskRunPlacement(run.SandboxPlacement),
		RuntimeMetadata: cloneStringMap(run.RuntimeMetadata), Input: append([]byte(nil), run.Input...),
		ExitCode: run.ExitCode, LogsRef: run.LogsRef, ArtifactsRef: run.ArtifactsRef,
		InputTokens: run.InputTokens, OutputTokens: run.OutputTokens, CacheReadTokens: run.CacheReadTokens,
		CacheWriteTokens: run.CacheWriteTokens, EstimatedCostUSD: run.EstimatedCostUSD,
		ErrorClass: run.ErrorClass, ErrorMessage: run.ErrorMessage, FinishedAt: run.FinishedAt,
		CreatedAt: run.CreatedAt, UpdatedAt: run.UpdatedAt,
	}
}

func testTaskRunOwner(run *domain.TaskRun, token string) execution.Owner {
	if run == nil || run.NodeID == "" {
		return execution.Owner{}
	}
	return execution.Owner{ResourceKind: execution.ResourceTaskRun, ResourceID: run.TaskRunID, NodeID: run.NodeID, LeaseID: run.LeaseID, LeaseToken: token, FencingToken: run.FencingToken}
}

func testLegacyPlacement(value execution.Placement) domain.TaskRunPlacement {
	return domain.TaskRunPlacement{Provider: value.Provider, NodeID: value.NodeID, RunnerID: value.RunnerID, SandboxID: value.SandboxID, CWD: value.CWD, RepoRef: value.RepoRef}
}

func testExecutionWorkerNode(node *domain.Node) *execution.WorkerNode {
	if node == nil {
		return nil
	}
	return &execution.WorkerNode{
		WorkspaceKey: node.WorkspaceKey, NodeID: node.NodeID, OwnerActor: node.OwnerActor,
		RuntimeProvider: string(node.RuntimeProvider), Labels: append([]string(nil), node.Labels...),
		Capabilities: append([]string(nil), node.Capabilities...), ToolInventory: append([]string(nil), node.ToolInventory...),
		Version: node.Version, Capacity: node.Capacity, DrainState: execution.WorkerNodeDrainState(node.DrainState),
		LastHeartbeat: node.LastHeartbeat, ExpiresAt: node.ExpiresAt, CreatedAt: node.CreatedAt, UpdatedAt: node.UpdatedAt,
	}
}

var _ execution.TaskRunWorkerAPI = taskWorkerTestExecution{}
var _ execution.TaskRunConvergenceAPI = taskWorkerTestExecution{}

// Keep time imported on older Go toolchains where Node fields may be elided
// by build constraints in downstream forks.
var _ = time.Time{}

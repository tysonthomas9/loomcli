package serve

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// ExecutionTaskRunPortDependencies separates the atomic TaskRun command
// ports from the worker registry/query stores. Production supplies every
// command port from FleetDB's Execution transport; this composition has no
// generic TaskRun/DriverStep/Event mutation fallback.
type ExecutionTaskRunPortDependencies struct {
	Requests        execution.TaskRunRequestPort
	Claims          execution.TaskRunClaimPort
	WorkItemDesign  execution.TaskRunWorkItemDesignPort
	Requeues        execution.TaskRunRequeuePort
	RetryExhaustion execution.TaskRunRetryExhaustionPort
	Nodes           store.NodeStore
	WorkerProfiles  store.WorkerProfileStore
}

func NewExecutionTaskRunPorts(dependencies ExecutionTaskRunPortDependencies) (execution.TaskRunDependencies, execution.WorkerDependencies, error) {
	if dependencies.Requests == nil || dependencies.Claims == nil || dependencies.WorkItemDesign == nil || dependencies.Requeues == nil ||
		dependencies.RetryExhaustion == nil || dependencies.Nodes == nil || dependencies.WorkerProfiles == nil {
		return execution.TaskRunDependencies{}, execution.WorkerDependencies{}, fmt.Errorf("compose TaskRun Execution ports: every narrow dependency is required")
	}
	adapter := &executionTaskRunPortsAdapter{dependencies: dependencies}
	return execution.TaskRunDependencies{
			Requests: dependencies.Requests, Claims: dependencies.Claims, WorkItemDesign: dependencies.WorkItemDesign,
			Requeues:        dependencies.Requeues,
			RetryExhaustion: dependencies.RetryExhaustion, Scheduling: adapter,
		}, execution.WorkerDependencies{
			Registration: adapter, Heartbeats: adapter, Drain: adapter, Profiles: adapter,
		}, nil
}

// NewFleetTaskRunCommandPorts binds every owner-sensitive TaskRun command to
// FleetDB's atomic Execution routes. All returned interfaces share one adapter
// and preserve the raw lease credential only for the outbound header.
func NewFleetTaskRunCommandPorts(transport fleetdb.ExecutionTransport) (
	execution.TaskRunRequestPort,
	execution.TaskRunClaimPort,
	execution.TaskRunWorkItemDesignPort,
	execution.TaskRunRequeuePort,
	execution.TaskRunRetryExhaustionPort,
	error,
) {
	if transport == nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("compose atomic TaskRun commands: Fleet Execution transport required")
	}
	adapter := &fleetTaskRunCommandPort{transport: transport}
	return adapter, adapter, adapter, adapter, adapter, nil
}

// NewFleetTaskRunClaimPort binds the only production TaskRun claim adapter to
// FleetDB's atomic Issue-claim + TaskRun-start command.
func NewFleetTaskRunClaimPort(transport fleetdb.ExecutionTransport) (execution.TaskRunClaimPort, error) {
	if transport == nil {
		return nil, fmt.Errorf("compose atomic TaskRun claim: Fleet Execution transport required")
	}
	return &fleetTaskRunCommandPort{transport: transport}, nil
}

type fleetTaskRunCommandPort struct {
	transport fleetdb.ExecutionTransport
}

func (adapter *fleetTaskRunCommandPort) UpdateTaskRunWorkItemDesign(
	ctx context.Context,
	command execution.UpdateTaskRunWorkItemDesignCommand,
) (execution.UpdateTaskRunWorkItemDesignResult, error) {
	design := ""
	if command.Design != nil {
		design = *command.Design
	}
	result, err := adapter.transport.UpdateTaskRunWorkItemDesign(ctx, fleetdb.ExecutionTaskRunWorkItemDesignCommand{
		WorkspaceKey: command.WorkspaceKey, CommandID: command.RequestID, TaskRunID: command.Owner.ResourceID,
		NodeID: command.Owner.NodeID, LeaseID: command.Owner.LeaseID, LeaseToken: command.Owner.LeaseToken,
		FencingToken: command.Owner.FencingToken, Design: design, DesignFormat: command.DesignFormat,
	})
	if err != nil {
		return execution.UpdateTaskRunWorkItemDesignResult{}, mapFleetExecutionPortError(err)
	}
	if result == nil || result.Action == nil || strings.TrimSpace(result.Committed.TaskID) == "" {
		return execution.UpdateTaskRunWorkItemDesignResult{}, execution.ErrConflict
	}
	return execution.UpdateTaskRunWorkItemDesignResult{
		WorkItemID: result.Committed.TaskID,
		ActionID:   result.Action.ActionID,
		Replay:     result.Replayed,
	}, nil
}

func (adapter *fleetTaskRunCommandPort) ClaimTaskRun(ctx context.Context, command execution.ClaimTaskRunCommand) (execution.ClaimTaskRunResult, error) {
	result, err := adapter.transport.ClaimAndStartTaskRun(ctx, fleetdb.ExecutionClaimAndStartCommand{
		WorkspaceKey: command.WorkspaceKey, CommandID: command.RequestID, TaskRunID: command.TaskRunID,
		NodeID: command.NodeID, RunnerID: command.RunnerID, LeaseID: command.LeaseID, LeaseToken: command.LeaseToken,
		ClaimTTL: command.LeaseTTL, SupportedProviders: append([]string(nil), command.SupportedProviders...),
		Capabilities: append([]string(nil), command.Capabilities...), WorkerProfileIDs: append([]string(nil), command.WorkerProfileIDs...),
		RunnerPlacement: legacyExecutionPlacement(command.RunnerPlacement), SandboxPlacement: legacyExecutionPlacement(command.SandboxPlacement),
	})
	if err != nil {
		return execution.ClaimTaskRunResult{}, mapFleetExecutionPortError(err)
	}
	if result == nil || result.TaskRun == nil || result.DriverStep == nil {
		return execution.ClaimTaskRunResult{}, execution.ErrConflict
	}
	run, err := executionTaskRunSnapshot(result.TaskRun, command.LeaseToken)
	if err != nil {
		return execution.ClaimTaskRunResult{}, err
	}
	actionID := ""
	if result.Action != nil {
		actionID = result.Action.ActionID
	}
	return execution.ClaimTaskRunResult{
		Run: run, Step: executionTaskRunDriverStepSnapshot(result.DriverStep),
		ActionID: actionID, Replay: result.Replayed,
	}, nil
}

func (adapter *fleetTaskRunCommandPort) ReplayTaskRunRequest(ctx context.Context, command execution.RequestTaskRunCommand) (execution.RequestTaskRunResult, error) {
	return adapter.requestTaskRun(ctx, command, true)
}

func (adapter *fleetTaskRunCommandPort) RequestTaskRun(ctx context.Context, command execution.RequestTaskRunCommand) (execution.RequestTaskRunResult, error) {
	return adapter.requestTaskRun(ctx, command, false)
}

func (adapter *fleetTaskRunCommandPort) requestTaskRun(ctx context.Context, command execution.RequestTaskRunCommand, replayOnly bool) (execution.RequestTaskRunResult, error) {
	result, err := adapter.transport.RequestTaskRun(ctx, fleetdb.ExecutionTaskRunRequestCommand{
		WorkspaceKey: command.WorkspaceKey, CommandID: command.RequestID, TaskRunID: command.TaskRunID,
		DriverRunID: command.DriverRunID, DriverStepID: command.DriverStepID, TaskID: command.WorkItemID,
		ClaimActionID: command.ClaimActionID,
		NodeID:        command.ParentOwner.NodeID, LeaseID: command.ParentOwner.LeaseID,
		LeaseToken: command.ParentOwner.LeaseToken, FencingToken: command.ParentOwner.FencingToken,
		WorkerProfileID: command.WorkerProfileID, Runner: command.Runner, RunnerRef: command.RunnerRef,
		RunnerKind: command.RunnerKind, RunnerEntrypoint: command.RunnerEntrypoint, RunnerVersionID: command.RunnerVersionID,
		ProviderProfile: command.ProviderProfile, TargetNodeID: command.TargetNodeID,
		RequiredCapabilities: append([]string(nil), command.RequiredCapabilities...),
		RunnerPlacement:      legacyExecutionPlacement(command.RunnerPlacement), SandboxPlacement: legacyExecutionPlacement(command.SandboxPlacement),
		RuntimeMetadata: cloneExecutionStringMap(command.RuntimeMetadata), Input: append([]byte(nil), command.Input...),
		RequestedAt: command.RequestedAt, ReplayOnly: replayOnly,
	})
	if err != nil {
		if replayOnly && errors.Is(err, fleetdb.ErrExecutionNotFound) {
			return execution.RequestTaskRunResult{}, execution.ErrTaskRunRequestReplayNotFound
		}
		return execution.RequestTaskRunResult{}, mapFleetExecutionPortError(err)
	}
	if result == nil || result.TaskRun == nil || result.DriverStep == nil || result.Action == nil {
		return execution.RequestTaskRunResult{}, execution.ErrConflict
	}
	run, err := executionTaskRunSnapshot(result.TaskRun, "")
	if err != nil {
		return execution.RequestTaskRunResult{}, err
	}
	return execution.RequestTaskRunResult{
		Run: run, Step: executionTaskRunDriverStepSnapshot(result.DriverStep),
		ActionID: result.Action.ActionID, ClaimActionID: result.ClaimActionID, Replay: result.Replayed,
	}, nil
}

func (adapter *fleetTaskRunCommandPort) RequeueTaskRun(ctx context.Context, command execution.RequeueTaskRunCommand) (execution.RequeueTaskRunResult, error) {
	result, err := adapter.transport.RequeueTaskRunAndResetStep(ctx, fleetdb.ExecutionTaskRunRequeueCommand{
		WorkspaceKey: command.WorkspaceKey, CommandID: command.RequestID, TaskRunID: command.Owner.ResourceID,
		NodeID: command.Owner.NodeID, LeaseID: command.Owner.LeaseID, LeaseToken: command.Owner.LeaseToken,
		FencingToken: command.Owner.FencingToken, RuntimeMetadata: cloneExecutionStringMap(command.RuntimeMetadata),
		LogsRef: command.LogsRef, ArtifactsRef: command.ArtifactsRef, ErrorClass: command.ErrorClass,
		ErrorMessage: command.ErrorMessage, RequeuedAt: command.RequeuedAt, NextEligibleAt: command.NextEligibleAt,
	})
	if err != nil {
		return execution.RequeueTaskRunResult{}, mapFleetExecutionPortError(err)
	}
	if result == nil || result.TaskRun == nil || result.DriverStep == nil || result.Action == nil {
		return execution.RequeueTaskRunResult{}, execution.ErrConflict
	}
	run, err := executionTaskRunSnapshot(result.TaskRun, "")
	if err != nil {
		return execution.RequeueTaskRunResult{}, err
	}
	return execution.RequeueTaskRunResult{
		Run: run, Step: executionTaskRunDriverStepSnapshot(result.DriverStep), ActionID: result.Action.ActionID,
		Replay: result.Replayed,
		Committed: &execution.RequeueTaskRunCommit{
			WorkspaceKey: result.Committed.WorkspaceKey, TaskRunID: result.Committed.TaskRunID,
			DriverRunID: result.Committed.DriverRunID, DriverStepID: result.Committed.DriverStepID,
			WorkItemID: result.Committed.TaskID, TaskRunStatus: execution.Status(result.Committed.Status),
			DriverStepStatus: string(result.Committed.DriverStepStatus),
			RuntimeMetadata:  cloneExecutionStringMap(result.Committed.RuntimeMetadata), LogsRef: result.Committed.LogsRef,
			ArtifactsRef: result.Committed.ArtifactsRef, ErrorClass: result.Committed.ErrorClass,
			ErrorMessage: result.Committed.ErrorMessage, RequeuedAt: result.Committed.RequeuedAt,
			NextEligibleAt: result.Committed.NextEligibleAt,
		},
	}, nil
}

func (adapter *fleetTaskRunCommandPort) ExhaustTaskRunRetries(ctx context.Context, command execution.ExhaustTaskRunRetriesCommand) (execution.ExhaustTaskRunRetriesResult, error) {
	result, err := adapter.transport.ExhaustTaskRunRetries(ctx, fleetdb.ExecutionTaskRunRetryExhaustionCommand{
		WorkspaceKey: command.WorkspaceKey, CommandID: command.RequestID, TaskRunID: command.Owner.ResourceID,
		NodeID: command.Owner.NodeID, LeaseID: command.Owner.LeaseID, LeaseToken: command.Owner.LeaseToken,
		FencingToken: command.Owner.FencingToken, Attempt: command.Attempt, MaxAttempts: command.MaxAttempts,
		ExitCode: command.ExitCode, LogsRef: command.LogsRef, ArtifactsRef: command.ArtifactsRef,
		RequiredArtifactIDs: append([]string(nil), command.RequiredArtifactIDs...), RequireArtifacts: command.RequireArtifacts,
		InputTokens: command.InputTokens, OutputTokens: command.OutputTokens, CacheReadTokens: command.CacheReadTokens,
		CacheWriteTokens: command.CacheWriteTokens, EstimatedCostUSD: command.EstimatedCostUSD,
		RuntimeMetadata: cloneExecutionStringMap(command.RuntimeMetadata), ErrorClass: command.ErrorClass,
		ErrorMessage: command.ErrorMessage, FinishedAt: command.FinishedAt,
	})
	if err != nil {
		return execution.ExhaustTaskRunRetriesResult{}, mapFleetExecutionPortError(err)
	}
	if result == nil || result.TaskRun == nil || result.Action == nil {
		return execution.ExhaustTaskRunRetriesResult{}, execution.ErrConflict
	}
	run, err := executionTaskRunSnapshot(result.TaskRun, "")
	if err != nil {
		return execution.ExhaustTaskRunRetriesResult{}, err
	}
	workItemID := strings.TrimSpace(result.Committed.TaskID)
	if workItemID == "" || workItemID != run.WorkItemID || (result.Issue != nil && result.Issue.ID != workItemID) {
		return execution.ExhaustTaskRunRetriesResult{}, execution.ErrConflict
	}
	currentWorkItemBlocked := result.Issue != nil && strings.EqualFold(result.Issue.Status, "blocked")
	return execution.ExhaustTaskRunRetriesResult{
		Run: run, WorkItemID: workItemID, WorkItemBlocked: currentWorkItemBlocked,
		ActionID: result.Action.ActionID, Replay: result.Replayed,
		Committed: &execution.ExhaustTaskRunRetriesCommit{
			WorkspaceKey: result.Committed.WorkspaceKey, TaskRunID: result.Committed.TaskRunID,
			WorkItemID: result.Committed.TaskID, TaskRunStatus: execution.Status(result.Committed.Status),
			WorkItemBlocked: result.Committed.IssueBlocked, Attempt: result.Committed.Attempt,
			MaxAttempts: result.Committed.MaxAttempts, ExitCode: result.Committed.ExitCode,
			LogsRef: result.Committed.LogsRef, ArtifactsRef: result.Committed.ArtifactsRef,
			RequiredArtifactIDs: append([]string(nil), result.Committed.RequiredArtifactIDs...),
			RequireArtifacts:    result.Committed.RequireArtifacts, InputTokens: result.Committed.InputTokens,
			OutputTokens: result.Committed.OutputTokens, CacheReadTokens: result.Committed.CacheReadTokens,
			CacheWriteTokens: result.Committed.CacheWriteTokens, EstimatedCostUSD: result.Committed.EstimatedCostUSD,
			RuntimeMetadata: cloneExecutionStringMap(result.Committed.RuntimeMetadata), ErrorClass: result.Committed.ErrorClass,
			ErrorMessage: result.Committed.ErrorMessage, FinishedAt: result.Committed.FinishedAt,
		},
	}, nil
}

func mapFleetExecutionPortError(err error) error {
	switch {
	case errors.Is(err, fleetdb.ErrExecutionNotFound):
		return errors.Join(execution.ErrNotFound, err)
	case errors.Is(err, fleetdb.ErrExecutionInvalid):
		return errors.Join(execution.ErrInvalid, err)
	case errors.Is(err, fleetdb.ErrExecutionNotOwner):
		return errors.Join(execution.ErrFenceConflict, err)
	case errors.Is(err, fleetdb.ErrExecutionInvalidTransition):
		return errors.Join(execution.ErrInvalidTransition, err)
	case errors.Is(err, fleetdb.ErrExecutionAlreadyResumed):
		return errors.Join(execution.ErrAlreadyResumed, err)
	case errors.Is(err, fleetdb.ErrExecutionConflict):
		return errors.Join(execution.ErrConflict, err)
	default:
		return errors.Join(execution.ErrUnavailable, err)
	}
}

type executionTaskRunPortsAdapter struct {
	dependencies ExecutionTaskRunPortDependencies
}

func (adapter *executionTaskRunPortsAdapter) RegisterWorkerNode(ctx context.Context, command execution.RegisterWorkerNodeCommand) (*execution.WorkerNode, error) {
	existing, err := adapter.dependencies.Nodes.Get(ctx, command.WorkspaceKey, command.NodeID)
	if err == nil {
		return adapter.refreshWorkerNode(ctx, existing, command)
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return nil, err
	}

	initialDrain := domain.NodeDrainActive
	node, err := adapter.dependencies.Nodes.Create(ctx, store.NodeCreate{
		WorkspaceKey: command.WorkspaceKey, NodeID: command.NodeID, OwnerActor: command.OwnerActor,
		RuntimeProvider: domain.RuntimeProvider(command.RuntimeProvider), Labels: append([]string(nil), command.Labels...),
		Capabilities: append([]string(nil), command.Capabilities...), ToolInventory: append([]string(nil), command.ToolInventory...),
		Version: command.Version, Capacity: command.Capacity, DrainState: initialDrain, TTL: command.TTL,
	})
	if err == nil {
		return executionWorkerNodeSnapshot(node), nil
	}
	if !errors.Is(err, domain.ErrAlreadyExists) && !errors.Is(err, domain.ErrConflict) {
		return nil, err
	}
	existing, err = adapter.dependencies.Nodes.Get(ctx, command.WorkspaceKey, command.NodeID)
	if err != nil {
		return nil, err
	}
	return adapter.refreshWorkerNode(ctx, existing, command)
}

func (adapter *executionTaskRunPortsAdapter) refreshWorkerNode(ctx context.Context, existing *domain.Node, command execution.RegisterWorkerNodeCommand) (*execution.WorkerNode, error) {
	owner := command.OwnerActor
	provider := domain.RuntimeProvider(command.RuntimeProvider)
	labels := mergeExecutionStringSet(existing.Labels, command.Labels)
	capabilities := mergeExecutionStringSet(existing.Capabilities, command.Capabilities)
	tools := mergeExecutionStringSet(existing.ToolInventory, command.ToolInventory)
	version := command.Version
	capacity := command.Capacity
	node, err := adapter.dependencies.Nodes.Update(ctx, command.WorkspaceKey, command.NodeID, store.NodeUpdate{
		OwnerActor: &owner, RuntimeProvider: &provider, Labels: &labels, Capabilities: &capabilities,
		ToolInventory: &tools, Version: &version, Capacity: &capacity,
	})
	if err != nil {
		return nil, err
	}
	return executionWorkerNodeSnapshot(node), nil
}

func (adapter *executionTaskRunPortsAdapter) HeartbeatWorkerNode(ctx context.Context, command execution.HeartbeatWorkerNodeCommand) (*execution.WorkerNode, error) {
	node, err := adapter.dependencies.Nodes.Heartbeat(ctx, command.WorkspaceKey, command.NodeID, command.TTL)
	if err != nil {
		return nil, err
	}
	return executionWorkerNodeSnapshot(node), nil
}

func (adapter *executionTaskRunPortsAdapter) SetWorkerNodeDrain(ctx context.Context, command execution.SetWorkerNodeDrainCommand) (*execution.WorkerNode, error) {
	drain := domain.NodeDrainState(command.DrainState)
	node, err := adapter.dependencies.Nodes.Update(ctx, command.WorkspaceKey, command.NodeID, store.NodeUpdate{DrainState: &drain})
	if err != nil {
		return nil, err
	}
	return executionWorkerNodeSnapshot(node), nil
}

func (adapter *executionTaskRunPortsAdapter) CheckTaskRunScheduling(ctx context.Context, query execution.TaskRunSchedulingQuery) (execution.TaskRunSchedulingResult, error) {
	requiredProviders := []string{strings.TrimSpace(query.ProviderProfile)}
	requiredFeatures := append([]string(nil), query.RequiredFeatures...)
	if query.WorkerProfileID != "" {
		profile, err := adapter.dependencies.WorkerProfiles.Get(ctx, query.WorkspaceKey, query.WorkerProfileID)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				return execution.TaskRunSchedulingResult{ReasonCode: "worker_profile_not_found"}, nil
			}
			return execution.TaskRunSchedulingResult{}, err
		}
		if !profile.Enabled {
			return execution.TaskRunSchedulingResult{ReasonCode: "worker_profile_disabled"}, nil
		}
		requiredProviders = append(requiredProviders, profile.Backend)
		requiredFeatures = append(requiredFeatures, profile.Capabilities...)
	}
	nodes, err := adapter.dependencies.Nodes.List(ctx, query.WorkspaceKey)
	if err != nil {
		return execution.TaskRunSchedulingResult{}, err
	}
	now := time.Now().UTC()
	for _, node := range nodes {
		if node == nil || (query.TargetNodeID != "" && node.NodeID != query.TargetNodeID) ||
			node.DrainState != domain.NodeDrainActive || (!node.ExpiresAt.IsZero() && !node.ExpiresAt.After(now)) {
			continue
		}
		if executionNodeSatisfies(node, requiredProviders, requiredFeatures) {
			return execution.TaskRunSchedulingResult{Schedulable: true}, nil
		}
	}
	return execution.TaskRunSchedulingResult{ReasonCode: "no_live_capable_node"}, nil
}

func executionNodeSatisfies(node *domain.Node, providers, features []string) bool {
	for _, required := range normalizeExecutionStringSet(providers) {
		if string(node.RuntimeProvider) != required && !executionStringSetContains(node.Capabilities, required) {
			return false
		}
	}
	for _, required := range normalizeExecutionStringSet(features) {
		if !executionStringSetContains(node.Capabilities, required) {
			return false
		}
	}
	return true
}

func executionTaskRunSnapshot(run *domain.TaskRun, leaseToken string) (*execution.TaskRun, error) {
	if run == nil || strings.TrimSpace(run.WorkspaceKey) == "" || strings.TrimSpace(run.TaskRunID) == "" {
		return nil, execution.ErrConflict
	}
	status, err := executionTaskRunStatus(run.Status)
	if err != nil {
		return nil, err
	}
	owner := execution.Owner{}
	if status == execution.StatusRunning && run.NodeID != "" && run.LeaseID != "" && run.FencingToken > 0 {
		owner = execution.Owner{
			ResourceKind: execution.ResourceTaskRun, ResourceID: run.TaskRunID, NodeID: run.NodeID,
			LeaseID: run.LeaseID, LeaseToken: leaseToken, FencingToken: run.FencingToken,
		}
	}
	return &execution.TaskRun{
		WorkspaceKey: run.WorkspaceKey, TaskRunID: run.TaskRunID, DriverRunID: run.DriverRunID,
		DriverStepID: run.DriverStepID, WorkItemID: run.TaskID, WorkerProfileID: run.WorkerProfileID,
		Runner: run.Runner, RunnerRef: run.RunnerRef, RunnerKind: run.RunnerKind,
		RunnerEntrypoint: run.RunnerEntrypoint, RunnerVersionID: run.RunnerVersionID,
		ProviderProfile: run.ProviderProfile, TargetNodeID: run.TargetNodeID, Status: status, Owner: owner,
		RunnerPlacement: executionPlacement(run.RunnerPlacement), SandboxPlacement: executionPlacement(run.SandboxPlacement),
		RuntimeMetadata: cloneExecutionStringMap(run.RuntimeMetadata), Input: append([]byte(nil), run.Input...),
		ExitCode: run.ExitCode, LogsRef: run.LogsRef, ArtifactsRef: run.ArtifactsRef,
		InputTokens: run.InputTokens, OutputTokens: run.OutputTokens, CacheReadTokens: run.CacheReadTokens,
		CacheWriteTokens: run.CacheWriteTokens, EstimatedCostUSD: run.EstimatedCostUSD,
		ErrorClass: run.ErrorClass, ErrorMessage: run.ErrorMessage, StartedAt: timePtr(run.StartedAt),
		NextEligibleAt: timePtr(run.NextEligibleAt), LastHeartbeat: timePtr(run.LastHeartbeat),
		FinishedAt: run.FinishedAt, CreatedAt: run.CreatedAt, UpdatedAt: run.UpdatedAt,
	}, nil
}

func executionTaskRunStatus(status domain.TaskRunStatus) (execution.Status, error) {
	switch status {
	case domain.TaskRunQueued:
		return execution.StatusQueued, nil
	case domain.TaskRunRunning:
		return execution.StatusRunning, nil
	case domain.TaskRunCompleted:
		return execution.StatusSucceeded, nil
	case domain.TaskRunFailed:
		return execution.StatusFailed, nil
	case domain.TaskRunCancelled:
		return execution.StatusCancelled, nil
	default:
		return "", execution.ErrConflict
	}
}

func executionPlacement(value domain.TaskRunPlacement) execution.Placement {
	return execution.Placement{Provider: value.Provider, NodeID: value.NodeID, RunnerID: value.RunnerID, SandboxID: value.SandboxID, CWD: value.CWD, RepoRef: value.RepoRef}
}

func legacyExecutionPlacement(value execution.Placement) domain.TaskRunPlacement {
	return domain.TaskRunPlacement{Provider: value.Provider, NodeID: value.NodeID, RunnerID: value.RunnerID, SandboxID: value.SandboxID, CWD: value.CWD, RepoRef: value.RepoRef}
}

func executionWorkerNodeSnapshot(node *domain.Node) *execution.WorkerNode {
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

func executionTaskRunDriverStepSnapshot(step *domain.DriverStep) *execution.TaskRunDriverStep {
	if step == nil {
		return nil
	}
	return &execution.TaskRunDriverStep{
		WorkspaceKey: step.WorkspaceKey, StepID: step.StepID, DriverRunID: step.DriverRunID,
		TaskRunID: step.TaskRunID, Status: string(step.Status), ActionLedgerID: step.ActionLedgerID,
	}
}

func timePtr(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	copy := value
	return &copy
}

func mergeExecutionStringSet(existing, desired []string) []string {
	return normalizeExecutionStringSet(append(append([]string(nil), existing...), desired...))
}

func normalizeExecutionStringSet(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func executionStringSetContains(values []string, want string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == want {
			return true
		}
	}
	return false
}

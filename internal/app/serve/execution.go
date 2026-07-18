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
	workflowcataloghttp "github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog/httpapi"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// ExecutionCapability is the composition-owned handle exposed to inbound
// TaskRun adapters. It reveals only the running TaskRun mutation API and the
// exact-purpose authority resolver, never the Store, issuer, or outbound
// persistence adapter.
type ExecutionCapability struct {
	issuer               *authority.Issuer
	taskRuns             execution.TaskRunAPI
	taskRunRequests      execution.TaskRunRequestAPI
	taskRunWorkers       execution.TaskRunWorkerAPI
	taskRunScheduling    execution.TaskRunSchedulingAPI
	workerProfiles       execution.WorkerProfileAPI
	taskRunConvergence   execution.TaskRunConvergenceAPI
	convergenceSource    execution.TaskRunConvergenceSource
	taskRunRecovery      execution.TaskRunRecoveryAPI
	recoveryScopes       execution.TaskRunRecoveryScopePort
	taskRunAuthorities   execution.TaskRunAuthorityResolver
	driverRuns           execution.DriverRunAPI
	driverRunAuthorities execution.DriverRunAuthorityResolver
	systemAuthorities    execution.SystemAuthorityResolver
	operatorAuthorities  workflowcataloghttp.OperatorAuthorityResolver
	awaitEvents          execution.AwaitEventNotificationAPI
	runOutcomes          execution.DriverRunOutcomeAPI
}

func (capability *ExecutionCapability) AwaitEventNotificationAPI() execution.AwaitEventNotificationAPI {
	if capability == nil {
		return nil
	}
	return capability.awaitEvents
}

func (capability *ExecutionCapability) DriverRunOutcomeAPI() execution.DriverRunOutcomeAPI {
	if capability == nil {
		return nil
	}
	return capability.runOutcomes
}

func (capability *ExecutionCapability) TaskRunRequestAPI() execution.TaskRunRequestAPI {
	if capability == nil {
		return nil
	}
	return capability.taskRunRequests
}

func (capability *ExecutionCapability) TaskRunWorkerAPI() execution.TaskRunWorkerAPI {
	if capability == nil {
		return nil
	}
	return capability.taskRunWorkers
}

func (capability *ExecutionCapability) TaskRunSchedulingAPI() execution.TaskRunSchedulingAPI {
	if capability == nil {
		return nil
	}
	return capability.taskRunScheduling
}

func (capability *ExecutionCapability) WorkerProfileAPI() execution.WorkerProfileAPI {
	if capability == nil {
		return nil
	}
	return capability.workerProfiles
}

func (capability *ExecutionCapability) TaskRunConvergenceAPI() execution.TaskRunConvergenceAPI {
	if capability == nil {
		return nil
	}
	return capability.taskRunConvergence
}

func (capability *ExecutionCapability) TaskRunConvergenceSource() execution.TaskRunConvergenceSource {
	if capability == nil {
		return nil
	}
	return capability.convergenceSource
}

func (capability *ExecutionCapability) TaskRunRecoveryAPI() execution.TaskRunRecoveryAPI {
	if capability == nil {
		return nil
	}
	return capability.taskRunRecovery
}

func (capability *ExecutionCapability) TaskRunRecoveryScopes() execution.TaskRunRecoveryScopePort {
	if capability == nil {
		return nil
	}
	return capability.recoveryScopes
}

func (capability *ExecutionCapability) TaskRunAPI() execution.TaskRunAPI {
	if capability == nil {
		return nil
	}
	return capability.taskRuns
}

func (capability *ExecutionCapability) TaskRunAuthorityResolver() execution.TaskRunAuthorityResolver {
	if capability == nil {
		return nil
	}
	return capability.taskRunAuthorities
}

func (capability *ExecutionCapability) DriverRunAPI() execution.DriverRunAPI {
	if capability == nil {
		return nil
	}
	return capability.driverRuns
}

func (capability *ExecutionCapability) DriverRunAuthorityResolver() execution.DriverRunAuthorityResolver {
	if capability == nil {
		return nil
	}
	return capability.driverRunAuthorities
}

func (capability *ExecutionCapability) SystemAuthorityResolver() execution.SystemAuthorityResolver {
	if capability == nil {
		return nil
	}
	return capability.systemAuthorities
}

func (capability *ExecutionCapability) OperatorAuthorityResolver() workflowcataloghttp.OperatorAuthorityResolver {
	if capability == nil {
		return nil
	}
	return capability.operatorAuthorities
}

type ExecutionDependencies struct {
	TaskRuns                     store.TaskRunStore
	DriverRuns                   store.DriverRunStore
	DriverSteps                  store.DriverStepStore
	TerminalStepRepairs          store.TerminalDriverStepRepairStore
	TaskRunEvents                store.TaskRunEventStore
	Nodes                        store.NodeStore
	WorkerProfiles               store.WorkerProfileStore
	Agents                       store.AgentStore
	Outbox                       store.OutboxStore
	Awaits                       store.AwaitStore
	TriggerEvents                store.TriggerEventStore
	Workspaces                   store.WorkspaceStore
	AtomicTaskRunRequests        execution.TaskRunRequestPort
	AtomicTaskRunClaims          execution.TaskRunClaimPort
	AtomicTaskRunWorkItemDesign  execution.TaskRunWorkItemDesignPort
	AtomicTaskRunRequeues        execution.TaskRunRequeuePort
	AtomicTaskRunRetryExhaustion execution.TaskRunRetryExhaustionPort
	FleetExecution               fleetdb.ExecutionTransport
	AllowLegacyStoreAdapters     bool
}

type executionTaskRunMutationPort interface {
	execution.HeartbeatPort
	execution.LogPort
	execution.FinalizePort
}

// NewExecutionCapability composes the first production Execution caller
// slice. The Store-backed adapter is a bounded migration compatibility seam;
// Execution core receives only its three consumer-owned TaskRun ports. The
// Fleet-native atomic claim/start adapter is intentionally composed later,
// once its paired transport contract is final, and never falls back here to a
// sequence of Work Item + TaskRun writes.
func NewExecutionCapability(dependencies ExecutionDependencies) (*ExecutionCapability, error) {
	return newExecutionCapability(dependencies, authority.NewIssuer(), nil)
}

func newExecutionCapability(
	dependencies ExecutionDependencies,
	issuer *authority.Issuer,
	operatorAuthorities workflowcataloghttp.OperatorAuthorityResolver,
) (*ExecutionCapability, error) {
	if err := validateExecutionDependencies(dependencies, issuer); err != nil {
		return nil, err
	}
	rules := append(execution.OperationRules(), execution.DriverRunOperationRules()...)
	admission, err := issuer.NewAdmission(rules...)
	if err != nil {
		return nil, fmt.Errorf("compose Execution admission: %w", err)
	}
	serviceDependencies, convergenceSource, recoveryScopes, err := newExecutionServiceDependencies(dependencies)
	if err != nil {
		return nil, err
	}
	service, err := execution.New(serviceDependencies, admission)
	if err != nil {
		return nil, fmt.Errorf("compose Execution service: %w", err)
	}
	return newExecutionCapabilityHandle(service, issuer, operatorAuthorities, convergenceSource, recoveryScopes), nil
}

func newExecutionTaskRunMutationPort(dependencies ExecutionDependencies) executionTaskRunMutationPort {
	if dependencies.FleetExecution != nil {
		return &executionTaskRunTransportAdapter{transport: dependencies.FleetExecution}
	}
	return &executionTaskRunStoreAdapter{taskRuns: dependencies.TaskRuns}
}

func newExecutionDriverRunDependencies(dependencies ExecutionDependencies) (execution.DriverRunDependencies, error) {
	driverRunAdapter := &executionDriverRunStoreAdapter{driverRuns: dependencies.DriverRuns, awaits: dependencies.Awaits}
	var driverRunChildStarts execution.DriverRunChildStartPort
	var driverRunCascades execution.DriverRunCascadePort
	var driverRunClaims execution.DriverRunClaimPort = driverRunAdapter
	var driverRunHeartbeats execution.DriverRunHeartbeatPort = driverRunAdapter
	var driverRunWorkItems execution.DriverRunWorkItemPort
	var driverRunFinalizer execution.DriverRunFinalizePort = driverRunAdapter
	var driverRunAwaits execution.DriverAwaitPort = driverRunAdapter
	if dependencies.FleetExecution != nil {
		fleetDriverRuns, err := newFleetDriverRunCommandPort(dependencies.FleetExecution)
		if err != nil {
			return execution.DriverRunDependencies{}, fmt.Errorf("compose Fleet DriverRun commands: %w", err)
		}
		driverRunChildStarts, driverRunCascades = fleetDriverRuns, fleetDriverRuns
		driverRunClaims, driverRunHeartbeats, driverRunFinalizer = fleetDriverRuns, fleetDriverRuns, fleetDriverRuns
		driverRunWorkItems = fleetDriverRuns
		driverRunAwaits = &executionDriverAwaitFleetPort{queries: driverRunAdapter, suspensions: fleetDriverRuns}
	}
	return execution.DriverRunDependencies{
		Submissions: driverRunAdapter, ChildStarts: driverRunChildStarts, Cascades: driverRunCascades,
		Claims: driverRunClaims, Heartbeats: driverRunHeartbeats, WorkItems: driverRunWorkItems, Finalizer: driverRunFinalizer,
		Recovery: driverRunAdapter, Awaits: driverRunAwaits, Queries: driverRunAdapter, Resolutions: driverRunAdapter,
	}, nil
}

func newExecutionTaskRunDependencies(dependencies ExecutionDependencies) (execution.TaskRunDependencies, execution.WorkerDependencies, error) {
	return NewExecutionTaskRunPorts(ExecutionTaskRunPortDependencies{
		Requests: dependencies.AtomicTaskRunRequests, Claims: dependencies.AtomicTaskRunClaims,
		WorkItemDesign: dependencies.AtomicTaskRunWorkItemDesign,
		Requeues:       dependencies.AtomicTaskRunRequeues, RetryExhaustion: dependencies.AtomicTaskRunRetryExhaustion,
		Nodes: dependencies.Nodes, WorkerProfiles: dependencies.WorkerProfiles,
	})
}

func newExecutionConvergenceDependencies(dependencies ExecutionDependencies) (execution.TaskRunConvergenceDependencies, error) {
	return NewExecutionTaskRunConvergenceDependencies(ExecutionTaskRunConvergenceDependencies{
		TaskRuns: dependencies.TaskRuns, DriverRuns: dependencies.DriverRuns, DriverSteps: dependencies.TerminalStepRepairs,
		Events: dependencies.TaskRunEvents, Agents: dependencies.Agents, Outbox: dependencies.Outbox,
	})
}

func newExecutionRecoveryDependencies(dependencies ExecutionDependencies) (execution.TaskRunRecoveryDependencies, error) {
	return NewExecutionTaskRunRecoveryDependencies(ExecutionTaskRunRecoveryDependencies{
		Workspaces: dependencies.Workspaces, Transport: dependencies.FleetExecution,
		LegacyDriverRuns: dependencies.DriverRuns, AllowLegacyStoreAdapters: dependencies.AllowLegacyStoreAdapters,
	})
}

func newExecutionServiceDependencies(
	dependencies ExecutionDependencies,
) (execution.Dependencies, execution.TaskRunConvergenceSource, execution.TaskRunRecoveryScopePort, error) {
	driverRuns, err := newExecutionDriverRunDependencies(dependencies)
	if err != nil {
		return execution.Dependencies{}, nil, nil, err
	}
	queueAdapter, err := newExecutionReconciliationQueueAdapter(dependencies.TriggerEvents, dependencies.DriverRuns)
	if err != nil {
		return execution.Dependencies{}, nil, nil, err
	}
	taskRuns, workers, err := newExecutionTaskRunDependencies(dependencies)
	if err != nil {
		return execution.Dependencies{}, nil, nil, err
	}
	convergence, err := newExecutionConvergenceDependencies(dependencies)
	if err != nil {
		return execution.Dependencies{}, nil, nil, err
	}
	recovery, err := newExecutionRecoveryDependencies(dependencies)
	if err != nil {
		return execution.Dependencies{}, nil, nil, err
	}
	taskRunMutations := newExecutionTaskRunMutationPort(dependencies)
	return execution.Dependencies{
		Heartbeats: taskRunMutations, Logs: taskRunMutations, Finalizer: taskRunMutations,
		DriverRuns: driverRuns, TaskRuns: taskRuns, Workers: workers, Convergence: convergence,
		TaskRunRecovery: recovery, AwaitEvents: queueAdapter, RunOutcomes: queueAdapter,
	}, convergence.Source, recovery.Scopes, nil
}

func newExecutionCapabilityHandle(
	service *execution.Service,
	issuer *authority.Issuer,
	operatorAuthorities workflowcataloghttp.OperatorAuthorityResolver,
	convergenceSource execution.TaskRunConvergenceSource,
	recoveryScopes execution.TaskRunRecoveryScopePort,
) *ExecutionCapability {
	return &ExecutionCapability{
		issuer:               issuer,
		taskRuns:             service,
		taskRunRequests:      service,
		taskRunWorkers:       service,
		taskRunScheduling:    service,
		workerProfiles:       service,
		taskRunConvergence:   service,
		convergenceSource:    convergenceSource,
		taskRunRecovery:      service,
		recoveryScopes:       recoveryScopes,
		taskRunAuthorities:   &executionTaskRunAuthorityResolver{issuer: issuer, now: time.Now},
		driverRuns:           service,
		driverRunAuthorities: &executionDriverRunAuthorityResolver{issuer: issuer, now: time.Now},
		systemAuthorities:    &executionSystemAuthorityResolver{issuer: issuer, now: time.Now},
		operatorAuthorities:  operatorAuthorities,
		awaitEvents:          service,
		runOutcomes:          service,
	}
}

func validateExecutionDependencies(dependencies ExecutionDependencies, issuer *authority.Issuer) error {
	requiredPortsMissing := []bool{
		dependencies.TaskRuns == nil,
		dependencies.DriverRuns == nil,
		dependencies.DriverSteps == nil,
		dependencies.TerminalStepRepairs == nil,
		dependencies.TaskRunEvents == nil,
		dependencies.Nodes == nil,
		dependencies.WorkerProfiles == nil,
		dependencies.Agents == nil,
		dependencies.Outbox == nil,
		dependencies.Awaits == nil,
		dependencies.Workspaces == nil,
		dependencies.TriggerEvents == nil,
		dependencies.AtomicTaskRunRequests == nil,
		dependencies.AtomicTaskRunClaims == nil,
		dependencies.AtomicTaskRunWorkItemDesign == nil,
		dependencies.AtomicTaskRunRequeues == nil,
		dependencies.AtomicTaskRunRetryExhaustion == nil,
	}
	for _, missing := range requiredPortsMissing {
		if missing {
			return fmt.Errorf("compose Execution: all TaskRun, DriverRun, worker, convergence, Await, and atomic-claim ports are required")
		}
	}
	if dependencies.FleetExecution == nil && !dependencies.AllowLegacyStoreAdapters {
		return fmt.Errorf("compose Execution: Fleet Execution owner transport is required")
	}
	if issuer == nil {
		return fmt.Errorf("compose Execution: authority issuer is required")
	}
	return nil
}

// executionTaskRunTransportAdapter binds owner commands directly to FleetDB's
// token-header Execution transport. No composite Store mutation is reachable
// from the production composition.
type executionTaskRunTransportAdapter struct {
	transport fleetdb.ExecutionTransport
}

func (adapter *executionTaskRunTransportAdapter) Heartbeat(
	ctx context.Context,
	command execution.HeartbeatCommand,
) (execution.HeartbeatResult, error) {
	run, err := adapter.transport.HeartbeatTaskRun(ctx, command.WorkspaceKey, command.Owner.ResourceID, store.TaskRunHeartbeat{
		NodeID: command.Owner.NodeID, LeaseID: command.Owner.LeaseID, LeaseToken: command.Owner.LeaseToken,
		FencingToken: command.Owner.FencingToken, RuntimeMetadata: cloneExecutionStringMap(command.RuntimeMetadata),
		LogsRef: command.LogsRef, ArtifactsRef: command.ArtifactsRef, HeartbeatAt: command.At,
	})
	if err != nil {
		return execution.HeartbeatResult{}, mapFleetExecutionPortError(err)
	}
	owner, err := executionOwnerFromTaskRun(command.Owner.LeaseToken, run)
	if err != nil {
		return execution.HeartbeatResult{}, err
	}
	return execution.HeartbeatResult{Owner: owner}, nil
}

func (adapter *executionTaskRunTransportAdapter) AppendLog(
	ctx context.Context,
	command execution.AppendLogCommand,
) (execution.LogEntry, error) {
	entry, err := adapter.transport.AppendTaskRunLog(ctx, command.WorkspaceKey, command.Owner.ResourceID, store.TaskRunLogAppend{
		RequestID: command.RequestID, NodeID: command.Owner.NodeID, LeaseID: command.Owner.LeaseID,
		LeaseToken: command.Owner.LeaseToken, FencingToken: command.Owner.FencingToken,
		Stream: command.Stream, Text: command.Text, Timestamp: command.Timestamp,
	})
	if err != nil {
		return execution.LogEntry{}, mapFleetExecutionPortError(err)
	}
	if entry == nil {
		return execution.LogEntry{}, execution.ErrConflict
	}
	return execution.LogEntry{TaskRunID: entry.TaskRunID, Sequence: entry.Sequence, Stream: entry.Stream, Text: entry.Text, Timestamp: entry.Timestamp}, nil
}

func (adapter *executionTaskRunTransportAdapter) Finalize(
	ctx context.Context,
	command execution.FinalizeCommand,
) (execution.FinalizeResult, error) {
	status, err := legacyTaskRunStatus(command.Classification.Status)
	if err != nil {
		return execution.FinalizeResult{}, err
	}
	run, err := adapter.transport.CompleteTaskRun(ctx, command.WorkspaceKey, command.Owner.ResourceID, store.TaskRunComplete{
		CompletionID: command.RequestID, NodeID: command.Owner.NodeID, LeaseID: command.Owner.LeaseID,
		LeaseToken: command.Owner.LeaseToken, FencingToken: command.Owner.FencingToken, Status: status,
		ExitCode: command.ExitCode, LogsRef: command.LogsRef, ArtifactsRef: command.ArtifactsRef,
		RequiredArtifactIDs: append([]string(nil), command.RequiredArtifactIDs...), RequireArtifacts: command.RequireArtifacts,
		InputTokens: command.InputTokens, OutputTokens: command.OutputTokens, CacheReadTokens: command.CacheReadTokens,
		CacheWriteTokens: command.CacheWriteTokens, EstimatedCostUSD: command.EstimatedCostUSD,
		RuntimeMetadata: cloneExecutionStringMap(command.RuntimeMetadata), ErrorClass: command.Classification.ErrorClass,
		ErrorMessage: command.Classification.Summary, CloseTask: command.CloseWorkItem,
		CloseReason: command.CloseReason, FinishedAt: command.FinishedAt,
	})
	if err != nil {
		return execution.FinalizeResult{}, mapFleetExecutionPortError(err)
	}
	owner, err := executionOwnerFromTaskRun(command.Owner.LeaseToken, run)
	if err != nil {
		return execution.FinalizeResult{}, err
	}
	finishedAt := command.FinishedAt
	if run.FinishedAt != nil {
		finishedAt = *run.FinishedAt
	}
	return execution.FinalizeResult{Owner: owner, Status: command.Classification.Status, FinishedAt: finishedAt}, nil
}

// executionTaskRunStoreAdapter is the explicitly opted-in legacy test seam.
// Production composition fails closed unless FleetExecution is present.
type executionTaskRunStoreAdapter struct {
	taskRuns store.TaskRunStore
}

func (adapter *executionTaskRunStoreAdapter) Heartbeat(
	ctx context.Context,
	command execution.HeartbeatCommand,
) (execution.HeartbeatResult, error) {
	run, err := adapter.taskRuns.Heartbeat(ctx, command.WorkspaceKey, command.Owner.ResourceID, store.TaskRunHeartbeat{
		NodeID:          command.Owner.NodeID,
		LeaseID:         command.Owner.LeaseID,
		LeaseToken:      command.Owner.LeaseToken,
		FencingToken:    command.Owner.FencingToken,
		RuntimeMetadata: cloneExecutionStringMap(command.RuntimeMetadata),
		LogsRef:         command.LogsRef,
		ArtifactsRef:    command.ArtifactsRef,
		HeartbeatAt:     command.At,
	})
	if err != nil {
		return execution.HeartbeatResult{}, err
	}
	owner, err := executionOwnerFromTaskRun(command.Owner.LeaseToken, run)
	if err != nil {
		return execution.HeartbeatResult{}, err
	}
	return execution.HeartbeatResult{Owner: owner}, nil
}

func (adapter *executionTaskRunStoreAdapter) AppendLog(
	ctx context.Context,
	command execution.AppendLogCommand,
) (execution.LogEntry, error) {
	entry, err := adapter.taskRuns.AppendLog(ctx, command.WorkspaceKey, command.Owner.ResourceID, store.TaskRunLogAppend{
		RequestID:    command.RequestID,
		NodeID:       command.Owner.NodeID,
		LeaseID:      command.Owner.LeaseID,
		LeaseToken:   command.Owner.LeaseToken,
		FencingToken: command.Owner.FencingToken,
		Stream:       command.Stream,
		Text:         command.Text,
		Timestamp:    command.Timestamp,
	})
	if err != nil {
		return execution.LogEntry{}, err
	}
	if entry == nil {
		return execution.LogEntry{}, fmt.Errorf("append TaskRun log returned no entry: %w", execution.ErrConflict)
	}
	return execution.LogEntry{
		TaskRunID: entry.TaskRunID,
		Sequence:  entry.Sequence,
		Stream:    entry.Stream,
		Text:      entry.Text,
		Timestamp: entry.Timestamp,
	}, nil
}

func (adapter *executionTaskRunStoreAdapter) Finalize(
	ctx context.Context,
	command execution.FinalizeCommand,
) (execution.FinalizeResult, error) {
	status, err := legacyTaskRunStatus(command.Classification.Status)
	if err != nil {
		return execution.FinalizeResult{}, err
	}
	run, err := adapter.taskRuns.Complete(ctx, command.WorkspaceKey, command.Owner.ResourceID, store.TaskRunComplete{
		CompletionID:        command.RequestID,
		NodeID:              command.Owner.NodeID,
		LeaseID:             command.Owner.LeaseID,
		LeaseToken:          command.Owner.LeaseToken,
		FencingToken:        command.Owner.FencingToken,
		Status:              status,
		ExitCode:            command.ExitCode,
		LogsRef:             command.LogsRef,
		ArtifactsRef:        command.ArtifactsRef,
		RequiredArtifactIDs: append([]string(nil), command.RequiredArtifactIDs...),
		RequireArtifacts:    command.RequireArtifacts,
		InputTokens:         command.InputTokens,
		OutputTokens:        command.OutputTokens,
		CacheReadTokens:     command.CacheReadTokens,
		CacheWriteTokens:    command.CacheWriteTokens,
		EstimatedCostUSD:    command.EstimatedCostUSD,
		RuntimeMetadata:     cloneExecutionStringMap(command.RuntimeMetadata),
		ErrorClass:          command.Classification.ErrorClass,
		ErrorMessage:        command.Classification.Summary,
		CloseTask:           command.CloseWorkItem,
		CloseReason:         command.CloseReason,
		FinishedAt:          command.FinishedAt,
	})
	if err != nil {
		return execution.FinalizeResult{}, err
	}
	owner, err := executionOwnerFromTaskRun(command.Owner.LeaseToken, run)
	if err != nil {
		return execution.FinalizeResult{}, err
	}
	finishedAt := command.FinishedAt
	if run.FinishedAt != nil {
		finishedAt = *run.FinishedAt
	}
	return execution.FinalizeResult{
		Owner:      owner,
		Status:     command.Classification.Status,
		FinishedAt: finishedAt,
	}, nil
}

func executionOwnerFromTaskRun(leaseToken string, run *domain.TaskRun) (execution.Owner, error) {
	if run == nil || strings.TrimSpace(run.TaskRunID) == "" || strings.TrimSpace(run.NodeID) == "" || strings.TrimSpace(run.LeaseID) == "" || run.FencingToken <= 0 {
		return execution.Owner{}, fmt.Errorf("invalid persisted TaskRun owner: %w", execution.ErrConflict)
	}
	return execution.Owner{
		ResourceKind: execution.ResourceTaskRun,
		ResourceID:   run.TaskRunID,
		NodeID:       run.NodeID,
		LeaseID:      run.LeaseID,
		LeaseToken:   leaseToken,
		FencingToken: run.FencingToken,
	}, nil
}

func legacyTaskRunStatus(status execution.Status) (domain.TaskRunStatus, error) {
	switch status {
	case execution.StatusSucceeded:
		return domain.TaskRunCompleted, nil
	case execution.StatusFailed, execution.StatusBlocked:
		return domain.TaskRunFailed, nil
	case execution.StatusCancelled:
		return domain.TaskRunCancelled, nil
	default:
		return "", fmt.Errorf("unsupported terminal Execution status %q: %w", status, execution.ErrInvalid)
	}
}

func cloneExecutionStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

type executionDriverRunStoreAdapter struct {
	driverRuns store.DriverRunStore
	awaits     store.AwaitStore
}

func (adapter *executionDriverRunStoreAdapter) SubmitDriverRun(
	ctx context.Context,
	command execution.SubmitDriverRunCommand,
) (*execution.DriverRun, error) {
	run, err := adapter.driverRuns.Create(ctx, store.DriverRunCreate{
		WorkspaceKey: command.WorkspaceKey, RunID: command.RunID,
		DriverID: command.DriverID, DriverVersionID: command.DriverVersionID,
		Entrypoint: command.Entrypoint, SourceKind: command.SourceKind, SourceRef: command.SourceRef,
		EpicID: command.EpicID, ParentRunID: command.ParentRunID, TriggerBindingID: command.TriggerBindingID,
		IdempotencyKey: command.RequestID, Payload: append([]byte(nil), command.Payload...),
	})
	if err != nil {
		return nil, translateDriverRunStoreError(err)
	}
	return executionDriverRunSnapshot(run)
}

func (adapter *executionDriverRunStoreAdapter) GetDriverRun(
	ctx context.Context,
	workspace, runID string,
) (*execution.DriverRun, error) {
	run, err := adapter.driverRuns.Get(ctx, workspace, runID)
	if err != nil {
		return nil, translateDriverRunStoreError(err)
	}
	return executionDriverRunSnapshot(run)
}

func (adapter *executionDriverRunStoreAdapter) ResolveAndResumeDriverAwait(
	ctx context.Context,
	command execution.ResolveDriverAwaitCommand,
) error {
	resolver, ok := adapter.awaits.(store.AtomicAwaitStore)
	if !ok {
		return execution.ErrUnavailable
	}
	return resolver.ResolveAwaitAndResume(
		ctx, command.WorkspaceKey, command.InstanceKey, command.EventID,
		append([]byte(nil), command.Payload...), command.Actor,
	)
}

func (adapter *executionDriverRunStoreAdapter) ClaimDriverRun(
	ctx context.Context,
	command execution.ClaimDriverRunCommand,
) (*execution.DriverRun, error) {
	run, err := adapter.driverRuns.Claim(ctx, command.WorkspaceKey, command.RunID, command.NodeID, command.LeaseID)
	if err != nil {
		return nil, translateDriverRunStoreError(err)
	}
	snapshot, err := executionDriverRunSnapshot(run)
	if err != nil {
		return nil, err
	}
	snapshot.Owner.LeaseToken = command.LeaseToken
	return snapshot, nil
}

func (adapter *executionDriverRunStoreAdapter) HeartbeatDriverRun(
	ctx context.Context,
	command execution.DriverRunHeartbeatCommand,
) (*execution.DriverRun, error) {
	run, err := adapter.driverRuns.Heartbeat(ctx, command.WorkspaceKey, command.Owner.ResourceID,
		command.Owner.NodeID, command.Owner.LeaseID, command.Owner.FencingToken)
	if err != nil {
		return nil, translateDriverRunStoreError(err)
	}
	snapshot, err := executionDriverRunSnapshot(run)
	if err != nil {
		return nil, err
	}
	snapshot.Owner.LeaseToken = command.Owner.LeaseToken
	return snapshot, nil
}

func (adapter *executionDriverRunStoreAdapter) FinalizeDriverRun(
	ctx context.Context,
	command execution.FinalizeDriverRunCommand,
) (*execution.DriverRun, error) {
	status, err := legacyDriverRunStatus(command.Status)
	if err != nil {
		return nil, err
	}
	run, err := adapter.driverRuns.Finish(ctx, command.WorkspaceKey, command.Owner.ResourceID, store.DriverRunFinish{
		NodeID: command.Owner.NodeID, LeaseID: command.Owner.LeaseID, FencingToken: command.Owner.FencingToken,
		Status: status, Summary: command.Summary, ErrorClass: command.ErrorClass,
		Output: cloneExecutionStringMap(command.Output),
	})
	if err != nil {
		return nil, translateDriverRunStoreError(err)
	}
	return executionDriverRunSnapshot(run)
}

func (adapter *executionDriverRunStoreAdapter) RecoverDriverRuns(
	ctx context.Context,
	command execution.RecoverDriverRunsCommand,
) (*execution.DriverRunRecoveryResult, error) {
	result, err := adapter.driverRuns.RecoverStale(ctx, command.WorkspaceKey, store.StaleDriverRunRecovery{
		StaleBefore: command.ObservedAt.Add(-command.MaxAge), MaxAgeSeconds: int64(command.MaxAge / time.Second),
		ErrorClass: command.ErrorClass, Summary: command.Summary, Limit: command.Limit,
	})
	if err != nil {
		return nil, translateDriverRunStoreError(err)
	}
	if result == nil {
		return nil, execution.ErrConflict
	}
	return &execution.DriverRunRecoveryResult{
		WorkspaceKey: result.WorkspaceKey, StaleBefore: result.StaleBefore, RecoveredAt: result.RecoveredAt,
		Recovered: result.Recovered, SkippedFresh: result.SkippedFresh,
		RecoveredRunIDs:    append([]string(nil), result.RecoveredRunIDs...),
		SkippedFreshRunIDs: append([]string(nil), result.SkippedFreshRunIDs...),
	}, nil
}

func (adapter *executionDriverRunStoreAdapter) RegisterAndCheckDriverAwait(
	ctx context.Context,
	workspace string,
	registration execution.DriverAwaitRegistration,
) (*execution.DriverAwaitRegistrationResult, error) {
	result, err := adapter.awaits.RegisterAwaitAndCheck(ctx, workspace, store.AwaitRegistration{
		InstanceKey: registration.InstanceKey, RunID: registration.RunID, Pattern: registration.Pattern,
		ActorAllow: append([]string(nil), registration.ActorAllow...), Deadline: registration.Deadline,
		RegisteredAt: registration.RegisteredAt,
	})
	if err != nil {
		return nil, translateDriverRunStoreError(err)
	}
	if result == nil {
		return nil, execution.ErrConflict
	}
	instance, err := executionDriverAwaitSnapshot(result.Instance)
	if err != nil {
		return nil, err
	}
	return &execution.DriverAwaitRegistrationResult{Instance: instance, Satisfied: result.Satisfied}, nil
}

func (adapter *executionDriverRunStoreAdapter) GetSatisfiedDriverAwait(
	ctx context.Context,
	workspace, instanceKey string,
) (*execution.DriverAwaitInstance, error) {
	instance, err := adapter.awaits.GetSatisfiedAwait(ctx, workspace, instanceKey)
	if err != nil {
		return nil, translateDriverRunStoreError(err)
	}
	return executionDriverAwaitSnapshot(instance)
}

func (adapter *executionDriverRunStoreAdapter) SuspendDriverRun(
	ctx context.Context,
	workspace string,
	owner execution.Owner,
	instanceKey string,
) (*execution.DriverRun, error) {
	run, err := adapter.driverRuns.Suspend(ctx, workspace, owner.ResourceID, owner.NodeID, owner.LeaseID, owner.FencingToken, instanceKey)
	if err != nil {
		return nil, translateDriverRunStoreError(err)
	}
	return executionDriverRunSnapshot(run)
}

func (adapter *executionDriverRunStoreAdapter) ResumeAwaitingDriverRun(
	ctx context.Context,
	workspace, runID, instanceKey, eventID string,
) (*execution.DriverRun, error) {
	run, err := adapter.driverRuns.ResumeAwaiting(ctx, workspace, runID, instanceKey, eventID)
	if err != nil {
		return nil, translateDriverRunStoreError(err)
	}
	return executionDriverRunSnapshot(run)
}

func executionDriverRunSnapshot(run *domain.DriverRun) (*execution.DriverRun, error) {
	if run == nil || strings.TrimSpace(run.WorkspaceKey) == "" || strings.TrimSpace(run.RunID) == "" {
		return nil, fmt.Errorf("invalid persisted DriverRun: %w", execution.ErrConflict)
	}
	status, err := executionDriverRunStatus(run.Status)
	if err != nil {
		return nil, err
	}
	owner := execution.Owner{ResourceKind: execution.ResourceDriverRun, ResourceID: run.RunID}
	if run.NodeID != "" || run.LeaseID != "" || run.FencingToken != 0 {
		owner.NodeID, owner.LeaseID, owner.FencingToken = run.NodeID, run.LeaseID, run.FencingToken
	}
	return &execution.DriverRun{
		WorkspaceKey: run.WorkspaceKey, RunID: run.RunID, DriverID: run.DriverID, DriverVersionID: run.DriverVersionID,
		Entrypoint: run.Entrypoint, SourceKind: run.SourceKind, SourceRef: run.SourceRef, EpicID: run.EpicID,
		ParentRunID: run.ParentRunID, TriggerBindingID: run.TriggerBindingID, AgentServiceID: run.AgentServiceID,
		SubjectKey: run.SubjectKey, Status: status, Owner: owner, IdempotencyKey: run.IdempotencyKey,
		Payload: append([]byte(nil), run.Payload...), Output: cloneExecutionStringMap(run.Output), Summary: run.Summary,
		ErrorClass: run.ErrorClass, StartedAt: run.StartedAt, LastHeartbeat: run.LastHeartbeat, FinishedAt: run.FinishedAt,
		AwaitInstanceKey: run.AwaitInstanceKey, SuspendedAt: run.SuspendedAt,
		CancelRequestedAt: run.CancelRequestedAt, CancelRequestedReason: run.CancelRequestedReason,
		ResumeSourceEventID: run.ResumeSourceEventID, CreatedAt: run.CreatedAt, UpdatedAt: run.UpdatedAt,
	}, nil
}

func executionDriverAwaitSnapshot(instance *domain.AwaitInstance) (*execution.DriverAwaitInstance, error) {
	if instance == nil || strings.TrimSpace(instance.InstanceKey) == "" || strings.TrimSpace(instance.RunID) == "" {
		return nil, fmt.Errorf("invalid persisted DriverRun await: %w", execution.ErrConflict)
	}
	status := execution.DriverAwaitStatus(instance.Status)
	switch status {
	case execution.DriverAwaitPending, execution.DriverAwaitSatisfied, execution.DriverAwaitTimedOut:
	default:
		return nil, fmt.Errorf("unsupported persisted await status %q: %w", status, execution.ErrConflict)
	}
	return &execution.DriverAwaitInstance{
		WorkspaceKey: instance.WorkspaceKey, InstanceKey: instance.InstanceKey, RunID: instance.RunID,
		Pattern: instance.Pattern, ActorAllow: append([]string(nil), instance.ActorAllow...),
		Deadline: instance.Deadline, RegisteredAt: instance.RegisteredAt, Status: status,
		SatisfiedByEventID: instance.SatisfiedByEventID, SatisfiedActor: instance.SatisfiedActor,
		SatisfiedPayload: append([]byte(nil), instance.SatisfiedPayload...), ResumedAt: instance.ResumedAt,
	}, nil
}

func executionDriverRunStatus(status domain.DriverRunStatus) (execution.DriverRunStatus, error) {
	mapped := execution.DriverRunStatus(status)
	switch mapped {
	case execution.DriverRunQueued, execution.DriverRunRunning, execution.DriverRunCompleted,
		execution.DriverRunFailed, execution.DriverRunNeedsReview, execution.DriverRunCancelled,
		execution.DriverRunSuspendedAwait:
		return mapped, nil
	default:
		return "", fmt.Errorf("unsupported persisted DriverRun status %q: %w", status, execution.ErrConflict)
	}
}

func legacyDriverRunStatus(status execution.DriverRunStatus) (domain.DriverRunStatus, error) {
	mapped := domain.DriverRunStatus(status)
	if !mapped.IsTerminal() {
		return "", fmt.Errorf("unsupported terminal DriverRun status %q: %w", status, execution.ErrInvalid)
	}
	return mapped, nil
}

func translateDriverRunStoreError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, domain.ErrDriverRunAlreadyResumed):
		return errors.Join(execution.ErrAlreadyResumed, err)
	case errors.Is(err, domain.ErrNotFound):
		return errors.Join(execution.ErrNotFound, err)
	default:
		return err
	}
}

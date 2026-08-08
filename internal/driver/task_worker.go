package driver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"

	"github.com/tysonthomas9/loomcli/internal/domain"
	artifactsmodule "github.com/tysonthomas9/loomcli/internal/modules/artifacts"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
)

var ErrNoQueuedTaskRun = errors.New("task worker: no queued task run")

var errTaskWorkerNodeLifecycleInFlight = errors.New("task worker: node lifecycle pass already in flight")

type taskWorkerClaimState struct {
	runMu   sync.Mutex
	pending *execution.ClaimTaskRunCommand
}

var taskWorkerClaimStateInitMu sync.Mutex

type taskWorkerNodeLifecycleEntry struct {
	registered      bool
	heartbeatSent   bool
	active          bool
	nextHeartbeatAt time.Time
}

type taskWorkerNodeLifecycleKeyState struct {
	mu    sync.Mutex
	entry taskWorkerNodeLifecycleEntry
}

// taskWorkerNodeLifecycleState is shared by every concurrency slot cloned
// from one serve TaskWorker template. The slots use the same process node, so
// registering, activating, and heartbeating that node once per workspace is
// sufficient; claims remain independent and concurrent.
type taskWorkerNodeLifecycleState struct {
	mu      sync.Mutex
	entries map[string]*taskWorkerNodeLifecycleKeyState
}

var taskWorkerNodeLifecycleStateInitMu sync.Mutex

type TaskWorker struct {
	Store        taskWorkerStore
	WorkspaceKey string
	TaskRunID    string
	WorkDir      string
	NodeID       string
	// NodeCapacity is the number of concurrent TaskRun slots advertised by the
	// shared worker node. Values below one preserve the standalone default of one.
	NodeCapacity       int
	RunnerID           string
	LeaseID            string
	LeaseToken         string
	SupportedProviders []string
	Capabilities       []string
	WorkerProfileIDs   []string
	RunnerPlacement    domain.TaskRunPlacement
	SandboxPlacement   domain.TaskRunPlacement
	HeartbeatInterval  time.Duration
	MaxAttempts        int
	Executor           TaskExecutor
	// Artifacts is injected from the serve-owned capability and forwarded to
	// every HostBridge executor. There is no production Store.Artifacts fallback.
	Artifacts artifactsmodule.API
	// Execution is the only production mutation surface for TaskRun and worker
	// node lifecycle. Store remains available for read-side runner resolution,
	// workspace enumeration, and host-bridge dependencies.
	Execution          execution.TaskRunWorkerAPI
	TaskRunAuthorities execution.TaskRunAuthorityResolver
	// Convergence is the Execution-owned immediate projection command. The
	// runtime convergence pass replays the same idempotent command after
	// crashes; production composition supplies both fields together.
	Convergence          execution.TaskRunConvergenceAPI
	ExecutionAuthorities execution.SystemAuthorityResolver
	ExecutionComponentID string
	// APIBaseURL is the serve task-run API base URL exported to bridge task
	// runners as LOOM_TASK_RUN_API_URL (see HostBridgeTaskExecutor).
	APIBaseURL string
	// LocalSettingsDir is passed through to HostBridgeTaskExecutor and the
	// default worktree resolver so bundled runners and git operations can read
	// desktop-local settings just in time.
	LocalSettingsDir string
	// SourceControl is the authority-free checkout materializer used by the
	// default local task worktree resolver.
	SourceControl sourcecontrol.Materializer
	// WorktreeResolver resolves per-task-run local worktrees for bundled local
	// task runners. Nil uses the machine-local workspace cache.
	WorktreeResolver TaskWorktreeResolver
	// Now is a clock seam for tests; nil uses time.Now.
	Now func() time.Time

	// claimState retains the complete private claim envelope across an
	// ambiguous response so the next pass can replay FleetDB's durable receipt.
	// It is intentionally per worker and never copied into public results.
	claimState *taskWorkerClaimState
	// nodeLifecycle is shared across runtime clones that represent concurrent
	// claim slots on the same process node. It bounds node lifecycle traffic
	// independently from the one-second queued-work claim cadence.
	nodeLifecycle *taskWorkerNodeLifecycleState
}

func (w *TaskWorker) RunOnce(ctx context.Context) (*TaskRunRequestOutcome, error) {
	if w == nil || w.Store == nil {
		return nil, fmt.Errorf("store required: %w", domain.ErrInvalid)
	}
	claimState := w.taskWorkerRuntimeClaimState()
	claimState.runMu.Lock()
	defer claimState.runMu.Unlock()
	if err := w.validateRunOnceDependencies(); err != nil {
		return nil, err
	}
	workDir, err := (&Executor{WorkDir: w.WorkDir}).resolveWorkDir()
	if err != nil {
		return nil, err
	}
	if ws := strings.TrimSpace(w.WorkspaceKey); ws != "" {
		if pending := claimState.pending; pending != nil && pending.WorkspaceKey != ws {
			return nil, fmt.Errorf("pending TaskRun claim belongs to workspace %q, not %q: %w", pending.WorkspaceKey, ws, execution.ErrConflict)
		}
		return w.runOnceInWorkspace(ctx, ws, workDir)
	}
	if pending := claimState.pending; pending != nil {
		outcome, err := w.runOnceInWorkspace(ctx, pending.WorkspaceKey, workDir)
		if err == nil {
			return outcome, nil
		}
		if !errors.Is(err, ErrNoQueuedTaskRun) {
			return nil, err
		}
	}
	workspaces, err := w.Store.Workspaces().List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list workspaces for task worker: %w", err)
	}
	for _, ws := range workspaces {
		if ws == nil {
			continue
		}
		outcome, err := w.runOnceInWorkspace(ctx, ws.Key, workDir)
		if err == nil {
			return outcome, nil
		}
		if !errors.Is(err, ErrNoQueuedTaskRun) {
			return nil, err
		}
	}
	return nil, ErrNoQueuedTaskRun
}

func (w *TaskWorker) validateRunOnceDependencies() error {
	if w.Execution == nil || w.TaskRunAuthorities == nil || w.ExecutionAuthorities == nil || w.Convergence == nil || strings.TrimSpace(w.ExecutionComponentID) == "" {
		return fmt.Errorf("execution TaskRun worker APIs and exact component id are required: %w", execution.ErrUnavailable)
	}
	if w.Artifacts == nil {
		return fmt.Errorf("artifacts capability is required: %w", artifactsmodule.ErrUnavailable)
	}
	return nil
}

//nolint:funlen // The worker's claim, placement, and execution setup must remain visibly ordered.
func (w *TaskWorker) runOnceInWorkspace(ctx context.Context, ws, workDir string) (*TaskRunRequestOutcome, error) {
	nodeID := w.nodeID()
	if pending := w.taskWorkerRuntimeClaimState().pending; pending != nil && pending.WorkspaceKey == ws {
		nodeID = pending.NodeID
	}
	if nodeID == "" {
		return nil, fmt.Errorf("worker node id required: %w", domain.ErrInvalid)
	}
	if err := w.ensureNode(ctx, ws, nodeID); err != nil {
		if errors.Is(err, errTaskWorkerNodeLifecycleInFlight) {
			pending := w.taskWorkerRuntimeClaimState().pending
			if pending == nil || pending.WorkspaceKey != ws {
				return nil, ErrNoQueuedTaskRun
			}
		}
		return nil, err
	}
	executor := w.Executor
	if executor == nil {
		stacks := StackBindingResolverFor(w.SourceControl)
		executor = HostBridgeTaskExecutor{
			Store:               w.Store,
			Artifacts:           w.Artifacts,
			ArtifactAuthorities: w.TaskRunAuthorities,
			WorktreePath:        workDir,
			APIBaseURL:          w.APIBaseURL,
			LocalSettingsDir:    w.LocalSettingsDir,
			WorktreeResolver: firstNonNilTaskWorktreeResolver(w.WorktreeResolver, LocalTaskWorktreeResolver{
				Store:         w.Store,
				Lineage:       StackLineageLookup{Bindings: stacks},
				SourceControl: w.SourceControl,
			}),
			StackBindings: stacks,
			TaskOutcomes:  taskOutcomeRecorder(w.SourceControl),
		}
	} else {
		executor = withTaskWorkerArtifacts(executor, w.Artifacts, w.TaskRunAuthorities)
	}
	outcome, err := w.claimAndExecuteTaskRun(ctx, ws, nodeID, executor)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, ErrNoQueuedTaskRun
		}
		return nil, err
	}
	if outcome.Run != nil && outcome.Run.Status.IsTerminal() && w.Convergence != nil {
		if err := w.convergeTerminalTaskRun(ctx, ws, outcome.Run.TaskRunID); err != nil {
			return outcome, err
		}
		return outcome, nil
	}
	return outcome, nil
}

func withTaskWorkerArtifacts(executor TaskExecutor, api artifactsmodule.API, resolver execution.TaskRunAuthorityResolver) TaskExecutor {
	switch value := executor.(type) {
	case HostBridgeTaskExecutor:
		if value.Artifacts == nil {
			value.Artifacts = api
		}
		if value.ArtifactAuthorities == nil {
			value.ArtifactAuthorities = resolver
		}
		return value
	case *HostBridgeTaskExecutor:
		if value != nil && value.Artifacts == nil {
			value.Artifacts = api
		}
		if value != nil && value.ArtifactAuthorities == nil {
			value.ArtifactAuthorities = resolver
		}
	}
	return executor
}

func (w *TaskWorker) claimAndExecuteTaskRun(ctx context.Context, workspace, nodeID string, executor TaskExecutor) (*TaskRunRequestOutcome, error) {
	claimState := w.taskWorkerRuntimeClaimState()
	componentID := strings.TrimSpace(w.ExecutionComponentID)
	systemAuth, err := w.ExecutionAuthorities.ResolveExecutionSystemAuthority(ctx, workspace, execution.ActionClaimTaskRun, componentID)
	if err != nil {
		return nil, fmt.Errorf("resolve TaskRun claim authority: %w", err)
	}
	command := w.pendingOrNewTaskRunClaim(claimState, workspace, nodeID, componentID)
	claim, err := w.Execution.ClaimTaskRun(ctx, systemAuth, command)
	if err != nil {
		if errors.Is(err, execution.ErrNotFound) || errors.Is(err, domain.ErrNotFound) {
			clearTaskWorkerPendingClaim(claimState, command.RequestID)
			return nil, ErrNoQueuedTaskRun
		}
		if !errors.Is(err, execution.ErrUnavailable) && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			clearTaskWorkerPendingClaim(claimState, command.RequestID)
		}
		return nil, fmt.Errorf("claim queued TaskRun: %w", err)
	}
	claimed, err := legacyTaskRunFromExecution(claim.Run)
	if err != nil {
		return nil, err
	}
	clearTaskWorkerPendingClaim(claimState, command.RequestID)
	leaseToken := command.LeaseToken
	owner := execution.Owner{
		ResourceKind: execution.ResourceTaskRun, ResourceID: claimed.TaskRunID,
		NodeID: claimed.NodeID, LeaseID: claimed.LeaseID, LeaseToken: leaseToken, FencingToken: claimed.FencingToken,
	}
	opts := executeClaimedTaskRunOptions{
		WorkspaceKey: claimed.WorkspaceKey, DriverRunID: claimed.DriverRunID, DriverStepID: claimed.DriverStepID,
		TaskID: claimed.TaskID, ProviderProfile: claimed.ProviderProfile,
		RunnerTrustLevel: workflowcatalog.DriverTrustLevel(claimed.RuntimeMetadata["runner_trust_level"]),
		ParentSessionID:  claimed.RuntimeMetadata["parent_session_id"], LeaseToken: leaseToken,
		HeartbeatInterval: w.HeartbeatInterval, CloseTaskOnSuccess: true, MaxAttempts: w.maxAttempts(),
		HeartbeatSource: "task_run_worker", Now: w.Now,
	}
	refs := claimedTaskRunRefsFromOptions(claimed, opts)
	stopHeartbeat := w.startExecutionTaskRunHeartbeat(ctx, claimed, owner, refs)
	defer stopHeartbeat()

	execResult, execErr := executor.ExecuteTask(ctx, taskExecRequest(claimed, opts, refs))
	return w.finishExecutedTaskRun(ctx, workspace, claimed, owner, opts, refs, leaseToken, execResult, execErr)
}

func (w *TaskWorker) pendingOrNewTaskRunClaim(state *taskWorkerClaimState, workspace, nodeID, componentID string) execution.ClaimTaskRunCommand {
	if state.pending != nil {
		return cloneTaskWorkerClaimCommand(*state.pending)
	}
	now := taskRunNow(w.Now)
	leaseID, leaseToken := w.taskRunLease(nodeID)
	command := execution.ClaimTaskRunCommand{
		WorkspaceKey: workspace, RequestID: fmt.Sprintf("claim-task-run:%s:%d", componentID, now.UnixNano()),
		TaskRunID: strings.TrimSpace(w.TaskRunID), NodeID: nodeID, RunnerID: strings.TrimSpace(w.RunnerID),
		LeaseID: leaseID, LeaseToken: leaseToken, LeaseTTL: (&Executor{HeartbeatInterval: w.HeartbeatInterval}).nodeTTL(),
		SupportedProviders: append([]string(nil), w.SupportedProviders...), Capabilities: append([]string(nil), w.Capabilities...),
		WorkerProfileIDs: append([]string(nil), w.WorkerProfileIDs...),
		RunnerPlacement:  executionTaskRunPlacement(w.runnerPlacement(nodeID)),
		SandboxPlacement: executionTaskRunPlacement(w.SandboxPlacement), ClaimedAt: now,
	}
	stored := cloneTaskWorkerClaimCommand(command)
	state.pending = &stored
	return command
}

func cloneTaskWorkerClaimCommand(command execution.ClaimTaskRunCommand) execution.ClaimTaskRunCommand {
	command.SupportedProviders = append([]string(nil), command.SupportedProviders...)
	command.Capabilities = append([]string(nil), command.Capabilities...)
	command.WorkerProfileIDs = append([]string(nil), command.WorkerProfileIDs...)
	return command
}

func clearTaskWorkerPendingClaim(state *taskWorkerClaimState, requestID string) {
	if state == nil || state.pending == nil || state.pending.RequestID != requestID {
		return
	}
	*state.pending = execution.ClaimTaskRunCommand{}
	state.pending = nil
}

func (w *TaskWorker) taskWorkerRuntimeClaimState() *taskWorkerClaimState {
	// TaskWorker is commonly constructed with a struct literal. A tiny global
	// initializer lock makes that path race-safe without placing a copy-sensitive
	// mutex in the worker value itself.
	taskWorkerClaimStateInitMu.Lock()
	defer taskWorkerClaimStateInitMu.Unlock()
	if w.claimState == nil {
		w.claimState = &taskWorkerClaimState{}
	}
	return w.claimState
}

// CloneForRuntime returns an independent worker execution slot. The retained
// claim envelope is runtime-local and must never be shared when the serve
// template fans out into concurrent worker passes. Node lifecycle state is
// deliberately shared because every slot registers the same process node.
func (w *TaskWorker) CloneForRuntime() TaskWorker {
	nodeLifecycle := w.taskWorkerRuntimeNodeLifecycleState()
	clone := *w
	clone.claimState = &taskWorkerClaimState{}
	clone.nodeLifecycle = nodeLifecycle
	return clone
}

func (w *TaskWorker) taskWorkerRuntimeNodeLifecycleState() *taskWorkerNodeLifecycleState {
	taskWorkerNodeLifecycleStateInitMu.Lock()
	defer taskWorkerNodeLifecycleStateInitMu.Unlock()
	if w.nodeLifecycle == nil {
		w.nodeLifecycle = &taskWorkerNodeLifecycleState{entries: make(map[string]*taskWorkerNodeLifecycleKeyState)}
	}
	return w.nodeLifecycle
}

func (state *taskWorkerNodeLifecycleState) keyState(key string) *taskWorkerNodeLifecycleKeyState {
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.entries == nil {
		state.entries = make(map[string]*taskWorkerNodeLifecycleKeyState)
	}
	entry := state.entries[key]
	if entry == nil {
		entry = &taskWorkerNodeLifecycleKeyState{}
		state.entries[key] = entry
	}
	return entry
}

func (w *TaskWorker) taskRunLease(nodeID string) (string, string) {
	leaseID := strings.TrimSpace(w.LeaseID)
	if leaseID == "" {
		leaseID = generatedTaskRunLeaseID(nodeID)
	}
	leaseToken := strings.TrimSpace(w.LeaseToken)
	if leaseToken == "" {
		leaseToken = generatedTaskRunLeaseToken()
	}
	return leaseID, leaseToken
}

type executedTaskRunState struct {
	claimed     *domain.TaskRun
	owner       execution.Owner
	opts        executeClaimedTaskRunOptions
	leaseToken  string
	result      TaskExecResult
	completion  taskExecCompletion
	metadata    map[string]string
	retry       taskRunRetryDecisionResult
	artifactIDs []string
}

func (w *TaskWorker) finishExecutedTaskRun(
	ctx context.Context,
	workspace string,
	claimed *domain.TaskRun,
	owner execution.Owner,
	opts executeClaimedTaskRunOptions,
	refs claimedTaskRunRefs,
	leaseToken string,
	result TaskExecResult,
	execErr error,
) (*TaskRunRequestOutcome, error) {
	state := executedTaskRunState{
		claimed: claimed, owner: owner, opts: opts, leaseToken: leaseToken, result: result,
		completion:  normalizeTaskExecCompletion(result, execErr),
		metadata:    taskExecRuntimeMetadata(result, refs),
		artifactIDs: normalizeArtifactIDs(result.ArtifactIDs),
	}
	state.retry = taskRunRetryDecision(claimed, opts, state.completion)
	if state.retry.Retry {
		return w.requeueExecutedTaskRun(ctx, workspace, state)
	}
	if state.completion.Status == domain.TaskRunFailed {
		return w.exhaustExecutedTaskRun(ctx, workspace, state)
	}
	return w.finalizeExecutedTaskRun(ctx, workspace, state)
}

func (w *TaskWorker) requeueExecutedTaskRun(ctx context.Context, workspace string, state executedTaskRunState) (*TaskRunRequestOutcome, error) {
	state.metadata = taskRunRetryMetadata(state.claimed, state.retry, state.completion, state.metadata)
	auth, err := w.TaskRunAuthorities.ResolveTaskRunAuthority(ctx, workspace, execution.ActionRequeueTaskRun, state.owner)
	if err != nil {
		return nil, fmt.Errorf("resolve TaskRun requeue authority: %w", err)
	}
	requeuedAt := taskRunNow(w.Now)
	result, err := w.Execution.RequeueTaskRun(ctx, auth, execution.RequeueTaskRunCommand{
		WorkspaceKey: workspace, RequestID: fmt.Sprintf("requeue-task-run:%s:%d", state.claimed.TaskRunID, state.retry.Attempt),
		Owner: state.owner, RuntimeMetadata: state.metadata, LogsRef: state.result.LogsRef, ArtifactsRef: state.result.ArtifactsRef,
		ErrorClass: state.completion.ErrorClass, ErrorMessage: state.completion.ErrorMessage,
		RequeuedAt: requeuedAt, NextEligibleAt: requeuedAt.Add(taskRunRetryBackoff(state.retry.Attempt)),
	})
	if err != nil {
		return nil, fmt.Errorf("requeue TaskRun: %w", err)
	}
	run, err := legacyTaskRunFromExecution(result.Run)
	return &TaskRunRequestOutcome{Run: run, LeaseToken: state.leaseToken, ArtifactIDs: state.artifactIDs}, err
}

func (w *TaskWorker) exhaustExecutedTaskRun(ctx context.Context, workspace string, state executedTaskRunState) (*TaskRunRequestOutcome, error) {
	state.metadata = taskRunBlockedMetadata(state.claimed, state.opts, state.completion, state.metadata)
	auth, err := w.TaskRunAuthorities.ResolveTaskRunAuthority(ctx, workspace, execution.ActionExhaustTaskRunRetries, state.owner)
	if err != nil {
		return nil, fmt.Errorf("resolve TaskRun retry exhaustion authority: %w", err)
	}
	exitCode := state.completion.ExitCode
	result, err := w.Execution.ExhaustTaskRunRetries(ctx, auth, execution.ExhaustTaskRunRetriesCommand{
		WorkspaceKey: workspace, RequestID: fmt.Sprintf("exhaust-task-run:%s:%d", state.claimed.TaskRunID, state.retry.Attempt),
		Owner: state.owner, Attempt: state.retry.Attempt, MaxAttempts: state.retry.MaxAttempts, ExitCode: &exitCode,
		LogsRef: state.result.LogsRef, ArtifactsRef: state.result.ArtifactsRef,
		RequiredArtifactIDs: state.artifactIDs, RequireArtifacts: len(state.artifactIDs) > 0,
		InputTokens: state.result.InputTokens, OutputTokens: state.result.OutputTokens,
		CacheReadTokens: state.result.CacheReadTokens, CacheWriteTokens: state.result.CacheWriteTokens,
		EstimatedCostUSD: state.result.EstimatedCostUSD, RuntimeMetadata: state.metadata,
		ErrorClass: state.completion.ErrorClass, ErrorMessage: state.completion.ErrorMessage, FinishedAt: taskRunNow(w.Now),
	})
	if err != nil {
		return nil, fmt.Errorf("exhaust TaskRun retries: %w", err)
	}
	run, err := legacyTaskRunFromExecution(result.Run)
	return &TaskRunRequestOutcome{Run: run, LeaseToken: state.leaseToken, ArtifactIDs: state.artifactIDs}, err
}

func (w *TaskWorker) finalizeExecutedTaskRun(ctx context.Context, workspace string, state executedTaskRunState) (*TaskRunRequestOutcome, error) {
	auth, err := w.TaskRunAuthorities.ResolveTaskRunAuthority(ctx, workspace, execution.ActionFinalize, state.owner)
	if err != nil {
		return nil, fmt.Errorf("resolve TaskRun finalize authority: %w", err)
	}
	status := execution.StatusSucceeded
	if state.completion.Status == domain.TaskRunCancelled {
		status = execution.StatusCancelled
	}
	exitCode := state.completion.ExitCode
	finishedAt := taskRunNow(w.Now)
	_, err = w.Execution.Finalize(ctx, auth, execution.FinalizeCommand{
		WorkspaceKey: workspace, RequestID: "worker-complete-" + state.claimed.TaskRunID,
		Owner: state.owner, Classification: execution.ExitClassification{
			Status: status, ErrorClass: state.completion.ErrorClass, Summary: state.completion.ErrorMessage,
		},
		ExitCode: &exitCode, LogsRef: state.result.LogsRef, ArtifactsRef: state.result.ArtifactsRef,
		RequiredArtifactIDs: state.artifactIDs, RequireArtifacts: len(state.artifactIDs) > 0,
		InputTokens: state.result.InputTokens, OutputTokens: state.result.OutputTokens,
		CacheReadTokens: state.result.CacheReadTokens, CacheWriteTokens: state.result.CacheWriteTokens,
		EstimatedCostUSD: state.result.EstimatedCostUSD, RuntimeMetadata: state.metadata,
		CloseWorkItem: status == execution.StatusSucceeded && resolveCloseTaskOnSuccess(true, state.claimed.RuntimeMetadata),
		CloseReason:   "completed by task run", FinishedAt: finishedAt,
	})
	if err != nil {
		return nil, fmt.Errorf("finalize TaskRun: %w", err)
	}
	applyExecutedTaskRunResult(state.claimed, state.result, state.completion, state.metadata, exitCode, finishedAt)
	return &TaskRunRequestOutcome{Run: state.claimed, LeaseToken: state.leaseToken, ArtifactIDs: state.artifactIDs}, nil
}

func applyExecutedTaskRunResult(claimed *domain.TaskRun, result TaskExecResult, completion taskExecCompletion, metadata map[string]string, exitCode int, finishedAt time.Time) {
	claimed.Status = completion.Status
	claimed.ExitCode = &exitCode
	claimed.LogsRef = result.LogsRef
	claimed.ArtifactsRef = result.ArtifactsRef
	claimed.RuntimeMetadata = metadata
	claimed.ErrorClass = completion.ErrorClass
	claimed.ErrorMessage = completion.ErrorMessage
	claimed.FinishedAt = &finishedAt
}

func (w *TaskWorker) startExecutionTaskRunHeartbeat(
	ctx context.Context,
	run *domain.TaskRun,
	owner execution.Owner,
	refs claimedTaskRunRefs,
) context.CancelFunc {
	heartbeatCtx, cancel := context.WithCancel(ctx)
	interval := taskRunHeartbeatInterval(w.HeartbeatInterval)
	if interval <= 0 {
		return cancel
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case at := <-ticker.C:
				auth, err := w.TaskRunAuthorities.ResolveTaskRunAuthority(heartbeatCtx, run.WorkspaceKey, execution.ActionHeartbeat, owner)
				if err == nil {
					_, err = w.Execution.Heartbeat(heartbeatCtx, auth, execution.HeartbeatCommand{
						WorkspaceKey: run.WorkspaceKey, Owner: owner, At: at.UTC(),
						RuntimeMetadata: map[string]string{
							"driver_run_id": refs.DriverRunID, "runner": refs.Runner, "runner_ref": refs.RunnerRef,
							"runner_kind": refs.RunnerKind, "provider_profile": refs.ProviderProfile,
							"heartbeat_source": refs.HeartbeatSource,
						},
					})
				}
				if err != nil && heartbeatCtx.Err() == nil {
					slog.WarnContext(heartbeatCtx, "Execution TaskRun heartbeat failed; run may be recovered as stale",
						"task_run_id", run.TaskRunID, "workspace", run.WorkspaceKey, "err", err)
				}
			}
		}
	}()
	return cancel
}

func executionTaskRunPlacement(value domain.TaskRunPlacement) execution.Placement {
	return execution.Placement{
		Provider: value.Provider, NodeID: value.NodeID, RunnerID: value.RunnerID,
		SandboxID: value.SandboxID, CWD: value.CWD, RepoRef: value.RepoRef,
	}
}

func legacyTaskRunFromExecution(run *execution.TaskRun) (*domain.TaskRun, error) {
	if run == nil || strings.TrimSpace(run.TaskRunID) == "" {
		return nil, fmt.Errorf("execution returned no TaskRun: %w", execution.ErrConflict)
	}
	status := domain.TaskRunStatus(run.Status)
	if run.Status == execution.StatusSucceeded {
		status = domain.TaskRunCompleted
	}
	legacy := &domain.TaskRun{
		WorkspaceKey: run.WorkspaceKey, TaskRunID: run.TaskRunID, DriverRunID: run.DriverRunID,
		DriverStepID: run.DriverStepID, TaskID: run.WorkItemID, WorkerProfileID: run.WorkerProfileID,
		Runner: run.Runner, RunnerRef: run.RunnerRef, RunnerKind: run.RunnerKind,
		RunnerEntrypoint: run.RunnerEntrypoint, RunnerVersionID: run.RunnerVersionID,
		ProviderProfile: run.ProviderProfile, TargetNodeID: run.TargetNodeID, Status: status,
		NodeID: run.Owner.NodeID, LeaseID: run.Owner.LeaseID, FencingToken: run.Owner.FencingToken,
		RunnerPlacement: domain.TaskRunPlacement{
			Provider: run.RunnerPlacement.Provider, NodeID: run.RunnerPlacement.NodeID,
			RunnerID: run.RunnerPlacement.RunnerID, SandboxID: run.RunnerPlacement.SandboxID,
			CWD: run.RunnerPlacement.CWD, RepoRef: run.RunnerPlacement.RepoRef,
		},
		SandboxPlacement: domain.TaskRunPlacement{
			Provider: run.SandboxPlacement.Provider, NodeID: run.SandboxPlacement.NodeID,
			RunnerID: run.SandboxPlacement.RunnerID, SandboxID: run.SandboxPlacement.SandboxID,
			CWD: run.SandboxPlacement.CWD, RepoRef: run.SandboxPlacement.RepoRef,
		},
		Input: append([]byte(nil), run.Input...), ExitCode: run.ExitCode, LogsRef: run.LogsRef,
		ArtifactsRef: run.ArtifactsRef, InputTokens: run.InputTokens, OutputTokens: run.OutputTokens,
		CacheReadTokens: run.CacheReadTokens, CacheWriteTokens: run.CacheWriteTokens,
		EstimatedCostUSD: run.EstimatedCostUSD, RuntimeMetadata: cloneStringMap(run.RuntimeMetadata),
		ErrorClass: run.ErrorClass, ErrorMessage: run.ErrorMessage, FinishedAt: run.FinishedAt,
		CreatedAt: run.CreatedAt, UpdatedAt: run.UpdatedAt,
	}
	if run.NextEligibleAt != nil {
		legacy.NextEligibleAt = *run.NextEligibleAt
	}
	if run.StartedAt != nil {
		legacy.StartedAt = *run.StartedAt
	}
	if run.LastHeartbeat != nil {
		legacy.LastHeartbeat = *run.LastHeartbeat
	}
	return legacy, nil
}

func (w *TaskWorker) convergeTerminalTaskRun(ctx context.Context, workspace, taskRunID string) error {
	if w.ExecutionAuthorities == nil {
		return fmt.Errorf("execution convergence authority resolver required: %w", execution.ErrUnavailable)
	}
	componentID := strings.TrimSpace(w.ExecutionComponentID)
	if componentID == "" {
		componentID = "execution-task-run-worker-1"
	}
	auth, err := w.ExecutionAuthorities.ResolveExecutionSystemAuthority(ctx, workspace, execution.ActionConvergeTaskRun, componentID)
	if err != nil {
		return fmt.Errorf("resolve TaskRun convergence authority: %w", err)
	}
	_, err = w.Convergence.ConvergeTaskRun(ctx, auth, execution.ConvergeTaskRunCommand{
		WorkspaceKey: workspace, RequestID: "immediate-task-run:" + taskRunID,
		TaskRunID: taskRunID, ObservedAt: taskRunNow(w.Now),
	})
	if err != nil {
		return fmt.Errorf("converge terminal TaskRun: %w", err)
	}
	return nil
}

func firstNonNilTaskWorktreeResolver(primary, fallback TaskWorktreeResolver) TaskWorktreeResolver {
	if primary != nil {
		return primary
	}
	return fallback
}

func (w *TaskWorker) nodeID() string {
	if id := strings.TrimSpace(w.NodeID); id != "" {
		return id
	}
	host, _ := os.Hostname()
	if host == "" {
		host = "local"
	}
	return fmt.Sprintf("loom-task-worker-%s-%d", host, os.Getpid())
}

func (w *TaskWorker) nodeCapacity() int {
	if w.NodeCapacity < 1 {
		return 1
	}
	return w.NodeCapacity
}

func (w *TaskWorker) runnerPlacement(nodeID string) domain.TaskRunPlacement {
	placement := w.RunnerPlacement
	if placement.Provider == "" {
		placement.Provider = "loom-serve"
	}
	if placement.NodeID == "" {
		placement.NodeID = nodeID
	}
	if placement.RunnerID == "" {
		placement.RunnerID = w.RunnerID
	}
	return placement
}

func (w *TaskWorker) maxAttempts() int {
	if w.MaxAttempts < 1 {
		return 2
	}
	return w.MaxAttempts
}

func (w *TaskWorker) ensureNode(ctx context.Context, ws, nodeID string) error {
	ttl := (&Executor{HeartbeatInterval: w.HeartbeatInterval}).nodeTTL()
	now := taskRunNow(w.Now)
	componentID := strings.TrimSpace(w.ExecutionComponentID)
	lifecycle := w.taskWorkerRuntimeNodeLifecycleState()
	key := ws + "\x00" + nodeID
	keyState := lifecycle.keyState(key)
	if !keyState.mu.TryLock() {
		return errTaskWorkerNodeLifecycleInFlight
	}
	defer keyState.mu.Unlock()
	entry := keyState.entry

	if !entry.registered {
		if err := w.registerTaskWorkerRuntimeNode(ctx, ws, nodeID, componentID, ttl, now); err != nil {
			return err
		}
		entry.registered = true
		keyState.entry = entry
	}

	heartbeatInterval := taskWorkerNodeHeartbeatInterval(w.HeartbeatInterval)
	heartbeatDue := !entry.heartbeatSent || !now.Before(entry.nextHeartbeatAt)
	if heartbeatDue {
		if err := w.heartbeatTaskWorkerRuntimeNode(ctx, ws, nodeID, componentID, ttl, now); err != nil {
			if errors.Is(err, execution.ErrNotFound) || errors.Is(err, domain.ErrNotFound) {
				keyState.entry = taskWorkerNodeLifecycleEntry{}
			}
			return err
		}
		entry.heartbeatSent = true
		entry.nextHeartbeatAt = now.Add(heartbeatInterval)
		keyState.entry = entry
	}

	if !entry.active {
		if err := w.activateTaskWorkerRuntimeNode(ctx, ws, nodeID, componentID, now); err != nil {
			if errors.Is(err, execution.ErrNotFound) || errors.Is(err, domain.ErrNotFound) {
				keyState.entry = taskWorkerNodeLifecycleEntry{}
			}
			return err
		}
		entry.active = true
		keyState.entry = entry
	}
	return nil
}

func (w *TaskWorker) registerTaskWorkerRuntimeNode(
	ctx context.Context, ws, nodeID, componentID string, ttl time.Duration, now time.Time,
) error {
	registerAuth, err := w.ExecutionAuthorities.ResolveExecutionSystemAuthority(ctx, ws, execution.ActionRegisterWorkerNode, componentID)
	if err != nil {
		return fmt.Errorf("resolve task worker registration authority: %w", err)
	}
	_, err = w.Execution.RegisterWorkerNode(ctx, registerAuth, execution.RegisterWorkerNodeCommand{
		WorkspaceKey: ws, RequestID: "register-task-worker:" + componentID + ":" + nodeID,
		NodeID: nodeID, OwnerActor: executorOwnerActor(), RuntimeProvider: string(domain.RuntimeProviderLocal),
		Labels: []string{"loom-driver-executor", "loom-task-worker"}, Capabilities: w.nodeCapabilities(),
		ToolInventory: []string{"loom-driver", "loom-task-worker"}, Version: "loom-serve", Capacity: w.nodeCapacity(),
		TTL: ttl, RegisteredAt: now,
	})
	if err != nil {
		return fmt.Errorf("register task worker node: %w", err)
	}
	return nil
}

func (w *TaskWorker) heartbeatTaskWorkerRuntimeNode(
	ctx context.Context, ws, nodeID, componentID string, ttl time.Duration, now time.Time,
) error {
	heartbeatAuth, err := w.ExecutionAuthorities.ResolveExecutionSystemAuthority(ctx, ws, execution.ActionHeartbeatWorkerNode, componentID)
	if err != nil {
		return fmt.Errorf("resolve task worker node heartbeat authority: %w", err)
	}
	_, err = w.Execution.HeartbeatWorkerNode(ctx, heartbeatAuth, execution.HeartbeatWorkerNodeCommand{
		WorkspaceKey: ws, RequestID: fmt.Sprintf("heartbeat-task-worker:%s:%d", componentID, now.UnixNano()),
		NodeID: nodeID, TTL: ttl, HeartbeatAt: now,
	})
	if err != nil {
		return fmt.Errorf("heartbeat task worker node: %w", err)
	}
	return nil
}

func (w *TaskWorker) activateTaskWorkerRuntimeNode(
	ctx context.Context, ws, nodeID, componentID string, now time.Time,
) error {
	drainAuth, err := w.ExecutionAuthorities.ResolveExecutionSystemAuthority(ctx, ws, execution.ActionSetWorkerNodeDrain, componentID)
	if err != nil {
		return fmt.Errorf("resolve task worker drain authority: %w", err)
	}
	_, err = w.Execution.SetWorkerNodeDrain(ctx, drainAuth, execution.SetWorkerNodeDrainCommand{
		WorkspaceKey: ws, RequestID: "activate-task-worker:" + componentID + ":" + nodeID,
		NodeID: nodeID, DrainState: execution.WorkerNodeActive, ChangedAt: now,
	})
	if err != nil {
		return fmt.Errorf("activate task worker node: %w", err)
	}
	return nil
}

func (w *TaskWorker) nodeCapabilities() []string {
	values := []string{"driver-runner", "task-runner"}
	values = append(values, w.SupportedProviders...)
	values = append(values, w.Capabilities...)
	values = append(values, w.SandboxPlacement.Provider)
	return normalizeStringList(values)
}

func taskRunHeartbeatInterval(interval time.Duration) time.Duration {
	if interval == 0 {
		return 30 * time.Second
	}
	if interval < 0 {
		return 0
	}
	return interval
}

// taskWorkerNodeHeartbeatInterval is intentionally independent from the
// TaskRun execution heartbeat switch. Tests and one-shot workers may disable
// claimed-run heartbeats with a negative interval, but the shared worker node
// still has a FleetDB TTL and must remain alive for future claims.
func taskWorkerNodeHeartbeatInterval(interval time.Duration) time.Duration {
	if interval <= 0 {
		return 30 * time.Second
	}
	return interval
}

func TaskRunResultFromDomain(run *domain.TaskRun, artifactIDs ...[]string) TaskRunRequestResult {
	if run == nil {
		return TaskRunRequestResult{}
	}
	ids := []string(nil)
	if len(artifactIDs) > 0 {
		ids = normalizeArtifactIDs(artifactIDs[0])
	}
	return TaskRunRequestResult{
		ID:               run.TaskRunID,
		TaskRunID:        run.TaskRunID,
		DriverStepID:     run.DriverStepID,
		TaskID:           run.TaskID,
		Status:           run.Status,
		ExitCode:         run.ExitCode,
		LogsRef:          run.LogsRef,
		ArtifactsRef:     run.ArtifactsRef,
		ArtifactIDs:      ids,
		InputTokens:      run.InputTokens,
		OutputTokens:     run.OutputTokens,
		CacheReadTokens:  run.CacheReadTokens,
		CacheWriteTokens: run.CacheWriteTokens,
		EstimatedCostUSD: run.EstimatedCostUSD,
		ErrorClass:       run.ErrorClass,
		ErrorMessage:     run.ErrorMessage,
		FinishedAt:       run.FinishedAt,
		Runner:           run.Runner,
		RunnerRef:        run.RunnerRef,
		RunnerKind:       run.RunnerKind,
		RunnerEntrypoint: run.RunnerEntrypoint,
		RunnerVersionID:  run.RunnerVersionID,
		ProviderProfile:  run.ProviderProfile,
		RuntimeMetadata:  cloneStringMap(run.RuntimeMetadata),
	}
}

func TaskRunResultFromOutcome(outcome *TaskRunRequestOutcome) TaskRunRequestResult {
	if outcome == nil {
		return TaskRunRequestResult{}
	}
	result := TaskRunResultFromDomain(outcome.Run, outcome.ArtifactIDs)
	return result
}

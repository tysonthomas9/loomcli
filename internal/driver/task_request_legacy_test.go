package driver

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// The helpers in this file preserve the historical Store-backed execution
// path only for legacy unit and integration fixtures. Production callers use
// the typed Execution TaskRun APIs instead.

func heartbeatTaskRun(ctx context.Context, s store.Store, run *domain.TaskRun, leaseToken string, interval time.Duration, metadata map[string]string) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := s.TaskRuns().Heartbeat(ctx, run.WorkspaceKey, run.TaskRunID, store.TaskRunHeartbeat{
				NodeID:          run.NodeID,
				LeaseID:         run.LeaseID,
				LeaseToken:      leaseToken,
				FencingToken:    run.FencingToken,
				RuntimeMetadata: cloneStringMap(metadata),
			}); err != nil && ctx.Err() == nil {
				slog.WarnContext(ctx, "legacy task run heartbeat failed; run may be swept as stale",
					"task_run_id", run.TaskRunID, "workspace", run.WorkspaceKey, "err", err)
			}
		}
	}
}

func RequestTaskRun(ctx context.Context, s store.Store, opts TaskRunRequestOptions, executor TaskExecutor) (*domain.TaskRun, error) {
	outcome, err := RequestTaskRunWithResult(ctx, s, opts, executor)
	if err != nil {
		return nil, err
	}
	return outcome.Run, nil
}

func EnqueueTaskRun(ctx context.Context, s store.Store, opts TaskRunRequestOptions, preflighter TaskProviderPreflighter) (*domain.TaskRun, error) {
	outcome, err := EnqueueTaskRunWithResult(ctx, s, opts, preflighter)
	if err != nil {
		return nil, err
	}
	return outcome.Run, nil
}

func EnqueueTaskRunWithResult(ctx context.Context, s store.Store, opts TaskRunRequestOptions, preflighter TaskProviderPreflighter) (*TaskRunRequestOutcome, error) {
	if s == nil {
		return nil, fmt.Errorf("store required: %w", domain.ErrInvalid)
	}
	opts = normalizeTaskRunRequestOptions(opts)
	if err := validateTaskRunRequestOptions(opts); err != nil {
		return nil, err
	}
	if preflighter == nil {
		preflighter = LocalTaskExecutor{}
	}
	parent, err := verifyTaskRunRequestParent(ctx, s, opts)
	if err != nil {
		return nil, err
	}
	resolved, err := resolveTaskRunRequestRunner(ctx, s, opts, parent)
	if err != nil {
		return nil, err
	}
	opts = resolved
	resolved, err = preflightTaskRunRequest(ctx, opts, preflighter)
	if err != nil {
		return nil, err
	}
	opts = resolved
	if err := verifyTaskRunRequestSchedulable(ctx, s, opts); err != nil {
		return nil, err
	}
	refs := newTaskRunRequestRefs(opts, parent)
	queued, err := createQueuedTaskRun(ctx, s, opts, refs)
	if err != nil {
		return nil, fmt.Errorf("create task run: %w", err)
	}
	if err := linkQueuedTaskRunRequestDriverStep(ctx, s, opts, queued); err != nil {
		return nil, err
	}
	appendTaskRunEvent(ctx, s, queued, domain.TaskRunEventQueued, taskExecCompletion{}, taskRunEventContext{EpicID: parent.EpicID})
	return &TaskRunRequestOutcome{Run: queued}, nil
}

func ClaimAndExecuteTaskRun(ctx context.Context, s store.Store, opts TaskRunWorkerOptions, executor TaskExecutor) (*domain.TaskRun, error) {
	outcome, err := ClaimAndExecuteTaskRunWithResult(ctx, s, opts, executor)
	if err != nil {
		return nil, err
	}
	return outcome.Run, nil
}

func ClaimAndExecuteTaskRunWithResult(ctx context.Context, s store.Store, opts TaskRunWorkerOptions, executor TaskExecutor) (*TaskRunRequestOutcome, error) {
	if s == nil {
		return nil, fmt.Errorf("store required: %w", domain.ErrInvalid)
	}
	opts.WorkspaceKey = strings.TrimSpace(opts.WorkspaceKey)
	opts.TaskRunID = strings.TrimSpace(opts.TaskRunID)
	opts.NodeID = strings.TrimSpace(opts.NodeID)
	opts.RunnerID = strings.TrimSpace(opts.RunnerID)
	opts.LeaseID = strings.TrimSpace(opts.LeaseID)
	opts.LeaseToken = strings.TrimSpace(opts.LeaseToken)
	if opts.WorkspaceKey == "" || opts.NodeID == "" {
		return nil, fmt.Errorf("workspace key and node id required: %w", domain.ErrInvalid)
	}
	if executor == nil {
		executor = LocalTaskExecutor{}
	}
	leaseID := opts.LeaseID
	if leaseID == "" {
		leaseID = generatedTaskRunLeaseID(opts.NodeID)
	}
	leaseToken := opts.LeaseToken
	if leaseToken == "" {
		leaseToken = generatedTaskRunLeaseToken()
	}
	claimed, err := s.TaskRuns().ClaimQueued(ctx, opts.WorkspaceKey, store.TaskRunClaim{
		TaskRunID:          opts.TaskRunID,
		NodeID:             opts.NodeID,
		RunnerID:           opts.RunnerID,
		LeaseID:            leaseID,
		LeaseToken:         leaseToken,
		SupportedProviders: taskRunWorkerSupportedProviders(opts),
		Capabilities:       normalizeStringList(opts.Capabilities),
		WorkerProfileIDs:   normalizeStringList(opts.WorkerProfileIDs),
		RunnerPlacement:    opts.RunnerPlacement,
		SandboxPlacement:   opts.SandboxPlacement,
		ClaimedAt:          taskRunNow(opts.Now),
	})
	if err != nil {
		return nil, fmt.Errorf("claim queued task run: %w", err)
	}
	return executeClaimedTaskRunWithResult(ctx, s, claimed, executeClaimedTaskRunOptions{
		WorkspaceKey:       opts.WorkspaceKey,
		LeaseToken:         leaseToken,
		HeartbeatInterval:  opts.HeartbeatInterval,
		DeferCompletion:    opts.DeferCompletion,
		CloseTaskOnSuccess: opts.CloseTaskOnSuccess,
		MaxAttempts:        opts.MaxAttempts,
		HeartbeatSource:    "task_run_worker",
		Now:                opts.Now,
	}, executor)
}

func RequestTaskRunWithResult(ctx context.Context, s store.Store, opts TaskRunRequestOptions, executor TaskExecutor) (*TaskRunRequestOutcome, error) {
	if s == nil {
		return nil, fmt.Errorf("store required: %w", domain.ErrInvalid)
	}
	opts = normalizeTaskRunRequestOptions(opts)
	if err := validateTaskRunRequestOptions(opts); err != nil {
		return nil, err
	}
	if executor == nil {
		executor = LocalTaskExecutor{}
	}
	parent, err := verifyTaskRunRequestParent(ctx, s, opts)
	if err != nil {
		return nil, err
	}
	resolved, err := resolveTaskRunRequestRunner(ctx, s, opts, parent)
	if err != nil {
		return nil, err
	}
	opts = resolved
	resolved, err = preflightTaskRunRequest(ctx, opts, executor)
	if err != nil {
		return nil, err
	}
	opts = resolved
	refs := newTaskRunRequestRefs(opts, parent)
	queued, err := createQueuedTaskRun(ctx, s, opts, refs)
	if err != nil {
		return nil, fmt.Errorf("create task run: %w", err)
	}
	claimed, err := claimQueuedTaskRunRequest(ctx, s, opts, queued, refs)
	if err != nil {
		return nil, fmt.Errorf("claim task run: %w", err)
	}
	if err := linkTaskRunRequestDriverStep(ctx, s, opts, claimed); err != nil {
		return nil, err
	}
	return executeClaimedTaskRunRequest(ctx, s, opts, refs, claimed, executor)
}

func executeClaimedTaskRunRequest(ctx context.Context, s store.Store, opts TaskRunRequestOptions, refs taskRunRequestRefs, claimed *domain.TaskRun, executor TaskExecutor) (*TaskRunRequestOutcome, error) {
	return executeClaimedTaskRunWithResult(ctx, s, claimed, executeClaimedTaskRunOptions{
		WorkspaceKey:      opts.WorkspaceKey,
		DriverRunID:       opts.DriverRunID,
		DriverStepID:      opts.DriverStepID,
		TaskID:            opts.TaskID,
		ProviderProfile:   opts.ProviderProfile,
		RunnerTrustLevel:  opts.RunnerTrustLevel,
		ParentSessionID:   opts.ParentSessionID,
		LeaseToken:        refs.LeaseToken,
		HeartbeatInterval: opts.HeartbeatInterval,
		DeferCompletion:   opts.DeferCompletion,
		UpdateDriverStep:  opts.DriverStepID != "",
		ParentNodeID:      opts.ParentNodeID,
		ParentLeaseID:     opts.ParentLeaseID,
		ParentFence:       opts.ParentFence,
		HeartbeatSource:   "driver_task_request",
	}, executor)
}

type taskRunRequestRefs struct {
	NodeID     string
	TaskRunID  string
	LeaseID    string
	LeaseToken string
}

func verifyTaskRunRequestParent(ctx context.Context, s store.Store, opts TaskRunRequestOptions) (*domain.DriverRun, error) {
	parent, err := s.DriverRuns().Get(ctx, opts.WorkspaceKey, opts.DriverRunID)
	if err != nil {
		return nil, fmt.Errorf("get parent driver run: %w", err)
	}
	if parent.Status != domain.DriverRunRunning {
		return nil, fmt.Errorf("driver run %q is %s, want running: %w", opts.DriverRunID, parent.Status, domain.ErrInvalidTransition)
	}
	if parent.LeaseID == "" && parent.FencingToken == 0 {
		return parent, nil
	}
	if opts.ParentNodeID == "" || opts.ParentLeaseID == "" || opts.ParentFence == 0 {
		return nil, fmt.Errorf("driver run %q owner credentials required: %w", opts.DriverRunID, domain.ErrNotOwner)
	}
	parent, err = s.DriverRuns().Heartbeat(ctx, opts.WorkspaceKey, opts.DriverRunID, opts.ParentNodeID, opts.ParentLeaseID, opts.ParentFence)
	if err != nil {
		return nil, fmt.Errorf("verify parent driver run owner: %w", err)
	}
	return parent, nil
}

func newTaskRunRequestRefs(opts TaskRunRequestOptions, parent *domain.DriverRun) taskRunRequestRefs {
	refs := taskRunRequestRefs{
		NodeID:     firstNonEmpty(opts.NodeID, parent.NodeID),
		TaskRunID:  opts.TaskRunID,
		LeaseID:    opts.LeaseID,
		LeaseToken: opts.LeaseToken,
	}
	if refs.TaskRunID == "" {
		refs.TaskRunID = generatedTaskRunID(opts.DriverRunID, opts.TaskID)
	}
	if refs.LeaseID == "" {
		refs.LeaseID = refs.TaskRunID + "-lease"
	}
	if refs.LeaseToken == "" {
		refs.LeaseToken = generatedTaskRunLeaseToken()
	}
	return refs
}

func createQueuedTaskRun(ctx context.Context, s store.Store, opts TaskRunRequestOptions, refs taskRunRequestRefs) (*domain.TaskRun, error) {
	runtimeMetadata := map[string]string{
		"driver_run_id": opts.DriverRunID,
		"requested_by":  "driver",
	}
	if opts.Runner != "" {
		runtimeMetadata["runner"] = opts.Runner
	}
	if opts.RunnerRef != "" {
		runtimeMetadata["runner_ref"] = opts.RunnerRef
	}
	if opts.RunnerKind != "" {
		runtimeMetadata["runner_kind"] = opts.RunnerKind
	}
	if opts.RunnerEntrypoint != "" {
		runtimeMetadata["runner_entrypoint"] = opts.RunnerEntrypoint
	}
	if opts.RunnerVersionID != "" {
		runtimeMetadata["runner_driver_version_id"] = opts.RunnerVersionID
	}
	if opts.RunnerTrustLevel != "" {
		runtimeMetadata["runner_trust_level"] = string(opts.RunnerTrustLevel)
	}
	if opts.ParentSessionID != "" {
		runtimeMetadata["parent_session_id"] = opts.ParentSessionID
	}
	if opts.CloseTaskOnSuccess != nil {
		runtimeMetadata[TaskRunCloseOnSuccessMetaKey] = strconv.FormatBool(*opts.CloseTaskOnSuccess)
	}
	return s.TaskRuns().Create(ctx, store.TaskRunCreate{
		WorkspaceKey:     opts.WorkspaceKey,
		TaskRunID:        refs.TaskRunID,
		DriverRunID:      opts.DriverRunID,
		DriverStepID:     opts.DriverStepID,
		TaskID:           opts.TaskID,
		WorkerProfileID:  opts.WorkerProfileID,
		Runner:           opts.Runner,
		RunnerRef:        opts.RunnerRef,
		RunnerKind:       opts.RunnerKind,
		RunnerEntrypoint: opts.RunnerEntrypoint,
		RunnerVersionID:  opts.RunnerVersionID,
		ProviderProfile:  opts.ProviderProfile,
		Status:           domain.TaskRunQueued,
		NodeID:           opts.NodeID,
		RunnerPlacement:  opts.RunnerPlacement,
		SandboxPlacement: opts.SandboxPlacement,
		RuntimeMetadata:  runtimeMetadata,
		Input:            opts.Input,
	})
}

func claimQueuedTaskRunRequest(ctx context.Context, s store.Store, opts TaskRunRequestOptions, queued *domain.TaskRun, refs taskRunRequestRefs) (*domain.TaskRun, error) {
	return s.TaskRuns().ClaimQueued(ctx, opts.WorkspaceKey, store.TaskRunClaim{
		TaskRunID:          queued.TaskRunID,
		NodeID:             refs.NodeID,
		RunnerID:           opts.RunnerID,
		LeaseID:            refs.LeaseID,
		LeaseToken:         refs.LeaseToken,
		SupportedProviders: taskRunSupportedProviders(opts),
		Capabilities:       normalizeStringList(opts.Capabilities),
		WorkerProfileIDs:   taskRunRequestWorkerProfileIDs(opts),
		RunnerPlacement:    opts.RunnerPlacement,
		SandboxPlacement:   opts.SandboxPlacement,
	})
}

func taskRunRequestWorkerProfileIDs(opts TaskRunRequestOptions) []string {
	values := append([]string(nil), opts.WorkerProfileIDs...)
	if opts.WorkerProfileID != "" {
		values = append(values, opts.WorkerProfileID)
	}
	return normalizeStringList(values)
}

func linkTaskRunRequestDriverStep(ctx context.Context, s store.Store, opts TaskRunRequestOptions, claimed *domain.TaskRun) error {
	if opts.DriverStepID == "" {
		return nil
	}
	status := domain.DriverStepRunning
	_, err := s.DriverSteps().Update(ctx, opts.WorkspaceKey, opts.DriverStepID, store.DriverStepUpdate{
		Status:       &status,
		TaskRunID:    &claimed.TaskRunID,
		NodeID:       opts.ParentNodeID,
		LeaseID:      opts.ParentLeaseID,
		FencingToken: opts.ParentFence,
	})
	if err != nil {
		return fmt.Errorf("link driver step: %w", err)
	}
	return nil
}

func linkQueuedTaskRunRequestDriverStep(ctx context.Context, s store.Store, opts TaskRunRequestOptions, queued *domain.TaskRun) error {
	if opts.DriverStepID == "" {
		return nil
	}
	status := domain.DriverStepQueued
	_, err := s.DriverSteps().Update(ctx, opts.WorkspaceKey, opts.DriverStepID, store.DriverStepUpdate{
		Status:       &status,
		TaskRunID:    &queued.TaskRunID,
		NodeID:       opts.ParentNodeID,
		LeaseID:      opts.ParentLeaseID,
		FencingToken: opts.ParentFence,
	})
	if err != nil {
		return fmt.Errorf("link queued driver step: %w", err)
	}
	return nil
}

func executeClaimedTaskRunWithResult(ctx context.Context, s store.Store, claimed *domain.TaskRun, opts executeClaimedTaskRunOptions, executor TaskExecutor) (*TaskRunRequestOutcome, error) {
	if claimed == nil {
		return nil, fmt.Errorf("claimed task run required: %w", domain.ErrInvalid)
	}
	if executor == nil {
		executor = LocalTaskExecutor{}
	}
	refs := claimedTaskRunRefsFromOptions(claimed, opts)
	evctx := taskRunEventContext{EpicID: taskRunEpicID(ctx, s, claimed)}
	appendTaskRunEvent(ctx, s, claimed, domain.TaskRunEventClaimed, taskExecCompletion{}, evctx)
	stopHeartbeat := startClaimedTaskRunHeartbeat(ctx, s, claimed, opts, refs)
	defer stopHeartbeat()

	execResult, execErr := executor.ExecuteTask(ctx, taskExecRequest(claimed, opts, refs))
	completion := normalizeTaskExecCompletion(execResult, execErr)
	metadata := taskExecRuntimeMetadata(execResult, refs)
	if opts.DeferCompletion && completion.Status == domain.TaskRunCompleted {
		return deferClaimedTaskRunCompletion(ctx, s, claimed, opts, execResult, completion, metadata)
	}
	closeTaskOnSuccess := resolveCloseTaskOnSuccess(opts.CloseTaskOnSuccess, claimed.RuntimeMetadata)
	if closeTaskOnSuccess && completion.Status == domain.TaskRunCompleted {
		return completeAndCloseClaimedTaskRun(ctx, s, claimed, opts, refs, execResult, completion, metadata, evctx)
	}
	if retryTaskRun := taskRunRetryDecision(claimed, opts, completion); retryTaskRun.Retry {
		requeued, err := requeueClaimedTaskRun(ctx, s, claimed, opts, execResult, completion, metadata, retryTaskRun)
		if err != nil {
			return nil, err
		}
		if err := requeueLinkedDriverStep(ctx, s, claimed, requeued); err != nil {
			return nil, err
		}
		return &TaskRunRequestOutcome{Run: requeued, LeaseToken: opts.LeaseToken, ArtifactIDs: normalizeArtifactIDs(execResult.ArtifactIDs)}, nil
	}
	blockTask := completion.Status == domain.TaskRunFailed
	if blockTask {
		metadata = taskRunBlockedMetadata(claimed, opts, completion, metadata)
	}
	final, err := finishClaimedTaskRun(ctx, s, claimed, opts, refs, execResult, completion, metadata, blockTask)
	if err != nil {
		return nil, err
	}
	emitTerminalTaskRunEvents(ctx, s, final, completion, evctx)
	if err := finishLinkedDriverStep(ctx, s, claimed, opts, refs, execResult, completion.Status); err != nil {
		return nil, err
	}
	return &TaskRunRequestOutcome{Run: final, LeaseToken: opts.LeaseToken, ArtifactIDs: normalizeArtifactIDs(execResult.ArtifactIDs)}, nil
}

func startClaimedTaskRunHeartbeat(ctx context.Context, s store.Store, claimed *domain.TaskRun, opts executeClaimedTaskRunOptions, refs claimedTaskRunRefs) context.CancelFunc {
	hbCtx, stopHeartbeat := context.WithCancel(ctx)
	if interval := taskRunHeartbeatInterval(opts.HeartbeatInterval); interval > 0 {
		go heartbeatTaskRun(hbCtx, s, claimed, opts.LeaseToken, interval, map[string]string{
			"driver_run_id":    refs.DriverRunID,
			"runner":           refs.Runner,
			"runner_ref":       refs.RunnerRef,
			"runner_kind":      refs.RunnerKind,
			"provider_profile": refs.ProviderProfile,
			"heartbeat_source": refs.HeartbeatSource,
		})
	}
	return stopHeartbeat
}

func deferClaimedTaskRunCompletion(ctx context.Context, s store.Store, claimed *domain.TaskRun, opts executeClaimedTaskRunOptions, execResult TaskExecResult, completion taskExecCompletion, metadata map[string]string) (*TaskRunRequestOutcome, error) {
	pending, err := s.TaskRuns().Heartbeat(ctx, claimed.WorkspaceKey, claimed.TaskRunID, store.TaskRunHeartbeat{
		NodeID:          claimed.NodeID,
		LeaseID:         claimed.LeaseID,
		LeaseToken:      opts.LeaseToken,
		FencingToken:    claimed.FencingToken,
		RuntimeMetadata: metadata,
		LogsRef:         execResult.LogsRef,
		ArtifactsRef:    execResult.ArtifactsRef,
	})
	if err != nil {
		return nil, fmt.Errorf("record pending task run completion: %w", err)
	}
	synthetic := taskRunSyntheticCompletion(pending, execResult, completion, metadata)
	return &TaskRunRequestOutcome{Run: synthetic, LeaseToken: opts.LeaseToken, ArtifactIDs: normalizeArtifactIDs(execResult.ArtifactIDs)}, nil
}

func taskRunSyntheticCompletion(pending *domain.TaskRun, execResult TaskExecResult, completion taskExecCompletion, metadata map[string]string) *domain.TaskRun {
	synthetic := *pending
	synthetic.Status = completion.Status
	synthetic.ExitCode = &completion.ExitCode
	synthetic.LogsRef = execResult.LogsRef
	synthetic.ArtifactsRef = execResult.ArtifactsRef
	synthetic.InputTokens = execResult.InputTokens
	synthetic.OutputTokens = execResult.OutputTokens
	synthetic.CacheReadTokens = execResult.CacheReadTokens
	synthetic.CacheWriteTokens = execResult.CacheWriteTokens
	synthetic.EstimatedCostUSD = execResult.EstimatedCostUSD
	synthetic.RuntimeMetadata = metadata
	synthetic.ErrorClass = completion.ErrorClass
	synthetic.ErrorMessage = completion.ErrorMessage
	now := time.Now().UTC()
	synthetic.FinishedAt = &now
	return &synthetic
}

func completeAndCloseClaimedTaskRun(ctx context.Context, s store.Store, claimed *domain.TaskRun, opts executeClaimedTaskRunOptions, refs claimedTaskRunRefs, execResult TaskExecResult, completion taskExecCompletion, metadata map[string]string, evctx taskRunEventContext) (*TaskRunRequestOutcome, error) {
	final, err := completeClaimedTaskRun(ctx, s, claimed, opts, refs, execResult, completion, metadata)
	if err != nil {
		return nil, err
	}
	emitTerminalTaskRunEvents(ctx, s, final, completion, evctx)
	if err := finishLinkedDriverStep(ctx, s, claimed, opts, refs, execResult, completion.Status); err != nil {
		return nil, err
	}
	return &TaskRunRequestOutcome{Run: final, LeaseToken: opts.LeaseToken, ArtifactIDs: normalizeArtifactIDs(execResult.ArtifactIDs)}, nil
}

func completeClaimedTaskRun(ctx context.Context, s store.Store, claimed *domain.TaskRun, opts executeClaimedTaskRunOptions, refs claimedTaskRunRefs, execResult TaskExecResult, completion taskExecCompletion, metadata map[string]string) (*domain.TaskRun, error) {
	artifactIDs := normalizeArtifactIDs(execResult.ArtifactIDs)
	final, err := s.TaskRuns().Complete(ctx, refs.WorkspaceKey, claimed.TaskRunID, store.TaskRunComplete{
		CompletionID:        "worker-complete-" + claimed.TaskRunID,
		NodeID:              claimed.NodeID,
		LeaseID:             claimed.LeaseID,
		LeaseToken:          opts.LeaseToken,
		FencingToken:        claimed.FencingToken,
		Status:              completion.Status,
		ExitCode:            &completion.ExitCode,
		LogsRef:             execResult.LogsRef,
		ArtifactsRef:        execResult.ArtifactsRef,
		RequiredArtifactIDs: artifactIDs,
		RequireArtifacts:    len(artifactIDs) > 0,
		InputTokens:         execResult.InputTokens,
		OutputTokens:        execResult.OutputTokens,
		CacheReadTokens:     execResult.CacheReadTokens,
		CacheWriteTokens:    execResult.CacheWriteTokens,
		EstimatedCostUSD:    execResult.EstimatedCostUSD,
		RuntimeMetadata:     metadata,
		ErrorClass:          completion.ErrorClass,
		ErrorMessage:        completion.ErrorMessage,
		CloseTask:           true,
		CloseReason:         "completed by task worker",
	})
	if err != nil {
		return nil, fmt.Errorf("complete task run: %w", err)
	}
	return final, nil
}

func finishClaimedTaskRun(ctx context.Context, s store.Store, claimed *domain.TaskRun, opts executeClaimedTaskRunOptions, refs claimedTaskRunRefs, execResult TaskExecResult, completion taskExecCompletion, metadata map[string]string, blockTask bool) (*domain.TaskRun, error) {
	final, err := s.TaskRuns().Finish(ctx, refs.WorkspaceKey, claimed.TaskRunID, store.TaskRunFinish{
		NodeID:           claimed.NodeID,
		LeaseID:          claimed.LeaseID,
		LeaseToken:       opts.LeaseToken,
		FencingToken:     claimed.FencingToken,
		Status:           completion.Status,
		BlockTask:        blockTask,
		ExitCode:         &completion.ExitCode,
		LogsRef:          execResult.LogsRef,
		ArtifactsRef:     execResult.ArtifactsRef,
		InputTokens:      execResult.InputTokens,
		OutputTokens:     execResult.OutputTokens,
		CacheReadTokens:  execResult.CacheReadTokens,
		CacheWriteTokens: execResult.CacheWriteTokens,
		EstimatedCostUSD: execResult.EstimatedCostUSD,
		RuntimeMetadata:  metadata,
		ErrorClass:       completion.ErrorClass,
		ErrorMessage:     completion.ErrorMessage,
	})
	if err != nil {
		return nil, fmt.Errorf("finish task run: %w", err)
	}
	return final, nil
}

func requeueClaimedTaskRun(ctx context.Context, s store.Store, claimed *domain.TaskRun, opts executeClaimedTaskRunOptions, execResult TaskExecResult, completion taskExecCompletion, metadata map[string]string, retry taskRunRetryDecisionResult) (*domain.TaskRun, error) {
	metadata = taskRunRetryMetadata(claimed, retry, completion, metadata)
	requeued, err := s.TaskRuns().Requeue(ctx, claimed.WorkspaceKey, claimed.TaskRunID, store.TaskRunRequeue{
		NodeID:          claimed.NodeID,
		LeaseID:         claimed.LeaseID,
		LeaseToken:      opts.LeaseToken,
		FencingToken:    claimed.FencingToken,
		RuntimeMetadata: metadata,
		LogsRef:         execResult.LogsRef,
		ArtifactsRef:    execResult.ArtifactsRef,
		ErrorClass:      completion.ErrorClass,
		ErrorMessage:    completion.ErrorMessage,
		NextEligibleAt:  taskRunNow(opts.Now).Add(taskRunRetryBackoff(retry.Attempt)),
	})
	if err != nil {
		return nil, fmt.Errorf("requeue task run: %w", err)
	}
	appendTaskRunEvent(ctx, s, requeued, domain.TaskRunEventRequeued, completion, taskRunEventContext{
		EpicID: taskRunEpicID(ctx, s, requeued),
	})
	return requeued, nil
}

func requeueLinkedDriverStep(ctx context.Context, s store.Store, claimed, requeued *domain.TaskRun) error {
	if claimed == nil || requeued == nil || claimed.DriverStepID == "" {
		return nil
	}
	parent, err := s.DriverRuns().Get(ctx, requeued.WorkspaceKey, requeued.DriverRunID)
	if err != nil {
		return fmt.Errorf("get parent driver run for requeued task step update: %w", err)
	}
	if parent.Status != domain.DriverRunRunning {
		return nil
	}
	status := domain.DriverStepQueued
	outputRef := firstNonEmpty(requeued.ArtifactsRef, requeued.LogsRef)
	_, err = s.DriverSteps().Update(ctx, requeued.WorkspaceKey, claimed.DriverStepID, store.DriverStepUpdate{
		Status:       &status,
		TaskRunID:    &requeued.TaskRunID,
		OutputRef:    &outputRef,
		NodeID:       parent.NodeID,
		LeaseID:      parent.LeaseID,
		FencingToken: parent.FencingToken,
	})
	if err != nil {
		return fmt.Errorf("update linked driver step from requeued task run: %w", err)
	}
	return nil
}

func finishLinkedDriverStep(ctx context.Context, s store.Store, claimed *domain.TaskRun, opts executeClaimedTaskRunOptions, refs claimedTaskRunRefs, execResult TaskExecResult, status domain.TaskRunStatus) error {
	if !opts.UpdateDriverStep || refs.DriverStepID == "" {
		return nil
	}
	stepStatus := driverStepStatusForTaskRun(status)
	outputRef := firstNonEmpty(execResult.ArtifactsRef, execResult.LogsRef)
	_, err := s.DriverSteps().Update(ctx, refs.WorkspaceKey, refs.DriverStepID, store.DriverStepUpdate{
		Status:       &stepStatus,
		TaskRunID:    &claimed.TaskRunID,
		OutputRef:    &outputRef,
		NodeID:       opts.ParentNodeID,
		LeaseID:      opts.ParentLeaseID,
		FencingToken: opts.ParentFence,
	})
	if err != nil {
		return fmt.Errorf("finish driver step: %w", err)
	}
	return nil
}

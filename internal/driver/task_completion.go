package driver

import (
	"context"
	"fmt"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type claimedTaskRunRefs struct {
	WorkspaceKey     string
	DriverRunID      string
	DriverStepID     string
	TaskID           string
	Runner           string
	RunnerRef        string
	RunnerKind       string
	RunnerEntrypoint string
	RunnerVersionID  string
	RunnerTrustLevel domain.DriverTrustLevel
	ProviderProfile  string
	ParentSessionID  string
	HeartbeatSource  string
}

type taskExecCompletion struct {
	Status       domain.TaskRunStatus
	ExitCode     int
	ErrorClass   string
	ErrorMessage string
}

func claimedTaskRunRefsFromOptions(claimed *domain.TaskRun, opts executeClaimedTaskRunOptions) claimedTaskRunRefs {
	return claimedTaskRunRefs{
		WorkspaceKey:     firstNonEmpty(opts.WorkspaceKey, claimed.WorkspaceKey),
		DriverRunID:      firstNonEmpty(opts.DriverRunID, claimed.DriverRunID),
		DriverStepID:     firstNonEmpty(opts.DriverStepID, claimed.DriverStepID),
		TaskID:           firstNonEmpty(opts.TaskID, claimed.TaskID),
		Runner:           claimed.Runner,
		RunnerRef:        claimed.RunnerRef,
		RunnerKind:       claimed.RunnerKind,
		RunnerEntrypoint: claimed.RunnerEntrypoint,
		RunnerVersionID:  claimed.RunnerVersionID,
		RunnerTrustLevel: domain.DriverTrustLevel(firstNonEmpty(string(opts.RunnerTrustLevel), claimed.RuntimeMetadata["runner_trust_level"])),
		ProviderProfile:  firstNonEmpty(opts.ProviderProfile, claimed.ProviderProfile),
		ParentSessionID:  firstNonEmpty(opts.ParentSessionID, claimed.RuntimeMetadata["parent_session_id"]),
		HeartbeatSource:  firstNonEmpty(opts.HeartbeatSource, "task_run_executor"),
	}
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

func taskExecRequest(claimed *domain.TaskRun, opts executeClaimedTaskRunOptions, refs claimedTaskRunRefs) TaskExecRequest {
	return TaskExecRequest{
		WorkspaceKey:     refs.WorkspaceKey,
		DriverRunID:      refs.DriverRunID,
		DriverStepID:     refs.DriverStepID,
		TaskRunID:        claimed.TaskRunID,
		TaskID:           refs.TaskID,
		WorkerProfileID:  claimed.WorkerProfileID,
		Runner:           refs.Runner,
		RunnerRef:        refs.RunnerRef,
		RunnerKind:       refs.RunnerKind,
		RunnerEntrypoint: refs.RunnerEntrypoint,
		RunnerVersionID:  refs.RunnerVersionID,
		RunnerTrustLevel: refs.RunnerTrustLevel,
		ProviderProfile:  refs.ProviderProfile,
		ParentSessionID:  refs.ParentSessionID,
		NodeID:           claimed.NodeID,
		LeaseID:          claimed.LeaseID,
		LeaseToken:       opts.LeaseToken,
		FencingToken:     claimed.FencingToken,
		RunnerPlacement:  claimed.RunnerPlacement,
		SandboxPlacement: claimed.SandboxPlacement,
		Input:            claimed.Input,
	}
}

func normalizeTaskExecCompletion(execResult TaskExecResult, execErr error) taskExecCompletion {
	completion := taskExecCompletion{
		Status:       execResult.Status,
		ExitCode:     execResult.ExitCode,
		ErrorClass:   execResult.ErrorClass,
		ErrorMessage: execResult.ErrorMessage,
	}
	if execErr != nil {
		completion.applyExecutorError(execErr)
	}
	completion.requireTerminalStatus()
	return completion
}

func (c *taskExecCompletion) applyExecutorError(execErr error) {
	if c.ExitCode == 0 {
		c.ExitCode = 1
	}
	if c.ErrorClass == "" {
		c.ErrorClass = "task_executor_error"
	}
	if c.ErrorMessage == "" {
		c.ErrorMessage = execErr.Error()
	}
}

func (c *taskExecCompletion) requireTerminalStatus() {
	switch {
	case c.Status == "":
		c.markInvalidResult("task executor result missing terminal status")
	case !c.Status.IsTerminal():
		c.markInvalidResult(fmt.Sprintf("task executor result status %q is not terminal", c.Status))
	case c.Status == domain.TaskRunCompleted && c.ExitCode != 0:
		c.markInvalidResult(fmt.Sprintf("task executor reported completed with non-zero exit code %d", c.ExitCode))
	}
}

func (c *taskExecCompletion) markInvalidResult(message string) {
	c.Status = domain.TaskRunFailed
	if c.ExitCode == 0 {
		c.ExitCode = 1
	}
	if c.ErrorClass == "" {
		c.ErrorClass = "invalid_task_result"
	}
	if c.ErrorMessage == "" {
		c.ErrorMessage = message
	}
}

func taskExecRuntimeMetadata(execResult TaskExecResult, refs claimedTaskRunRefs) map[string]string {
	metadata := cloneStringMap(execResult.RuntimeMetadata)
	if metadata == nil {
		metadata = map[string]string{}
	}
	if refs.DriverRunID != "" {
		metadata["driver_run_id"] = refs.DriverRunID
	}
	if refs.DriverStepID != "" {
		metadata["driver_step_id"] = refs.DriverStepID
	}
	if refs.ParentSessionID != "" {
		metadata["parent_session_id"] = refs.ParentSessionID
	}
	if refs.Runner != "" {
		metadata["runner"] = refs.Runner
	}
	if refs.RunnerRef != "" {
		metadata["runner_ref"] = refs.RunnerRef
	}
	if refs.RunnerKind != "" {
		metadata["runner_kind"] = refs.RunnerKind
	}
	if refs.RunnerEntrypoint != "" {
		metadata["runner_entrypoint"] = refs.RunnerEntrypoint
	}
	if refs.RunnerVersionID != "" {
		metadata["runner_driver_version_id"] = refs.RunnerVersionID
	}
	if refs.RunnerTrustLevel != "" {
		metadata["runner_trust_level"] = string(refs.RunnerTrustLevel)
	}
	metadata["provider_profile"] = refs.ProviderProfile
	metadata["task_run_executor"] = refs.HeartbeatSource
	return metadata
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

// completeAndCloseClaimedTaskRun is the CloseTaskOnSuccess branch of
// executeClaimedTaskRunWithResult: complete the run (closing the task),
// emit the terminal journal event + lead outbox row, and finish the
// linked driver step.
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

// finishClaimedTaskRun records the terminal state of a claimed run. blockTask
// additionally marks the underlying task issue blocked server-side (fenced by
// the same lease/fencing checks, idempotent, best-effort like the other
// policy hooks); pass it only for failed runs whose retry budget is
// exhausted.
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

package driver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type TaskRunRequestOptions struct {
	WorkspaceKey       string
	DriverRunID        string
	DriverStepID       string
	TaskRunID          string
	TaskID             string
	WorkerProfileID    string
	ProviderProfile    string
	ParentSessionID    string
	ParentNodeID       string
	ParentLeaseID      string
	ParentFence        int64
	NodeID             string
	RunnerID           string
	LeaseID            string
	LeaseToken         string
	SupportedProviders []string
	Capabilities       []string
	WorkerProfileIDs   []string
	RunnerPlacement    domain.TaskRunPlacement
	SandboxPlacement   domain.TaskRunPlacement
	HeartbeatInterval  time.Duration
	DeferCompletion    bool
	// Input is the optional task-run payload (e.g. a review diff+rubric)
	// persisted on the run and delivered to the runner.
	Input json.RawMessage
}

type TaskRunWorkerOptions struct {
	WorkspaceKey       string
	TaskRunID          string
	NodeID             string
	RunnerID           string
	LeaseID            string
	LeaseToken         string
	SupportedProviders []string
	Capabilities       []string
	WorkerProfileIDs   []string
	RunnerPlacement    domain.TaskRunPlacement
	SandboxPlacement   domain.TaskRunPlacement
	HeartbeatInterval  time.Duration
	DeferCompletion    bool
	CloseTaskOnSuccess bool
	MaxAttempts        int
	// Now is a clock seam for tests; nil uses time.Now.
	Now func() time.Time
}

type TaskExecRequest struct {
	WorkspaceKey     string                  `json:"workspace_key"`
	DriverRunID      string                  `json:"driver_run_id"`
	DriverStepID     string                  `json:"driver_step_id,omitempty"`
	TaskRunID        string                  `json:"task_run_id"`
	TaskID           string                  `json:"task_id"`
	WorkerProfileID  string                  `json:"worker_profile_id,omitempty"`
	ProviderProfile  string                  `json:"provider_profile,omitempty"`
	ParentSessionID  string                  `json:"parent_session_id,omitempty"`
	NodeID           string                  `json:"node_id,omitempty"`
	LeaseID          string                  `json:"lease_id,omitempty"`
	LeaseToken       string                  `json:"lease_token,omitempty"`
	FencingToken     int64                   `json:"fencing_token,omitempty"`
	RunnerPlacement  domain.TaskRunPlacement `json:"runner_placement,omitempty"`
	SandboxPlacement domain.TaskRunPlacement `json:"sandbox_placement,omitempty"`
	// Input is the task-run payload delivered verbatim to the runner via
	// LOOM_TASK_RUN_REQUEST_JSON. Optional (omitempty) for back-compat.
	Input json.RawMessage `json:"input,omitempty"`
}

type TaskExecResult struct {
	Status           domain.TaskRunStatus
	ExitCode         int
	LogsRef          string
	ArtifactsRef     string
	ArtifactIDs      []string
	InputTokens      int64
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	EstimatedCostUSD float64
	RuntimeMetadata  map[string]string
	ErrorClass       string
	ErrorMessage     string
}

type TaskExecutor interface {
	ExecuteTask(ctx context.Context, req TaskExecRequest) (TaskExecResult, error)
}

type TaskProviderPreflighter interface {
	PreflightTaskProvider(ctx context.Context, opts TaskRunRequestOptions) (TaskRunRequestOptions, error)
}

type LocalTaskExecutor struct{}

func (LocalTaskExecutor) PreflightTaskProvider(_ context.Context, opts TaskRunRequestOptions) (TaskRunRequestOptions, error) {
	return resolveTaskProviderProfile(opts, false)
}

func (LocalTaskExecutor) ExecuteTask(_ context.Context, req TaskExecRequest) (TaskExecResult, error) {
	metadata := map[string]string{
		"executor":         "local-exec-task",
		"provider_profile": req.ProviderProfile,
	}
	switch strings.TrimSpace(req.ProviderProfile) {
	case "local-noop", "noop":
		return TaskExecResult{
			Status:          domain.TaskRunCompleted,
			ExitCode:        0,
			LogsRef:         "task-run://" + req.TaskRunID + "/logs",
			RuntimeMetadata: metadata,
		}, nil
	default:
		profile := req.ProviderProfile
		if strings.TrimSpace(profile) == "" {
			profile = "<empty>"
		}
		return TaskExecResult{
			Status:          domain.TaskRunFailed,
			ExitCode:        2,
			LogsRef:         "task-run://" + req.TaskRunID + "/logs",
			RuntimeMetadata: metadata,
			ErrorClass:      "provider_unsupported",
			ErrorMessage:    fmt.Sprintf("provider profile %q is not supported by local exec-task yet", profile),
		}, nil
	}
}

type TaskRunRequestResult struct {
	ID               string               `json:"id"`
	TaskRunID        string               `json:"taskRunId,omitempty"`
	LeaseToken       string               `json:"leaseToken,omitempty"`
	DriverStepID     string               `json:"driverStepId,omitempty"`
	TaskID           string               `json:"taskId"`
	Status           domain.TaskRunStatus `json:"status"`
	ExitCode         *int                 `json:"exitCode,omitempty"`
	LogsRef          string               `json:"logsRef,omitempty"`
	ArtifactsRef     string               `json:"artifactsRef,omitempty"`
	ArtifactIDs      []string             `json:"artifactIds,omitempty"`
	InputTokens      int64                `json:"inputTokens,omitempty"`
	OutputTokens     int64                `json:"outputTokens,omitempty"`
	CacheReadTokens  int64                `json:"cacheReadTokens,omitempty"`
	CacheWriteTokens int64                `json:"cacheWriteTokens,omitempty"`
	EstimatedCostUSD float64              `json:"estimatedCostUsd,omitempty"`
	ErrorClass       string               `json:"errorClass,omitempty"`
	ErrorMessage     string               `json:"errorMessage,omitempty"`
	FinishedAt       *time.Time           `json:"finishedAt,omitempty"`
	ProviderProfile  string               `json:"providerProfile,omitempty"`
	// RuntimeMetadata surfaces the runner's runtimeMetadata to the awaiting
	// workflow (e.g. the github-review-agent reads review_findings off it).
	// The task-run-get op must carry it through, else a completed run looks
	// empty to the driver.
	RuntimeMetadata map[string]string `json:"runtimeMetadata,omitempty"`
}

type TaskRunRequestOutcome struct {
	Run         *domain.TaskRun
	LeaseToken  string
	ArtifactIDs []string
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
	resolved, err := preflightTaskRunRequest(ctx, opts, preflighter)
	if err != nil {
		return nil, err
	}
	opts = resolved
	parent, err := verifyTaskRunRequestParent(ctx, s, opts)
	if err != nil {
		return nil, err
	}
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
	resolved, err := preflightTaskRunRequest(ctx, opts, executor)
	if err != nil {
		return nil, err
	}
	opts = resolved
	parent, err := verifyTaskRunRequestParent(ctx, s, opts)
	if err != nil {
		return nil, err
	}
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
	return executeClaimedTaskRunWithResult(ctx, s, claimed, executeClaimedTaskRunOptions{
		WorkspaceKey:      opts.WorkspaceKey,
		DriverRunID:       opts.DriverRunID,
		DriverStepID:      opts.DriverStepID,
		TaskID:            opts.TaskID,
		ProviderProfile:   opts.ProviderProfile,
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

func normalizeTaskRunRequestOptions(opts TaskRunRequestOptions) TaskRunRequestOptions {
	opts.WorkspaceKey = strings.TrimSpace(opts.WorkspaceKey)
	opts.DriverRunID = strings.TrimSpace(opts.DriverRunID)
	opts.DriverStepID = strings.TrimSpace(opts.DriverStepID)
	opts.TaskRunID = strings.TrimSpace(opts.TaskRunID)
	opts.TaskID = strings.TrimSpace(opts.TaskID)
	opts.WorkerProfileID = strings.TrimSpace(opts.WorkerProfileID)
	opts.ProviderProfile = strings.TrimSpace(opts.ProviderProfile)
	opts.ParentSessionID = strings.TrimSpace(opts.ParentSessionID)
	opts.ParentNodeID = strings.TrimSpace(opts.ParentNodeID)
	opts.ParentLeaseID = strings.TrimSpace(opts.ParentLeaseID)
	opts.NodeID = strings.TrimSpace(opts.NodeID)
	opts.RunnerID = strings.TrimSpace(opts.RunnerID)
	opts.LeaseID = strings.TrimSpace(opts.LeaseID)
	opts.LeaseToken = strings.TrimSpace(opts.LeaseToken)
	return opts
}

func validateTaskRunRequestOptions(opts TaskRunRequestOptions) error {
	if opts.WorkspaceKey == "" || opts.DriverRunID == "" || opts.TaskID == "" {
		return fmt.Errorf("workspace key, driver run id, and task id required: %w", domain.ErrInvalid)
	}
	return nil
}

func preflightTaskRunRequest(ctx context.Context, opts TaskRunRequestOptions, candidate any) (TaskRunRequestOptions, error) {
	preflighter, ok := candidate.(TaskProviderPreflighter)
	if !ok {
		return opts, nil
	}
	resolved, err := preflighter.PreflightTaskProvider(ctx, opts)
	if err != nil {
		return opts, fmt.Errorf("provider profile preflight: %w", err)
	}
	return resolved, nil
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
	if opts.ParentSessionID != "" {
		runtimeMetadata["parent_session_id"] = opts.ParentSessionID
	}
	return s.TaskRuns().Create(ctx, store.TaskRunCreate{
		WorkspaceKey:     opts.WorkspaceKey,
		TaskRunID:        refs.TaskRunID,
		DriverRunID:      opts.DriverRunID,
		DriverStepID:     opts.DriverStepID,
		TaskID:           opts.TaskID,
		WorkerProfileID:  opts.WorkerProfileID,
		ProviderProfile:  opts.ProviderProfile,
		Status:           domain.TaskRunQueued,
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

type executeClaimedTaskRunOptions struct {
	WorkspaceKey       string
	DriverRunID        string
	DriverStepID       string
	TaskID             string
	ProviderProfile    string
	ParentSessionID    string
	LeaseToken         string
	HeartbeatInterval  time.Duration
	DeferCompletion    bool
	CloseTaskOnSuccess bool
	MaxAttempts        int
	UpdateDriverStep   bool
	ParentNodeID       string
	ParentLeaseID      string
	ParentFence        int64
	HeartbeatSource    string
	// Now is a clock seam for tests; nil uses time.Now.
	Now func() time.Time
}

func executeClaimedTaskRunWithResult(ctx context.Context, s store.Store, claimed *domain.TaskRun, opts executeClaimedTaskRunOptions, executor TaskExecutor) (*TaskRunRequestOutcome, error) {
	if claimed == nil {
		return nil, fmt.Errorf("claimed task run required: %w", domain.ErrInvalid)
	}
	if executor == nil {
		executor = LocalTaskExecutor{}
	}
	refs := claimedTaskRunRefsFromOptions(claimed, opts)
	evctx := taskRunEventContext{EpicID: taskRunEpicID(ctx, s, claimed), LeaseToken: opts.LeaseToken}
	appendTaskRunEvent(ctx, s, claimed, domain.TaskRunEventClaimed, taskExecCompletion{}, evctx)
	stopHeartbeat := startClaimedTaskRunHeartbeat(ctx, s, claimed, opts, refs)
	defer stopHeartbeat()

	execResult, execErr := executor.ExecuteTask(ctx, taskExecRequest(claimed, opts, refs))
	completion := normalizeTaskExecCompletion(execResult, execErr)
	metadata := taskExecRuntimeMetadata(execResult, refs)
	if opts.DeferCompletion && completion.Status == domain.TaskRunCompleted {
		return deferClaimedTaskRunCompletion(ctx, s, claimed, opts, execResult, completion, metadata)
	}
	if opts.CloseTaskOnSuccess && completion.Status == domain.TaskRunCompleted {
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
	// Terminal failure past the retry budget: park the run and mark the
	// underlying task issue parked in the same fenced finish call.
	parkTask := completion.Status == domain.TaskRunFailed
	if parkTask {
		metadata = taskRunParkedMetadata(claimed, opts, completion, metadata)
	}
	final, err := finishClaimedTaskRun(ctx, s, claimed, opts, refs, execResult, completion, metadata, parkTask)
	if err != nil {
		return nil, err
	}
	emitTerminalTaskRunEvents(ctx, s, final, completion, evctx)
	if err := finishLinkedDriverStep(ctx, s, claimed, opts, refs, execResult, completion.Status); err != nil {
		return nil, err
	}
	return &TaskRunRequestOutcome{Run: final, LeaseToken: opts.LeaseToken, ArtifactIDs: normalizeArtifactIDs(execResult.ArtifactIDs)}, nil
}

type claimedTaskRunRefs struct {
	WorkspaceKey    string
	DriverRunID     string
	DriverStepID    string
	TaskID          string
	ProviderProfile string
	ParentSessionID string
	HeartbeatSource string
}

type taskExecCompletion struct {
	Status       domain.TaskRunStatus
	ExitCode     int
	ErrorClass   string
	ErrorMessage string
}

func claimedTaskRunRefsFromOptions(claimed *domain.TaskRun, opts executeClaimedTaskRunOptions) claimedTaskRunRefs {
	return claimedTaskRunRefs{
		WorkspaceKey:    firstNonEmpty(opts.WorkspaceKey, claimed.WorkspaceKey),
		DriverRunID:     firstNonEmpty(opts.DriverRunID, claimed.DriverRunID),
		DriverStepID:    firstNonEmpty(opts.DriverStepID, claimed.DriverStepID),
		TaskID:          firstNonEmpty(opts.TaskID, claimed.TaskID),
		ProviderProfile: firstNonEmpty(opts.ProviderProfile, claimed.ProviderProfile),
		ParentSessionID: firstNonEmpty(opts.ParentSessionID, claimed.RuntimeMetadata["parent_session_id"]),
		HeartbeatSource: firstNonEmpty(opts.HeartbeatSource, "task_run_executor"),
	}
}

func startClaimedTaskRunHeartbeat(ctx context.Context, s store.Store, claimed *domain.TaskRun, opts executeClaimedTaskRunOptions, refs claimedTaskRunRefs) context.CancelFunc {
	hbCtx, stopHeartbeat := context.WithCancel(ctx)
	if interval := taskRunHeartbeatInterval(opts.HeartbeatInterval); interval > 0 {
		go heartbeatTaskRun(hbCtx, s, claimed, opts.LeaseToken, interval, map[string]string{
			"driver_run_id":    refs.DriverRunID,
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

// finishClaimedTaskRun records the terminal state of a claimed run. parkTask
// additionally marks the underlying task issue parked server-side (fenced by
// the same lease/fencing checks, idempotent, best-effort like the other
// policy hooks); pass it only for failed runs whose retry budget is
// exhausted.
func finishClaimedTaskRun(ctx context.Context, s store.Store, claimed *domain.TaskRun, opts executeClaimedTaskRunOptions, refs claimedTaskRunRefs, execResult TaskExecResult, completion taskExecCompletion, metadata map[string]string, parkTask bool) (*domain.TaskRun, error) {
	final, err := s.TaskRuns().Finish(ctx, refs.WorkspaceKey, claimed.TaskRunID, store.TaskRunFinish{
		NodeID:           claimed.NodeID,
		LeaseID:          claimed.LeaseID,
		LeaseToken:       opts.LeaseToken,
		FencingToken:     claimed.FencingToken,
		Status:           completion.Status,
		ParkTask:         parkTask,
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
		ProviderProfile:  run.ProviderProfile,
		RuntimeMetadata:  cloneStringMap(run.RuntimeMetadata),
	}
}

func TaskRunResultFromOutcome(outcome *TaskRunRequestOutcome) TaskRunRequestResult {
	if outcome == nil {
		return TaskRunRequestResult{}
	}
	result := TaskRunResultFromDomain(outcome.Run, outcome.ArtifactIDs)
	result.LeaseToken = outcome.LeaseToken
	return result
}

func normalizeArtifactIDs(ids []string) []string {
	if len(ids) == 0 {
		return nil
	}
	out := make([]string, 0, len(ids))
	seen := map[string]struct{}{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func taskRunSupportedProviders(opts TaskRunRequestOptions) []string {
	values := append([]string(nil), opts.SupportedProviders...)
	values = append(values, opts.SandboxPlacement.Provider)
	return normalizeStringList(values)
}

func taskRunWorkerSupportedProviders(opts TaskRunWorkerOptions) []string {
	values := append([]string(nil), opts.SupportedProviders...)
	values = append(values, opts.SandboxPlacement.Provider)
	return normalizeStringList(values)
}

func resolveTaskProviderProfile(opts TaskRunRequestOptions, hostBridgeAvailable bool) (TaskRunRequestOptions, error) {
	profile := strings.TrimSpace(opts.ProviderProfile)
	opts.ProviderProfile = profile
	switch profile {
	case "local-noop", "noop":
		opts.ProviderProfile = "local-noop"
		if opts.SandboxPlacement.Provider == "" {
			opts.SandboxPlacement.Provider = "local-noop"
		}
		opts.SupportedProviders = append(opts.SupportedProviders, "local-noop", "noop")
		return opts, nil
	case "flue-daytona":
		if !hostBridgeAvailable {
			return opts, fmt.Errorf("provider profile %q requires a configured task runner command: %w", profile, domain.ErrInvalid)
		}
		if opts.RunnerPlacement.Provider == "" {
			opts.RunnerPlacement.Provider = "flue"
		}
		if opts.SandboxPlacement.Provider == "" {
			opts.SandboxPlacement.Provider = "daytona"
		}
		opts.SupportedProviders = append(opts.SupportedProviders, "daytona")
		return opts, nil
	case "":
		return opts, fmt.Errorf("provider profile required: %w", domain.ErrInvalid)
	default:
		if !hostBridgeAvailable {
			return opts, fmt.Errorf("provider profile %q is not supported by local exec-task: %w", profile, domain.ErrInvalid)
		}
		providers := normalizeStringList(opts.SupportedProviders)
		if opts.SandboxPlacement.Provider == "" {
			switch len(providers) {
			case 0:
				return opts, fmt.Errorf("provider profile %q requires --sandbox-provider or --supported-provider: %w", profile, domain.ErrInvalid)
			case 1:
				opts.SandboxPlacement.Provider = providers[0]
			default:
				return opts, fmt.Errorf("provider profile %q has multiple supported providers; --sandbox-provider is required: %w", profile, domain.ErrInvalid)
			}
		}
		return opts, nil
	}
}

func normalizeStringList(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func generatedTaskRunID(driverRunID, taskID string) string {
	runPart := slug(driverRunID)
	if runPart == "" {
		runPart = "run"
	}
	taskPart := slug(taskID)
	if taskPart == "" {
		taskPart = "task"
	}
	return fmt.Sprintf("task-run-%s-%s-%d", runPart, taskPart, time.Now().UTC().UnixNano())
}

func generatedTaskRunLeaseToken() string {
	var b [24]byte
	if _, err := rand.Read(b[:]); err == nil {
		return hex.EncodeToString(b[:])
	}
	return fmt.Sprintf("task-run-token-%d", time.Now().UTC().UnixNano())
}

func generatedTaskRunLeaseID(nodeID string) string {
	nodePart := slug(nodeID)
	if nodePart == "" {
		nodePart = "worker"
	}
	return fmt.Sprintf("task-run-lease-%s-%d", nodePart, time.Now().UTC().UnixNano())
}

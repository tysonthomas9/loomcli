package driver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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
}

type TaskExecRequest struct {
	WorkspaceKey     string                  `json:"workspace_key"`
	DriverRunID      string                  `json:"driver_run_id"`
	DriverStepID     string                  `json:"driver_step_id,omitempty"`
	TaskRunID        string                  `json:"task_run_id"`
	TaskID           string                  `json:"task_id"`
	WorkerProfileID  string                  `json:"worker_profile_id,omitempty"`
	ProviderProfile  string                  `json:"provider_profile,omitempty"`
	NodeID           string                  `json:"node_id,omitempty"`
	LeaseID          string                  `json:"lease_id,omitempty"`
	LeaseToken       string                  `json:"lease_token,omitempty"`
	FencingToken     int64                   `json:"fencing_token,omitempty"`
	RunnerPlacement  domain.TaskRunPlacement `json:"runner_placement,omitempty"`
	SandboxPlacement domain.TaskRunPlacement `json:"sandbox_placement,omitempty"`
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
	})
	if err != nil {
		return nil, fmt.Errorf("claim queued task run: %w", err)
	}
	return executeClaimedTaskRunWithResult(ctx, s, claimed, executeClaimedTaskRunOptions{
		WorkspaceKey:      opts.WorkspaceKey,
		LeaseToken:        leaseToken,
		HeartbeatInterval: opts.HeartbeatInterval,
		DeferCompletion:   opts.DeferCompletion,
		HeartbeatSource:   "task_run_worker",
	}, executor)
}

func RequestTaskRunWithResult(ctx context.Context, s store.Store, opts TaskRunRequestOptions, executor TaskExecutor) (*TaskRunRequestOutcome, error) {
	if s == nil {
		return nil, fmt.Errorf("store required: %w", domain.ErrInvalid)
	}
	opts.WorkspaceKey = strings.TrimSpace(opts.WorkspaceKey)
	opts.DriverRunID = strings.TrimSpace(opts.DriverRunID)
	opts.DriverStepID = strings.TrimSpace(opts.DriverStepID)
	opts.TaskRunID = strings.TrimSpace(opts.TaskRunID)
	opts.TaskID = strings.TrimSpace(opts.TaskID)
	opts.WorkerProfileID = strings.TrimSpace(opts.WorkerProfileID)
	opts.ProviderProfile = strings.TrimSpace(opts.ProviderProfile)
	opts.ParentNodeID = strings.TrimSpace(opts.ParentNodeID)
	opts.ParentLeaseID = strings.TrimSpace(opts.ParentLeaseID)
	opts.NodeID = strings.TrimSpace(opts.NodeID)
	opts.RunnerID = strings.TrimSpace(opts.RunnerID)
	opts.LeaseID = strings.TrimSpace(opts.LeaseID)
	opts.LeaseToken = strings.TrimSpace(opts.LeaseToken)
	if opts.WorkspaceKey == "" || opts.DriverRunID == "" || opts.TaskID == "" {
		return nil, fmt.Errorf("workspace key, driver run id, and task id required: %w", domain.ErrInvalid)
	}
	if executor == nil {
		executor = LocalTaskExecutor{}
	}
	if preflighter, ok := executor.(TaskProviderPreflighter); ok {
		resolved, err := preflighter.PreflightTaskProvider(ctx, opts)
		if err != nil {
			return nil, fmt.Errorf("provider profile preflight: %w", err)
		}
		opts = resolved
	}
	parent, err := s.DriverRuns().Get(ctx, opts.WorkspaceKey, opts.DriverRunID)
	if err != nil {
		return nil, fmt.Errorf("get parent driver run: %w", err)
	}
	if parent.Status != domain.DriverRunRunning {
		return nil, fmt.Errorf("driver run %q is %s, want running: %w", opts.DriverRunID, parent.Status, domain.ErrInvalidTransition)
	}
	if parent.LeaseID != "" || parent.FencingToken != 0 {
		if opts.ParentNodeID == "" || opts.ParentLeaseID == "" || opts.ParentFence == 0 {
			return nil, fmt.Errorf("driver run %q owner credentials required: %w", opts.DriverRunID, domain.ErrNotOwner)
		}
		parent, err = s.DriverRuns().Heartbeat(ctx, opts.WorkspaceKey, opts.DriverRunID, opts.ParentNodeID, opts.ParentLeaseID, opts.ParentFence)
		if err != nil {
			return nil, fmt.Errorf("verify parent driver run owner: %w", err)
		}
	}
	nodeID := opts.NodeID
	if nodeID == "" {
		nodeID = parent.NodeID
	}
	taskRunID := opts.TaskRunID
	if taskRunID == "" {
		taskRunID = generatedTaskRunID(opts.DriverRunID, opts.TaskID)
	}
	leaseID := opts.LeaseID
	if leaseID == "" {
		leaseID = taskRunID + "-lease"
	}
	leaseToken := opts.LeaseToken
	if leaseToken == "" {
		leaseToken = generatedTaskRunLeaseToken()
	}
	queued, err := s.TaskRuns().Create(ctx, store.TaskRunCreate{
		WorkspaceKey:     opts.WorkspaceKey,
		TaskRunID:        taskRunID,
		DriverRunID:      opts.DriverRunID,
		DriverStepID:     opts.DriverStepID,
		TaskID:           opts.TaskID,
		WorkerProfileID:  opts.WorkerProfileID,
		ProviderProfile:  opts.ProviderProfile,
		Status:           domain.TaskRunQueued,
		RunnerPlacement:  opts.RunnerPlacement,
		SandboxPlacement: opts.SandboxPlacement,
		RuntimeMetadata: map[string]string{
			"driver_run_id": opts.DriverRunID,
			"requested_by":  "driver",
		},
	})
	if err != nil {
		return nil, fmt.Errorf("create task run: %w", err)
	}
	claimWorkerProfiles := append([]string(nil), opts.WorkerProfileIDs...)
	if opts.WorkerProfileID != "" {
		claimWorkerProfiles = append(claimWorkerProfiles, opts.WorkerProfileID)
	}
	claimed, err := s.TaskRuns().ClaimQueued(ctx, opts.WorkspaceKey, store.TaskRunClaim{
		TaskRunID:          queued.TaskRunID,
		NodeID:             nodeID,
		RunnerID:           opts.RunnerID,
		LeaseID:            leaseID,
		LeaseToken:         leaseToken,
		SupportedProviders: taskRunSupportedProviders(opts),
		Capabilities:       normalizeStringList(opts.Capabilities),
		WorkerProfileIDs:   normalizeStringList(claimWorkerProfiles),
		RunnerPlacement:    opts.RunnerPlacement,
		SandboxPlacement:   opts.SandboxPlacement,
	})
	if err != nil {
		return nil, fmt.Errorf("claim task run: %w", err)
	}
	if opts.DriverStepID != "" {
		status := domain.DriverStepRunning
		if _, err := s.DriverSteps().Update(ctx, opts.WorkspaceKey, opts.DriverStepID, store.DriverStepUpdate{
			Status:       &status,
			TaskRunID:    &claimed.TaskRunID,
			NodeID:       opts.ParentNodeID,
			LeaseID:      opts.ParentLeaseID,
			FencingToken: opts.ParentFence,
		}); err != nil {
			return nil, fmt.Errorf("link driver step: %w", err)
		}
	}
	return executeClaimedTaskRunWithResult(ctx, s, claimed, executeClaimedTaskRunOptions{
		WorkspaceKey:      opts.WorkspaceKey,
		DriverRunID:       opts.DriverRunID,
		DriverStepID:      opts.DriverStepID,
		TaskID:            opts.TaskID,
		ProviderProfile:   opts.ProviderProfile,
		LeaseToken:        leaseToken,
		HeartbeatInterval: opts.HeartbeatInterval,
		DeferCompletion:   opts.DeferCompletion,
		UpdateDriverStep:  opts.DriverStepID != "",
		ParentNodeID:      opts.ParentNodeID,
		ParentLeaseID:     opts.ParentLeaseID,
		ParentFence:       opts.ParentFence,
		HeartbeatSource:   "driver_task_request",
	}, executor)
}

type executeClaimedTaskRunOptions struct {
	WorkspaceKey      string
	DriverRunID       string
	DriverStepID      string
	TaskID            string
	ProviderProfile   string
	LeaseToken        string
	HeartbeatInterval time.Duration
	DeferCompletion   bool
	UpdateDriverStep  bool
	ParentNodeID      string
	ParentLeaseID     string
	ParentFence       int64
	HeartbeatSource   string
}

func executeClaimedTaskRunWithResult(ctx context.Context, s store.Store, claimed *domain.TaskRun, opts executeClaimedTaskRunOptions, executor TaskExecutor) (*TaskRunRequestOutcome, error) {
	if claimed == nil {
		return nil, fmt.Errorf("claimed task run required: %w", domain.ErrInvalid)
	}
	if executor == nil {
		executor = LocalTaskExecutor{}
	}
	workspaceKey := firstNonEmpty(opts.WorkspaceKey, claimed.WorkspaceKey)
	driverRunID := firstNonEmpty(opts.DriverRunID, claimed.DriverRunID)
	driverStepID := firstNonEmpty(opts.DriverStepID, claimed.DriverStepID)
	taskID := firstNonEmpty(opts.TaskID, claimed.TaskID)
	providerProfile := firstNonEmpty(opts.ProviderProfile, claimed.ProviderProfile)
	heartbeatSource := firstNonEmpty(opts.HeartbeatSource, "task_run_executor")
	hbCtx, stopHeartbeat := context.WithCancel(ctx)
	defer stopHeartbeat()
	if interval := taskRunHeartbeatInterval(opts.HeartbeatInterval); interval > 0 {
		go heartbeatTaskRun(hbCtx, s, claimed, opts.LeaseToken, interval, map[string]string{
			"driver_run_id":    driverRunID,
			"provider_profile": providerProfile,
			"heartbeat_source": heartbeatSource,
		})
	}
	execResult, execErr := executor.ExecuteTask(ctx, TaskExecRequest{
		WorkspaceKey:     workspaceKey,
		DriverRunID:      driverRunID,
		DriverStepID:     driverStepID,
		TaskRunID:        claimed.TaskRunID,
		TaskID:           taskID,
		WorkerProfileID:  claimed.WorkerProfileID,
		ProviderProfile:  providerProfile,
		NodeID:           claimed.NodeID,
		LeaseID:          claimed.LeaseID,
		LeaseToken:       opts.LeaseToken,
		FencingToken:     claimed.FencingToken,
		RunnerPlacement:  claimed.RunnerPlacement,
		SandboxPlacement: claimed.SandboxPlacement,
	})
	status := execResult.Status
	if status == "" {
		status = domain.TaskRunCompleted
		if execResult.ExitCode != 0 || execErr != nil {
			status = domain.TaskRunFailed
		}
	}
	if !status.IsTerminal() {
		status = domain.TaskRunFailed
	}
	exitCode := execResult.ExitCode
	if execErr != nil && exitCode == 0 {
		exitCode = 1
	}
	errorClass := execResult.ErrorClass
	errorMessage := execResult.ErrorMessage
	if execErr != nil {
		if errorClass == "" {
			errorClass = "task_executor_error"
		}
		if errorMessage == "" {
			errorMessage = execErr.Error()
		}
	}
	metadata := cloneStringMap(execResult.RuntimeMetadata)
	if metadata == nil {
		metadata = map[string]string{}
	}
	if driverRunID != "" {
		metadata["driver_run_id"] = driverRunID
	}
	if driverStepID != "" {
		metadata["driver_step_id"] = driverStepID
	}
	metadata["provider_profile"] = providerProfile
	metadata["task_run_executor"] = heartbeatSource
	if opts.DeferCompletion && status == domain.TaskRunCompleted {
		pending, err := s.TaskRuns().Heartbeat(ctx, workspaceKey, claimed.TaskRunID, store.TaskRunHeartbeat{
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
		synthetic := *pending
		synthetic.Status = status
		synthetic.ExitCode = &exitCode
		synthetic.LogsRef = execResult.LogsRef
		synthetic.ArtifactsRef = execResult.ArtifactsRef
		synthetic.InputTokens = execResult.InputTokens
		synthetic.OutputTokens = execResult.OutputTokens
		synthetic.CacheReadTokens = execResult.CacheReadTokens
		synthetic.CacheWriteTokens = execResult.CacheWriteTokens
		synthetic.EstimatedCostUSD = execResult.EstimatedCostUSD
		synthetic.RuntimeMetadata = metadata
		synthetic.ErrorClass = errorClass
		synthetic.ErrorMessage = errorMessage
		now := time.Now().UTC()
		synthetic.FinishedAt = &now
		return &TaskRunRequestOutcome{Run: &synthetic, LeaseToken: opts.LeaseToken, ArtifactIDs: normalizeArtifactIDs(execResult.ArtifactIDs)}, nil
	}
	final, err := s.TaskRuns().Finish(ctx, workspaceKey, claimed.TaskRunID, store.TaskRunFinish{
		NodeID:           claimed.NodeID,
		LeaseID:          claimed.LeaseID,
		LeaseToken:       opts.LeaseToken,
		FencingToken:     claimed.FencingToken,
		Status:           status,
		ExitCode:         &exitCode,
		LogsRef:          execResult.LogsRef,
		ArtifactsRef:     execResult.ArtifactsRef,
		InputTokens:      execResult.InputTokens,
		OutputTokens:     execResult.OutputTokens,
		CacheReadTokens:  execResult.CacheReadTokens,
		CacheWriteTokens: execResult.CacheWriteTokens,
		EstimatedCostUSD: execResult.EstimatedCostUSD,
		RuntimeMetadata:  metadata,
		ErrorClass:       errorClass,
		ErrorMessage:     errorMessage,
	})
	if err != nil {
		return nil, fmt.Errorf("finish task run: %w", err)
	}
	if opts.UpdateDriverStep && driverStepID != "" {
		stepStatus := driverStepStatusForTaskRun(status)
		outputRef := execResult.ArtifactsRef
		if outputRef == "" {
			outputRef = execResult.LogsRef
		}
		if _, err := s.DriverSteps().Update(ctx, workspaceKey, driverStepID, store.DriverStepUpdate{
			Status:       &stepStatus,
			TaskRunID:    &claimed.TaskRunID,
			OutputRef:    &outputRef,
			NodeID:       opts.ParentNodeID,
			LeaseID:      opts.ParentLeaseID,
			FencingToken: opts.ParentFence,
		}); err != nil {
			return nil, fmt.Errorf("finish driver step: %w", err)
		}
	}
	return &TaskRunRequestOutcome{Run: final, LeaseToken: opts.LeaseToken, ArtifactIDs: normalizeArtifactIDs(execResult.ArtifactIDs)}, nil
}

func driverStepStatusForTaskRun(status domain.TaskRunStatus) domain.DriverStepStatus {
	switch status {
	case domain.TaskRunCompleted:
		return domain.DriverStepCompleted
	case domain.TaskRunCancelled:
		return domain.DriverStepSkipped
	default:
		return domain.DriverStepFailed
	}
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

func taskRunHeartbeatInterval(interval time.Duration) time.Duration {
	if interval == 0 {
		return 30 * time.Second
	}
	if interval < 0 {
		return 0
	}
	return interval
}

func heartbeatTaskRun(ctx context.Context, s store.Store, run *domain.TaskRun, leaseToken string, interval time.Duration, metadata map[string]string) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _ = s.TaskRuns().Heartbeat(ctx, run.WorkspaceKey, run.TaskRunID, store.TaskRunHeartbeat{
				NodeID:          run.NodeID,
				LeaseID:         run.LeaseID,
				LeaseToken:      leaseToken,
				FencingToken:    run.FencingToken,
				RuntimeMetadata: cloneStringMap(metadata),
			})
		}
	}
}

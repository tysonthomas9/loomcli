package driver

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type LocalTaskExecutor struct{}

func (LocalTaskExecutor) PreflightTaskProvider(_ context.Context, opts TaskRunRequestOptions) (TaskRunRequestOptions, error) {
	if taskRunHasNamedRunner(opts) {
		return opts, fmt.Errorf("runner %q requires a configured task runner command: %w", opts.Runner, domain.ErrInvalid)
	}
	if taskProviderIsNoop(opts.ProviderProfile) && !testNoopProviderEnabled() {
		return opts, fmt.Errorf("noop provider profile %q requires %s=1 (provider_unsupported): %w", opts.ProviderProfile, NoopTaskProviderEnvVar, domain.ErrInvalid)
	}
	return resolveTaskProviderProfile(opts, false)
}

func (LocalTaskExecutor) ExecuteTask(_ context.Context, req TaskExecRequest) (TaskExecResult, error) {
	metadata := map[string]string{
		"executor":         "local-exec-task",
		"provider_profile": req.ProviderProfile,
	}
	switch strings.TrimSpace(req.ProviderProfile) {
	case "local-noop", "noop":
		if !testNoopProviderEnabled() {
			return TaskExecResult{
				Status:          domain.TaskRunFailed,
				ExitCode:        2,
				LogsRef:         "task-run://" + req.TaskRunID + "/logs",
				RuntimeMetadata: metadata,
				ErrorClass:      "provider_unsupported",
				ErrorMessage:    fmt.Sprintf("noop provider profile %q requires %s=1", req.ProviderProfile, NoopTaskProviderEnvVar),
			}, nil
		}
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
	Runner           string               `json:"runner,omitempty"`
	RunnerRef        string               `json:"runnerRef,omitempty"`
	RunnerKind       string               `json:"runnerKind,omitempty"`
	RunnerEntrypoint string               `json:"runnerEntrypoint,omitempty"`
	RunnerVersionID  string               `json:"runnerDriverVersionId,omitempty"`
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

// PrepareTaskRunRequest resolves the requested runner and performs provider
// preflight without writing TaskRun or DriverStep state. The driver-op HTTP
// adapter uses this read-only compatibility seam while Execution owns the
// idempotent request mutation. New callers must not follow this with one of
// the legacy Store-writing request helpers.
func PrepareTaskRunRequest(
	ctx context.Context,
	s store.Store,
	opts TaskRunRequestOptions,
	parent *domain.DriverRun,
	preflighter TaskProviderPreflighter,
) (TaskRunRequestOptions, error) {
	if s == nil || parent == nil {
		return opts, fmt.Errorf("store and parent driver run required: %w", domain.ErrInvalid)
	}
	opts = normalizeTaskRunRequestOptions(opts)
	if err := validateTaskRunRequestOptions(opts); err != nil {
		return opts, err
	}
	if parent.WorkspaceKey != opts.WorkspaceKey || parent.RunID != opts.DriverRunID || parent.Status != domain.DriverRunRunning {
		return opts, fmt.Errorf("parent driver run does not match active request owner: %w", domain.ErrNotOwner)
	}
	resolved, err := resolveTaskRunRequestRunner(ctx, s, opts, parent)
	if err != nil {
		return opts, err
	}
	if preflighter == nil {
		preflighter = LocalTaskExecutor{}
	}
	return preflightTaskRunRequest(ctx, resolved, preflighter)
}

func normalizeTaskRunRequestOptions(opts TaskRunRequestOptions) TaskRunRequestOptions {
	opts.WorkspaceKey = strings.TrimSpace(opts.WorkspaceKey)
	opts.DriverRunID = strings.TrimSpace(opts.DriverRunID)
	opts.DriverStepID = strings.TrimSpace(opts.DriverStepID)
	opts.TaskRunID = strings.TrimSpace(opts.TaskRunID)
	opts.TaskID = strings.TrimSpace(opts.TaskID)
	opts.WorkerProfileID = strings.TrimSpace(opts.WorkerProfileID)
	opts.Runner = strings.TrimSpace(opts.Runner)
	opts.RunnerRef = strings.TrimSpace(opts.RunnerRef)
	opts.RunnerKind = strings.TrimSpace(opts.RunnerKind)
	opts.RunnerEntrypoint = strings.TrimSpace(opts.RunnerEntrypoint)
	opts.RunnerVersionID = strings.TrimSpace(opts.RunnerVersionID)
	opts.RunnerTrustLevel = domain.DriverTrustLevel(strings.TrimSpace(string(opts.RunnerTrustLevel)))
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

func resolveTaskRunRequestRunner(ctx context.Context, s store.Store, opts TaskRunRequestOptions, parent *domain.DriverRun) (TaskRunRequestOptions, error) {
	if strings.TrimSpace(opts.Runner) == "" {
		return opts, nil
	}
	if parent == nil || strings.TrimSpace(parent.DriverVersionID) == "" {
		return opts, fmt.Errorf("parent driver run version required to resolve runner %q: %w", opts.Runner, domain.ErrInvalid)
	}
	version, err := s.DriverVersions().Get(ctx, opts.WorkspaceKey, parent.DriverVersionID)
	if err != nil {
		return opts, fmt.Errorf("load runner manifest from driver version %q: %w", parent.DriverVersionID, err)
	}
	if version.DriverID != parent.DriverID {
		return opts, fmt.Errorf("runner manifest version %q belongs to driver %q, run wants %q: %w", version.VersionID, version.DriverID, parent.DriverID, domain.ErrInvalid)
	}
	resolved, err := applyResolvedRunner(opts, parent, version)
	if err == nil {
		driver, derr := s.Drivers().Get(ctx, opts.WorkspaceKey, parent.DriverID)
		if derr != nil {
			return opts, fmt.Errorf("load driver %q for runner trust policy: %w", parent.DriverID, derr)
		}
		resolved.RunnerTrustLevel = DriverVersionEffectiveTrust(driver, version)
		return resolved, nil
	}
	// The caller's own version does not declare this runner. Fall back to the
	// workspace-global BUILTIN task-runner registry (GAP A). This is what lets a
	// custom/untrusted workflow driver dispatch a blessed builtin runner (e.g.
	// local-task-runner) it never bundled. Any OTHER resolve failure (malformed
	// manifest, OpenShell guard) fails closed with no fallback.
	if !errors.Is(err, ErrRunnerNotDeclared) {
		return opts, err
	}
	if globalResolved, gErr := resolveGlobalRunnerRequest(ctx, opts, parent); gErr == nil {
		return globalResolved, nil
	}
	// Global resolution also failed: return the ORIGINAL not-declared error so an
	// unknown runner name fails exactly as it did before this fallback existed.
	return opts, err
}

// resolveGlobalRunnerRequest resolves opts.Runner against the trusted builtin
// registry and pins the request onto the runner's OWNING (builtin) version: its
// version id, ref, kind and entrypoint — so the host loads the builtin's bundle
// — and its trust level — so the runner executes under its own trust, never the
// (possibly untrusted) caller's. See global_runner.go for the security
// reasoning.
func resolveGlobalRunnerRequest(ctx context.Context, opts TaskRunRequestOptions, parent *domain.DriverRun) (TaskRunRequestOptions, error) {
	res, err := resolveGlobalRunner(ctx, opts.WorkspaceKey, opts.Runner)
	if err != nil {
		return opts, err
	}
	resolved, err := applyResolvedRunner(opts, parent, res.Version)
	if err != nil {
		return opts, err
	}
	resolved.RunnerTrustLevel = DriverVersionEffectiveTrust(res.Driver, res.Version)
	return resolved, nil
}

type executeClaimedTaskRunOptions struct {
	WorkspaceKey       string
	DriverRunID        string
	DriverStepID       string
	TaskID             string
	ProviderProfile    string
	RunnerTrustLevel   domain.DriverTrustLevel
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

func taskExecRequest(claimed *domain.TaskRun, opts executeClaimedTaskRunOptions, refs claimedTaskRunRefs) TaskExecRequest {
	return TaskExecRequest{
		WorkspaceKey:     refs.WorkspaceKey,
		DriverRunID:      refs.DriverRunID,
		DriverStepID:     refs.DriverStepID,
		TaskRunID:        claimed.TaskRunID,
		TaskRunAttempt:   taskRunAttempt(claimed) + 1,
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

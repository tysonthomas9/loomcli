package driver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// NoopTaskProviderEnvVar gates the test-only noop/local-noop provider profile
// (§4.5). When it is not set to "1" a noop provider fails closed with
// provider_unsupported in both preflight and execute — production must never
// auto-complete a task run without real execution evidence.
const NoopTaskProviderEnvVar = "LOOM_DRIVER_ENABLE_TEST_NOOP_PROVIDER"

// testNoopProviderEnabled reports whether the test-only noop provider gate is
// explicitly enabled. Default (unset) is fail-closed.
func testNoopProviderEnabled() bool {
	return strings.TrimSpace(os.Getenv(NoopTaskProviderEnvVar)) == "1"
}

type TaskRunRequestOptions struct {
	WorkspaceKey       string
	DriverRunID        string
	DriverStepID       string
	TaskRunID          string
	TaskID             string
	WorkerProfileID    string
	Runner             string
	RunnerRef          string
	RunnerKind         string
	RunnerEntrypoint   string
	RunnerVersionID    string
	RunnerTrustLevel   domain.DriverTrustLevel
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
	// CloseTaskOnSuccess overrides, per request, whether the serve task worker
	// closes the underlying task issue when this run completes. Nil preserves
	// the worker's default (true) byte-for-byte; a non-nil value is persisted on
	// the queued run (RuntimeMetadata) and honored after claim so an
	// asynchronously-executed run (enqueueOnly) can suppress the close — e.g. a
	// planner run that must leave the card in design+review, not done.
	CloseTaskOnSuccess *bool
	// Input is the optional task-run payload (e.g. a review diff+rubric)
	// persisted on the run and delivered to the runner.
	Input json.RawMessage
}

// TaskRunCloseOnSuccessMetaKey persists a per-request CloseTaskOnSuccess
// override on the queued task run's RuntimeMetadata ("true"/"false"). The serve
// task worker claims any queued run without knowing its id in advance, so the
// override travels with the run rather than the worker options; it is resolved
// after claim (resolveCloseTaskOnSuccess). Absent key => the caller's default is
// used unchanged.
const TaskRunCloseOnSuccessMetaKey = "close_task_on_success"

// resolveCloseTaskOnSuccess returns the effective close-on-success decision for
// a claimed run: the value persisted on the run's RuntimeMetadata wins when
// present (set from a per-request CloseTaskOnSuccess override), otherwise the
// caller's default (fallback) is preserved unchanged.
func resolveCloseTaskOnSuccess(fallback bool, metadata map[string]string) bool {
	if metadata == nil {
		return fallback
	}
	raw, ok := metadata[TaskRunCloseOnSuccessMetaKey]
	if !ok {
		return fallback
	}
	parsed, err := strconv.ParseBool(strings.TrimSpace(raw))
	if err != nil {
		return fallback
	}
	return parsed
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
	Runner           string                  `json:"runner,omitempty"`
	RunnerRef        string                  `json:"runner_ref,omitempty"`
	RunnerKind       string                  `json:"runner_kind,omitempty"`
	RunnerEntrypoint string                  `json:"runner_entrypoint,omitempty"`
	RunnerVersionID  string                  `json:"runner_driver_version_id,omitempty"`
	RunnerTrustLevel domain.DriverTrustLevel `json:"runner_trust_level,omitempty"`
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
	if globalResolved, gErr := resolveGlobalRunnerRequest(ctx, s, opts, parent); gErr == nil {
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
func resolveGlobalRunnerRequest(ctx context.Context, s store.Store, opts TaskRunRequestOptions, parent *domain.DriverRun) (TaskRunRequestOptions, error) {
	res, err := resolveGlobalRunner(ctx, s, opts.WorkspaceKey, opts.Runner)
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
		// Persist the per-request override so the worker (which claims any queued
		// run) can honor it after claim. Absent => worker default preserved.
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
	// The per-request CloseTaskOnSuccess override (persisted on the queued run's
	// RuntimeMetadata) wins over the caller's default when present, so an
	// enqueued planner run leaves its card in design+review instead of closing.
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
	// Terminal failure past the retry budget: finish the run and mark the
	// underlying task issue blocked in the same fenced finish call.
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

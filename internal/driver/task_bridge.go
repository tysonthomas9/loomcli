package driver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	runtimesettings "github.com/tysonthomas9/loomcli/internal/localsettings"
	"github.com/tysonthomas9/loomcli/internal/sessions/transcript"
	"github.com/tysonthomas9/loomcli/internal/stackstore"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/taskroot"
)

const (
	TaskRunnerCommandJSONEnv = "LOOM_DRIVER_TASK_RUNNER_CMD_JSON"
	TaskRunnerCommandEnv     = "LOOM_DRIVER_TASK_RUNNER_CMD"

	// LocalTaskRunnerEntrypoint is the bundled local task runner entrypoint.
	// Only this runner gets the resolved backend env + the trusted-local
	// provider-credential env allowlist (§4.3); Daytona/remote runners keep the
	// strict driver filter.
	LocalTaskRunnerEntrypoint = "local-task-runner"

	// TaskRunnerBackendEnv carries the resolved backend CLI to the local task
	// runner (§4.5).
	TaskRunnerBackendEnv = "LOOM_TASK_RUNNER_BACKEND"

	// defaultTaskRunnerBackend mirrors service.GetWorkspaceBackend's default
	// (DaemonProfile.AgentBackend empty -> codex).
	defaultTaskRunnerBackend = "codex"
)

// isLocalTaskRunner reports whether the request targets the bundled local task
// runner. The env widening in §4.3 is gated strictly by the runner entrypoint
// so a leak cannot reach Daytona/remote runners.
func isLocalTaskRunner(req TaskExecRequest) bool {
	return strings.TrimSpace(req.RunnerEntrypoint) == LocalTaskRunnerEntrypoint
}

type HostBridgeTaskExecutor struct {
	Store        store.Store
	WorktreePath string
	Command      []string
	// APIBaseURL, when set, is exported to the spawned task runner as
	// LOOM_TASK_RUN_API_URL: the serve-hosted task-run API the runner SDK
	// targets with its per-task-run lease token instead of dialing fleet-db
	// with deployment credentials (the bridge env already blocks
	// LOOM_FLEET_DB_* inheritance; this gives runners the sanctioned path).
	APIBaseURL string
	// LocalSettingsDir points at the desktop-local settings directory. When set,
	// only local-task-runner receives non-secret local runner settings and the
	// sealed GitHub token as process env for opt-in PR delivery.
	LocalSettingsDir string
	// WorktreeResolver maps bundled local task runs onto isolated per-run git
	// worktrees. When nil, WorktreePath is used as supplied by the caller.
	WorktreeResolver TaskWorktreeResolver
	// RootResolver maps repository-set TaskRuns onto a composite root. It owns
	// the new multi-repository path and takes precedence over WorktreeResolver.
	RootResolver TaskRootResolver
	// ChangePublisher pushes backend-authored commits and records the immutable
	// FleetDB handoff. Nil selects the credential-bearing local Git proxy when
	// the concrete Store implements TaskChangeHandoffStore.
	CompletionFinalizer TaskCompletionFinalizer
	// StackStore is the finalize-barrier seam: after a stacked task completes
	// (branch pushed, result reported) the executor records the task's stack node
	// state/SHA here BEFORE returning — i.e. before the worker closes the task and
	// unblocks successors — so a dependent's resolver reads a durable node. When
	// nil (the pre-stacking sites and all tests), the barrier is inert.
	StackStore stackstore.Store
	// stackBinding is computed once per ExecuteTask after the worktree resolves:
	// the task's stack id, canonical output branch, and base ref. When set, it is
	// exported to a stacked runner so it pushes the canonical branch on the
	// predecessor base. nil = the task is not stacked (runner keeps its old path).
	stackBinding *TaskLineage
	// driverBundleBaseDir retains the workspace driver base (the WorktreePath as
	// supplied before WorktreeResolver swaps it for a per-run target-repo worktree).
	// Runner bundles are staged at registration under <base>/.loom/drivers/<version>,
	// which the per-run git worktree does NOT contain — so taskRunnerBundleEnv must
	// resolve the bundle against this base, not the reassigned worktree.
	driverBundleBaseDir string
	taskRootManifest    string
}

type bridgeTaskRunnerResult struct {
	Status                  domain.TaskRunStatus `json:"status"`
	ExitCode                *int                 `json:"exit_code"`
	ExitCodeCamel           *int                 `json:"exitCode"`
	LogsRef                 string               `json:"logs_ref"`
	LogsRefCamel            string               `json:"logsRef"`
	Logs                    string               `json:"logs"`
	LogsPath                string               `json:"logs_path"`
	LogsPathCamel           string               `json:"logsPath"`
	ArtifactsRef            string               `json:"artifacts_ref"`
	ArtifactsRefCamel       string               `json:"artifactsRef"`
	ArtifactIDs             []string             `json:"artifact_ids"`
	ArtifactIDsCamel        []string             `json:"artifactIds"`
	InputTokens             int64                `json:"input_tokens"`
	InputTokensCamel        int64                `json:"inputTokens"`
	OutputTokens            int64                `json:"output_tokens"`
	OutputTokensCamel       int64                `json:"outputTokens"`
	CacheReadTokens         int64                `json:"cache_read_tokens"`
	CacheReadTokensCamel    int64                `json:"cacheReadTokens"`
	CacheWriteTokens        int64                `json:"cache_write_tokens"`
	CacheWriteTokensCamel   int64                `json:"cacheWriteTokens"`
	EstimatedCostUSD        float64              `json:"estimated_cost_usd"`
	EstimatedCostUSDCamel   float64              `json:"estimatedCostUsd"`
	Artifacts               []bridgeArtifact     `json:"artifacts"`
	ArtifactDescriptors     []bridgeArtifact     `json:"artifact_descriptors"`
	ArtifactDescriptorsAlt  []bridgeArtifact     `json:"artifactDescriptors"`
	SessionID               string               `json:"session_id"`
	SessionIDCamel          string               `json:"sessionId"`
	TranscriptRef           string               `json:"transcript_ref"`
	TranscriptRefCamel      string               `json:"transcriptRef"`
	Transcript              string               `json:"transcript"`
	TranscriptPath          string               `json:"transcript_path"`
	TranscriptPathCamel     string               `json:"transcriptPath"`
	TranscriptEntries       []transcript.Event   `json:"transcript_entries"`
	TranscriptEntriesCamel  []transcript.Event   `json:"transcriptEntries"`
	TranscriptEvents        []transcript.Event   `json:"transcript_events"`
	TranscriptEventsCamel   []transcript.Event   `json:"transcriptEvents"`
	RuntimeMetadata         map[string]string    `json:"runtime_metadata"`
	RuntimeMetadataCamel    map[string]string    `json:"runtimeMetadata"`
	ErrorClass              string               `json:"error_class"`
	ErrorClassCamel         string               `json:"errorClass"`
	ErrorMessage            string               `json:"error_message"`
	ErrorMessageCamel       string               `json:"errorMessage"`
	Patch                   string               `json:"patch"`
	PatchPath               string               `json:"patch_path"`
	PatchPathCamel          string               `json:"patchPath"`
	PatchBaseRef            string               `json:"patch_base_ref"`
	PatchBaseRefCamel       string               `json:"patchBaseRef"`
	BaseRef                 string               `json:"base_ref"`
	BaseRefCamel            string               `json:"baseRef"`
	PatchArtifactID         string               `json:"patch_artifact_id"`
	PatchArtifactIDCamel    string               `json:"patchArtifactId"`
	PatchSummary            string               `json:"patch_summary"`
	PatchSummaryCamel       string               `json:"patchSummary"`
	PatchMIMEType           string               `json:"patch_mime_type"`
	PatchMIMETypeCamel      string               `json:"patchMimeType"`
	PatchVisibility         string               `json:"patch_visibility"`
	PatchVisibilityCamel    string               `json:"patchVisibility"`
	PatchRedactionStatus    string               `json:"patch_redaction_status"`
	PatchRedactionStatusAlt string               `json:"patchRedactionStatus"`
}

type bridgeArtifact struct {
	ArtifactID         string            `json:"artifact_id"`
	ArtifactIDCamel    string            `json:"artifactId"`
	ID                 string            `json:"id"`
	Type               string            `json:"type"`
	URI                string            `json:"uri"`
	Summary            string            `json:"summary"`
	MIMEType           string            `json:"mime_type"`
	MIMETypeCamel      string            `json:"mimeType"`
	SizeBytes          int64             `json:"size_bytes"`
	SizeBytesCamel     int64             `json:"sizeBytes"`
	Checksum           string            `json:"checksum"`
	ContentHash        string            `json:"content_hash"`
	ContentHashCamel   string            `json:"contentHash"`
	Visibility         string            `json:"visibility"`
	RedactionStatus    string            `json:"redaction_status"`
	RedactionStatusAlt string            `json:"redactionStatus"`
	Metadata           map[string]string `json:"metadata"`
}

type flueTaskSession struct {
	SessionID string
	Metadata  map[string]string
	cancel    context.CancelFunc
}

func (e HostBridgeTaskExecutor) PreflightTaskProvider(ctx context.Context, opts TaskRunRequestOptions) (TaskRunRequestOptions, error) {
	if taskRunHasNamedRunner(opts) {
		if err := refuseUntrustedTaskRunnerPreflight(opts); err != nil {
			return opts, err
		}
		command, err := e.command()
		if err != nil {
			return opts, err
		}
		if len(command) == 0 && strings.TrimSpace(opts.RunnerKind) == RunnerKindFlueWorkflow {
			return opts, nil
		}
		if len(command) == 0 {
			return opts, fmt.Errorf("runner %q requires a configured task runner command: %w", opts.Runner, domain.ErrInvalid)
		}
		return opts, nil
	}
	if taskProviderIsNoop(opts.ProviderProfile) {
		return LocalTaskExecutor{}.PreflightTaskProvider(ctx, opts)
	}
	command, err := e.command()
	if err != nil {
		return opts, err
	}
	if len(command) == 0 {
		return resolveTaskProviderProfile(opts, false)
	}
	return resolveTaskProviderProfile(opts, true)
}

//nolint:cyclop,funlen,gocognit // ExecuteTask owns the bridge lifecycle so deferred session finalization keeps one error/result scope.
func (e HostBridgeTaskExecutor) ExecuteTask(ctx context.Context, req TaskExecRequest) (result TaskExecResult, err error) {
	if taskProviderIsNoop(req.ProviderProfile) {
		return LocalTaskExecutor{}.ExecuteTask(ctx, req)
	}
	if result, refused := refuseUntrustedTaskRunnerExecution(req); refused {
		return result, nil
	}
	resolvedRoot, rootFailure, rooted := e.resolveLocalTaskRoot(ctx, req)
	if rootFailure.ErrorClass != "" {
		return rootFailure, nil
	}
	rootManifestJSON := ""
	if rooted {
		rootManifestJSON, err = durableTaskRootManifest(resolvedRoot.ManifestPath)
		if err != nil {
			return TaskExecResult{}, fmt.Errorf("capture task root manifest: %w", err)
		}
	}
	if rooted {
		defer func() {
			state := domain.TaskRunRootRetained
			if releaser, ok := e.RootResolver.(TaskRootReleaser); ok {
				if err == nil && result.Status == domain.TaskRunCompleted {
					if releaseErr := releaser.ReleaseTaskRoot(ctx, req, taskroot.RetentionPolicy{}); releaseErr != nil {
						result.Status = domain.TaskRunFailed
						result.ExitCode = 1
						result.ErrorClass = "task_root_release_failed"
						result.ErrorMessage = releaseErr.Error()
						state = domain.TaskRunRootRetained
					} else {
						state = domain.TaskRunRootReleased
					}
				} else if retainErr := releaser.ReleaseTaskRoot(ctx, req, taskroot.RetentionPolicy{RetainUntil: time.Now().UTC().Add(24 * time.Hour)}); retainErr != nil {
					state = domain.TaskRunRootFailed
				}
			}
			if lifecycleErr := e.recordTaskRunExecutionContext(ctx, req, state, "", ""); lifecycleErr != nil && err == nil {
				err = lifecycleErr
				result.Status = domain.TaskRunFailed
				result.ExitCode = 1
				result.ErrorClass = "task_root_state_failed"
				result.ErrorMessage = lifecycleErr.Error()
			}
		}()
	}
	resolvedWorktree, worktreeFailure, failed := e.resolveLocalTaskWorktree(ctx, req)
	if failed {
		return worktreeFailure, nil
	}
	// Stacked task? Compute the binding (canonical output branch + base ref) once.
	// Local runs resolve the repo from the host worktree; daytona/named runs (no
	// host worktree) resolve it from the task's repo selectors. When set, the
	// binding is exported as runner env (local) AND injected into the request
	// Input (so a daytona sandbox, which has no host stack store, still receives
	// the canonical branch + base ref). nil => not stacked => runner's old path.
	if e.StackStore != nil && !rooted {
		repoName := strings.TrimSpace(resolvedWorktree.RepoName)
		if repoName == "" {
			repoName = e.resolveStackRepoName(ctx, req)
		}
		if repoName != "" {
			if binding, ok, berr := stackBindingForTask(ctx, e.StackStore, req.WorkspaceKey, repoName, req.TaskID); berr != nil {
				slog.WarnContext(ctx, "stack binding lookup failed; runner keeps non-stacked behavior", "task", req.TaskID, "repo", repoName, "err", berr)
			} else if ok {
				e.stackBinding = &binding
				if injected, ierr := WithLineage(req.Input, binding); ierr == nil {
					req.Input = injected
				}
			}
		}
	}
	// Finalize barrier: when this is a stacked task, record its node state in the
	// stack store as ExecuteTask returns — the worker closes the task (unblocking
	// successors) only afterwards, so a dependent's resolver reads a durable node.
	if e.StackStore != nil {
		defer func() { e.finalizeStackNode(ctx, req, resolvedWorktree, result, err) }()
	}
	runBridge, err := e.bridgeRunner(ctx, req)
	if err != nil {
		return TaskExecResult{}, err
	}
	if runBridge == nil {
		return LocalTaskExecutor{}.ExecuteTask(ctx, req)
	}

	session, err := e.startFlueTaskSession(ctx, req)
	if err != nil {
		return TaskExecResult{}, err
	}
	var runner *bridgeTaskRunnerResult
	defer func() {
		if session != nil {
			if finishErr := e.finishFlueTaskSession(ctx, req, session, result, runner, err); finishErr != nil && err == nil {
				err = finishErr
			}
		}
	}()
	runnerResult, err := runBridge()
	if err != nil {
		return TaskExecResult{}, err
	}
	// Pre-persist validation gate (§4.2): the decoded runner result must be a
	// non-empty terminal result with a zero exit when completed. An invalid
	// result fails closed (invalid_task_result, exit 1) and NEVER reaches the
	// artifact/patch/log/transcript persistence below — we must not stamp real
	// evidence onto a run the runner did not actually finish.
	if reason, ok := validateBridgeTaskRunnerResult(runnerResult); !ok {
		runner = &runnerResult
		result = invalidBridgeTaskExecResult(runnerResult, reason)
		return result, nil
	}
	continuationCount := 0
	var commitInspection compositeCommitInspection
	for rooted && req.ExecutionClass != domain.TaskRunExecutionReview && runnerResult.Status == domain.TaskRunCompleted && len(resolvedRoot.Repositories) > 0 {
		commitInspection, err = inspectCompositeCommits(ctx, resolvedRoot)
		if err != nil {
			return TaskExecResult{}, err
		}
		if commitInspection.Outcome != changeHandoffContinuationRequired {
			break
		}
		sessionRef := firstNonEmpty(runnerResult.SessionID, runnerResult.SessionIDCamel, req.BackendSessionRef)
		if sessionRef == "" || continuationCount >= 2 {
			failure := runnerResult.taskExecResult()
			failure.Status = domain.TaskRunFailed
			failure.ExitCode = 1
			failure.ErrorClass = "backend_commit_required"
			failure.ErrorMessage = "backend completion left uncommitted changes in: " + strings.Join(commitInspection.DirtyRepositories, ", ")
			failure.RuntimeMetadata = mergeStringMaps(failure.RuntimeMetadata, map[string]string{
				"change_handoff_outcome":     string(commitInspection.Outcome),
				"backend_continuation_count": strconv.Itoa(continuationCount),
			})
			runner = &runnerResult
			return failure, nil
		}
		continuationCount++
		continuationReq := req
		continuationReq.BackendSessionRef = sessionRef
		continuationReq.ContinuationPrompt = compositeCommitContinuationPrompt(commitInspection.DirtyRepositories)
		continueBridge, bridgeErr := e.bridgeRunner(ctx, continuationReq)
		if bridgeErr != nil {
			return TaskExecResult{}, bridgeErr
		}
		continued, continueErr := continueBridge()
		if continueErr != nil {
			return TaskExecResult{}, continueErr
		}
		if reason, ok := validateBridgeTaskRunnerResult(continued); !ok {
			runner = &continued
			result = invalidBridgeTaskExecResult(continued, reason)
			return result, nil
		}
		runnerResult = mergeBridgeContinuation(runnerResult, continued)
	}
	runner = &runnerResult
	backendKind := firstNonEmpty(
		runnerResult.RuntimeMetadata["backend"],
		runnerResult.RuntimeMetadataCamel["backend"],
		e.resolveTaskRunnerBackend(req),
	)
	if lifecycleErr := e.recordTaskRunExecutionContext(ctx, req, domain.TaskRunRootReady, firstNonEmpty(runnerResult.SessionID, runnerResult.SessionIDCamel), backendKind); lifecycleErr != nil {
		return TaskExecResult{}, lifecycleErr
	}
	result = runnerResult.taskExecResult()
	if rooted && len(resolvedRoot.Repositories) > 0 {
		if req.ExecutionClass == domain.TaskRunExecutionReview && result.Status == domain.TaskRunCompleted {
			reviewMetadata, reviewErr := validateImmutableReviewRoot(ctx, resolvedRoot)
			if reviewErr != nil {
				result.Status = domain.TaskRunFailed
				result.ExitCode = 1
				result.ErrorClass = "review_head_mismatch"
				result.ErrorMessage = reviewErr.Error()
			} else if reviewErr = validateReviewVerdict(resolvedRoot, result.RuntimeMetadata); reviewErr != nil {
				result.Status = domain.TaskRunFailed
				result.ExitCode = 1
				result.ErrorClass = "review_verdict_invalid"
				result.ErrorMessage = reviewErr.Error()
			} else {
				result.RuntimeMetadata = mergeStringMaps(result.RuntimeMetadata, reviewMetadata)
			}
		} else {
			result.RuntimeMetadata = mergeStringMaps(result.RuntimeMetadata, compositeCommitMetadata(commitInspection, runnerResult, continuationCount))
		}
	}
	if resolvedWorktree.Path != "" {
		result.RuntimeMetadata = mergeStringMaps(result.RuntimeMetadata, map[string]string{
			"worktree_path":   resolvedWorktree.Path,
			"repo_name":       resolvedWorktree.RepoName,
			"source_repo_id":  resolvedWorktree.SourceRepoID,
			"worktree_source": "local_workspace_state",
		})
	}
	if rooted {
		result.RuntimeMetadata = mergeStringMaps(result.RuntimeMetadata, map[string]string{
			"task_root_path":          resolvedRoot.Path,
			"task_root_manifest":      resolvedRoot.ManifestPath,
			"task_root_manifest_json": rootManifestJSON,
			"repository_count":        fmt.Sprintf("%d", len(resolvedRoot.Repositories)),
		})
	}
	if artifacts := runner.finalizedArtifacts(); len(artifacts) > 0 {
		result, err = e.registerRunnerArtifacts(ctx, req, artifacts, result)
		if err != nil {
			return TaskExecResult{}, err
		}
	}
	result, err = e.persistRunnerOutputArtifacts(ctx, req, session, runnerResult, result)
	if err != nil {
		return TaskExecResult{}, err
	}
	if rooted && result.Status == domain.TaskRunCompleted && req.ExecutionClass != domain.TaskRunExecutionReview && len(commitInspection.Repositories) > 0 {
		finalizer := e.CompletionFinalizer
		if finalizer == nil {
			recorder, ok := store.ResolveTaskChangeHandoffStore(e.Store)
			if !ok {
				result.Status = domain.TaskRunFailed
				result.ExitCode = 1
				result.ErrorClass = "change_handoff_unavailable"
				result.ErrorMessage = "Task Change Set recorder is unavailable"
				return result, nil
			}
			finalizer = GitPushProxy{Recorder: recorder, LocalSettingsDir: e.LocalSettingsDir}
		}
		completionOutcome, publishErr := finalizer.Finalize(ctx, CompletionClaim{Request: req, Inspection: commitInspection, ArtifactRefs: result.ArtifactIDs})
		if publishErr != nil {
			result.Status = domain.TaskRunFailed
			result.ExitCode = 1
			result.ErrorClass = "git_push_failed"
			result.ErrorMessage = publishErr.Error()
			result.RuntimeMetadata = mergeStringMaps(result.RuntimeMetadata, map[string]string{"change_handoff_outcome": "failed"})
			return result, nil
		}
		if changeSet := completionOutcome.ChangeSet; changeSet != nil {
			result.RuntimeMetadata = mergeStringMaps(result.RuntimeMetadata, map[string]string{
				"change_set_version":     strconv.Itoa(changeSet.Version),
				"change_handoff_outcome": "ready_to_review",
			})
			reviewTaskRunID, enqueueErr := e.enqueueTaskChangeReview(ctx, req, changeSet.Version)
			if enqueueErr != nil {
				result.Status = domain.TaskRunFailed
				result.ExitCode = 1
				result.ErrorClass = "review_enqueue_failed"
				result.ErrorMessage = enqueueErr.Error()
				return result, nil
			}
			result.RuntimeMetadata["review_task_run_id"] = reviewTaskRunID
		}
	}
	patch, err := e.readPatch(ctx, runnerResult)
	if err != nil {
		return TaskExecResult{}, err
	}
	if len(patch) == 0 {
		return result, nil
	}
	return e.finalizeAndApplyPatch(ctx, req, runnerResult, patch, result)
}

func (e HostBridgeTaskExecutor) enqueueTaskChangeReview(ctx context.Context, req TaskExecRequest, version int) (string, error) {
	if e.Store == nil {
		return "", fmt.Errorf("store required to enqueue review: %w", domain.ErrInvalid)
	}
	taskRunID := req.TaskRunID + "-review-v" + strconv.Itoa(version)
	_, err := e.Store.TaskRuns().Create(ctx, store.TaskRunCreate{
		WorkspaceKey: req.WorkspaceKey, TaskRunID: taskRunID, TaskID: req.TaskID,
		ExecutionClass: domain.TaskRunExecutionReview, ChangeSetVersion: version, RootGeneration: 1,
		WorkerProfileID: req.WorkerProfileID, Runner: req.Runner, RunnerRef: req.RunnerRef,
		RunnerKind: req.RunnerKind, RunnerEntrypoint: req.RunnerEntrypoint, RunnerVersionID: req.RunnerVersionID,
		ProviderProfile: req.ProviderProfile, BackendKind: req.ProviderProfile, Status: domain.TaskRunQueued,
		RuntimeMetadata: map[string]string{
			"review_of_task_run_id": req.TaskRunID,
			"change_set_version":    strconv.Itoa(version),
			// Preserve the server-admitted trust of the exact runner version.
			// Dropping this stamp makes the review look untrusted at claim time
			// even though it reuses the implementation runner identity.
			"runner_trust_level": string(taskRunnerTrustLevel(req.RunnerTrustLevel)),
		},
		Input: req.Input,
	})
	if err != nil && !errors.Is(err, domain.ErrAlreadyExists) {
		return "", fmt.Errorf("enqueue review TaskRun %s: %w", taskRunID, err)
	}
	return taskRunID, nil
}

func (e HostBridgeTaskExecutor) recordTaskRunExecutionContext(ctx context.Context, req TaskExecRequest, state domain.TaskRunRootState, backendSessionRef, backendKind string) error {
	lifecycle, ok := store.ResolveTaskRunExecutionContextStore(e.Store)
	if !ok {
		return nil
	}
	if backendKind == "" {
		backendKind = e.resolveTaskRunnerBackend(req)
	}
	_, err := lifecycle.UpdateTaskRunExecutionContext(ctx, req.WorkspaceKey, req.TaskRunID, store.TaskRunExecutionContextUpdate{
		RootState: state, RootNodeID: req.NodeID, RootFencingToken: req.FencingToken,
		BackendKind: backendKind, BackendSessionRef: backendSessionRef,
	})
	if err != nil {
		return fmt.Errorf("persist TaskRun execution context: %w", err)
	}
	return nil
}

func (e HostBridgeTaskExecutor) bridgeRunner(ctx context.Context, req TaskExecRequest) (func() (bridgeTaskRunnerResult, error), error) {
	command, err := e.command()
	if err != nil {
		return nil, err
	}
	switch {
	case len(command) > 0:
		return func() (bridgeTaskRunnerResult, error) {
			return e.runCommand(ctx, req, command)
		}, nil
	case taskExecUsesFlueRuntime(req):
		return func() (bridgeTaskRunnerResult, error) {
			return e.runBuiltInFlueWorkflow(ctx, req)
		}, nil
	case taskExecHasNamedRunner(req):
		return nil, fmt.Errorf("runner %q requires a configured task runner command: %w", req.Runner, domain.ErrInvalid)
	default:
		return nil, nil
	}
}

func (e HostBridgeTaskExecutor) command() ([]string, error) {
	if len(e.Command) > 0 {
		return append([]string(nil), e.Command...), nil
	}
	if raw := strings.TrimSpace(os.Getenv(TaskRunnerCommandJSONEnv)); raw != "" {
		var command []string
		if err := json.Unmarshal([]byte(raw), &command); err != nil {
			return nil, fmt.Errorf("decode %s: %w", TaskRunnerCommandJSONEnv, err)
		}
		return normalizeCommand(command)
	}
	if raw := strings.TrimSpace(os.Getenv(TaskRunnerCommandEnv)); raw != "" {
		return []string{raw}, nil
	}
	return nil, nil
}

func normalizeCommand(command []string) ([]string, error) {
	out := make([]string, 0, len(command))
	for _, part := range command {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("task runner command is empty: %w", domain.ErrInvalid)
	}
	return out, nil
}

func (e HostBridgeTaskExecutor) runBuiltInFlueWorkflow(ctx context.Context, req TaskExecRequest) (bridgeTaskRunnerResult, error) {
	input, err := json.Marshal(req)
	if err != nil {
		return bridgeTaskRunnerResult{}, fmt.Errorf("encode task runner request: %w", err)
	}
	launcherPath, cleanup, err := writeFlueTaskRunnerLauncher()
	if err != nil {
		return bridgeTaskRunnerResult{}, err
	}
	defer cleanup()

	cmd := exec.CommandContext(ctx, "node", launcherPath) //nolint:gosec // fixed local runtime for bundled Flue workflow runners.
	if worktree := strings.TrimSpace(e.WorktreePath); worktree != "" {
		cmd.Dir = worktree
	}
	baseEnv := taskRunnerBaseEnvForRequest(req, os.Environ())
	env := append([]string{}, baseEnv...)
	env = append(env, e.taskRunnerEnv(req, string(input), baseEnv)...)
	cmd.Env = env
	cmd.Stdin = bytes.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return cmd.Process.Signal(os.Interrupt)
	}
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return bridgeTaskRunnerResult{}, fmt.Errorf("built-in Flue task runner failed: %s", msg)
	}
	payload, err := lastJSONLine(stdout.Bytes())
	if err != nil {
		return bridgeTaskRunnerResult{}, err
	}
	var result bridgeTaskRunnerResult
	if err := json.Unmarshal(payload, &result); err != nil {
		return bridgeTaskRunnerResult{}, fmt.Errorf("decode built-in Flue task runner result: %w", err)
	}
	return result, nil
}

func writeFlueTaskRunnerLauncher() (string, func(), error) {
	launcher, err := os.CreateTemp("", "loom-flue-task-runner-*.mjs")
	if err != nil {
		return "", nil, fmt.Errorf("create Flue task runner launcher: %w", err)
	}
	cleanup := func() { _ = os.Remove(launcher.Name()) }
	if _, err := launcher.WriteString(flueTaskRunnerLauncher); err != nil {
		_ = launcher.Close()
		cleanup()
		return "", nil, fmt.Errorf("write Flue task runner launcher: %w", err)
	}
	if err := launcher.Close(); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("close Flue task runner launcher: %w", err)
	}
	return launcher.Name(), cleanup, nil
}

func (e HostBridgeTaskExecutor) runCommand(ctx context.Context, req TaskExecRequest, command []string) (bridgeTaskRunnerResult, error) {
	input, err := json.Marshal(req)
	if err != nil {
		return bridgeTaskRunnerResult{}, fmt.Errorf("encode task runner request: %w", err)
	}
	cmd := exec.CommandContext(ctx, command[0], command[1:]...) //nolint:gosec // configured argv vector; no shell expansion.
	if worktree := strings.TrimSpace(e.WorktreePath); worktree != "" {
		cmd.Dir = worktree
	}
	baseEnv := taskRunnerBaseEnvForRequest(req, os.Environ())
	env := append([]string{}, baseEnv...)
	env = append(env, e.taskRunnerEnv(req, string(input), baseEnv)...)
	cmd.Env = env
	cmd.Stdin = bytes.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return bridgeTaskRunnerResult{}, fmt.Errorf("task runner command failed: %s", msg)
	}
	payload, err := lastJSONLine(stdout.Bytes())
	if err != nil {
		return bridgeTaskRunnerResult{}, err
	}
	var result bridgeTaskRunnerResult
	if err := json.Unmarshal(payload, &result); err != nil {
		return bridgeTaskRunnerResult{}, fmt.Errorf("decode task runner result: %w", err)
	}
	return result, nil
}

const flueTaskRunnerLauncher = `
import { fork } from 'node:child_process';

function firstNonEmpty(...values) {
  for (const value of values) {
    const text = String(value || '').trim();
    if (text) return text;
  }
  return '';
}

function stringMetadata(values = {}) {
  const out = {};
  for (const [key, value] of Object.entries(values || {})) {
    if (value === undefined || value === null) continue;
    out[key] = typeof value === 'string' ? value : String(value);
  }
  return out;
}

function failure(errorClass, error) {
  return {
    status: 'failed',
    exit_code: 1,
    error_class: errorClass,
    error_message: error && error.message ? error.message : String(error || 'task runner failed'),
    runtime_metadata: {
      task_runner_invoker: 'loom-builtin-flue-runner',
    },
  };
}

const TERMINAL_STATUSES = new Set(['completed', 'failed', 'cancelled']);

function ownKeyCount(value) {
  if (!value || typeof value !== 'object') return 0;
  return Object.keys(value).length;
}

// validateBridgeResult applies the strict §4.1 algorithm: the result must be a
// non-null object with >=1 own key and a terminal status; completed requires a
// zero (or absent) exit. Anything else is INVALID and NEVER defaults to
// completed. Returns { ok, reason }.
function validateBridgeResult(result) {
  if (!result || typeof result !== 'object' || ownKeyCount(result) < 1) {
    return { ok: false, reason: 'task runner returned an empty result' };
  }
  const status = typeof result.status === 'string' ? result.status.trim() : '';
  if (!status || !TERMINAL_STATUSES.has(status)) {
    return { ok: false, reason: 'task runner result status ' + JSON.stringify(result.status) + ' is not terminal' };
  }
  const rawExit = result.exit_code !== undefined ? result.exit_code : result.exitCode;
  let exit;
  if (rawExit === undefined || rawExit === null) {
    exit = undefined;
  } else {
    const n = Number(rawExit);
    if (!Number.isFinite(n)) {
      return { ok: false, reason: 'task runner reported a non-numeric exit code ' + JSON.stringify(rawExit) };
    }
    exit = n;
  }
  if (status === 'completed' && exit !== undefined && exit !== 0) {
    return { ok: false, reason: 'task runner reported completed with non-zero exit code ' + exit };
  }
  return { ok: true, reason: '' };
}

function normalizeBridgeResult(result, request, entrypoint) {
  const verdict = validateBridgeResult(result);
  if (!verdict.ok) {
    const invalid = failure('invalid_task_result', new Error(verdict.reason));
    invalid.runtime_metadata = stringMetadata({
      ...invalid.runtime_metadata,
      runner: firstNonEmpty(request.runner, process.env.LOOM_TASK_RUNNER),
      runner_kind: 'flue-workflow',
      runner_entrypoint: entrypoint,
    });
    return invalid;
  }
  const out = { ...result };
  out.status = (typeof out.status === 'string' ? out.status.trim() : out.status);
  const rawExit = out.exit_code !== undefined ? out.exit_code : out.exitCode;
  let exit;
  if (rawExit === undefined || rawExit === null) {
    exit = undefined;
  } else {
    const n = Number(rawExit);
    exit = Number.isFinite(n) ? n : undefined;
  }
  out.exit_code = (exit === undefined) ? (out.status === 'completed' ? 0 : 1) : exit;
  const runtimeMetadata = out.runtime_metadata || out.runtimeMetadata || {};
  out.runtime_metadata = stringMetadata({
    ...runtimeMetadata,
    task_runner_invoker: 'loom-builtin-flue-runner',
    runner: firstNonEmpty(request.runner, process.env.LOOM_TASK_RUNNER),
    runner_kind: 'flue-workflow',
    runner_entrypoint: entrypoint,
  });
  delete out.runtimeMetadata;
  return out;
}

function emit(value) {
  console.log(JSON.stringify(value || {}));
}

const rawRequest = process.env.LOOM_TASK_RUN_REQUEST_JSON || '{}';
const request = JSON.parse(rawRequest);
const serverPath = firstNonEmpty(process.env.LOOM_TASK_RUNNER_SERVER_PATH, process.env.LOOM_FLUE_SERVER_PATH);
const bundleRoot = firstNonEmpty(process.env.LOOM_TASK_RUNNER_BUNDLE_ROOT, process.env.LOOM_FLUE_BUNDLE_ROOT, process.cwd());
const entrypoint = firstNonEmpty(process.env.LOOM_TASK_RUNNER_ENTRYPOINT, request.runner_entrypoint);

if (process.env.LOOM_TASK_RUN_LEASE_TOKEN !== request.lease_token) {
  emit(failure('task_runner_invoker_failed', new Error('task-run lease token did not reach task runner')));
  process.exit(0);
}
if (!serverPath) {
  emit(failure('task_runner_invoker_failed', new Error('flue-workflow runner requires LOOM_TASK_RUNNER_SERVER_PATH')));
  process.exit(0);
}
if (!entrypoint) {
  emit(failure('task_runner_invoker_failed', new Error('flue-workflow runner entrypoint is required')));
  process.exit(0);
}

let settled = false;
let invoked = false;
const child = fork(serverPath, [], {
  cwd: bundleRoot,
  env: {
    ...process.env,
    FLUE_MODE: 'local',
    FLUE_CLI_TARGET: 'workflow',
    FLUE_CLI_NAME: entrypoint,
    // Flue HEAD gates one-shot IPC mode behind this explicit internal flag (in
    // addition to FLUE_CLI_TARGET + an inherited IPC channel) so user-supplied
    // FLUE_CLI_* can never flip a production HTTP server into IPC mode. Without
    // it the generated entry falls through to serving HTTP on :3000 (EADDRINUSE
    // across concurrent leaves) and never performs the invoke/result handshake.
    FLUE_INTERNAL_CLI_IPC: '1',
  },
  stdio: ['ignore', 'pipe', 'pipe', 'ipc'],
});

child.stdout?.on('data', (data) => process.stderr.write(data));
child.stderr?.on('data', (data) => process.stderr.write(data));

function stopChild() {
  try { child.disconnect(); } catch {}
  if (!child.killed) {
    try { child.kill(); } catch {}
  }
}

function finish(value) {
  if (settled) return;
  settled = true;
  stopChild();
  emit(normalizeBridgeResult(value || {}, request, entrypoint));
}

function fail(error) {
  if (settled) return;
  settled = true;
  stopChild();
  emit(failure('task_runner_invoker_failed', error));
}

child.on('message', (message) => {
  if (!message || typeof message !== 'object') return;
  if (message.type === 'ready' && !invoked) {
    invoked = true;
    child.send({
      version: 1,
      type: 'invoke',
      requestId: request.task_run_id || process.env.LOOM_TASK_RUN_ID || 'task-runner',
      payload: request,
    });
    return;
  }
  if (message.type === 'result') {
    finish(message.result || {});
    return;
  }
  if (message.type === 'error') {
    const error = message.error || {};
    fail(new Error(error.message || error.details || 'Flue workflow runner failed'));
  }
});

child.on('error', fail);
child.on('exit', (code, signal) => {
  if (settled) return;
  fail(new Error('Flue workflow runner exited before result (code=' + (code ?? '') + ' signal=' + (signal || '') + ')'));
});

process.once('SIGINT', () => {
  finish({ status: 'cancelled', exit_code: 130, errorClass: 'driver_cancelled', errorMessage: 'Flue task runner cancelled' });
});
process.once('SIGTERM', () => {
  finish({ status: 'cancelled', exit_code: 143, errorClass: 'driver_cancelled', errorMessage: 'Flue task runner cancelled' });
});
`

func (e HostBridgeTaskExecutor) taskRunnerEnv(req TaskExecRequest, requestJSON string, inherited ...[]string) []string {
	env := []string{
		"LOOM_TASK_RUN_REQUEST_JSON=" + requestJSON,
		"LOOM_WORKTREE_PATH=" + strings.TrimSpace(e.WorktreePath),
		"LOOM_DRIVER_WORKSPACE=" + req.WorkspaceKey,
		"LOOM_DRIVER_RUN_ID=" + req.DriverRunID,
		"LOOM_DRIVER_STEP_ID=" + req.DriverStepID,
		"LOOM_PARENT_SESSION_ID=" + req.ParentSessionID,
		"LOOM_TASK_RUN_ID=" + req.TaskRunID,
		"LOOM_TASK_ID=" + req.TaskID,
		"LOOM_TASK_RUN_PARENT_SESSION_ID=" + req.ParentSessionID,
		"LOOM_TASK_RUN_WORKER_PROFILE_ID=" + req.WorkerProfileID,
		"LOOM_TASK_RUNNER=" + req.Runner,
		"LOOM_TASK_RUNNER_REF=" + req.RunnerRef,
		"LOOM_TASK_RUNNER_KIND=" + req.RunnerKind,
		"LOOM_TASK_RUNNER_ENTRYPOINT=" + req.RunnerEntrypoint,
		"LOOM_TASK_RUNNER_DRIVER_VERSION_ID=" + req.RunnerVersionID,
		"LOOM_TASK_RUNNER_TRUST_LEVEL=" + string(taskRunnerTrustLevel(req.RunnerTrustLevel)),
		"LOOM_TASK_RUN_PROVIDER_PROFILE=" + req.ProviderProfile,
		"LOOM_TASK_RUN_NODE_ID=" + req.NodeID,
		"LOOM_TASK_RUN_LEASE_ID=" + req.LeaseID,
		"LOOM_TASK_RUN_LEASE_TOKEN=" + req.LeaseToken,
		fmt.Sprintf("LOOM_TASK_RUN_FENCING_TOKEN=%d", req.FencingToken),
		"LOOM_TASK_RUN_RUNNER_PLACEMENT_JSON=" + taskRunPlacementJSON(req.RunnerPlacement),
		"LOOM_TASK_RUN_SANDBOX_PLACEMENT_JSON=" + taskRunPlacementJSON(req.SandboxPlacement),
	}
	if apiBaseURL := strings.TrimSpace(e.APIBaseURL); apiBaseURL != "" {
		env = append(env, "LOOM_TASK_RUN_API_URL="+apiBaseURL)
	}
	if manifest := strings.TrimSpace(e.taskRootManifest); manifest != "" {
		env = append(env, "LOOM_TASK_ROOT_MANIFEST="+manifest)
	}
	// Stacked task: tell the runner to push the canonical branch on the
	// predecessor base instead of opening an independent loom/<taskid> PR.
	if e.stackBinding != nil {
		env = append(env,
			"LOOM_TASK_RUN_STACKED=1",
			"LOOM_TASK_RUN_STACK_ID="+e.stackBinding.StackID,
			"LOOM_TASK_RUN_OUTPUT_BRANCH="+e.stackBinding.OutputBranch,
			"LOOM_TASK_RUN_BASE_REF="+e.stackBinding.BaseRef,
		)
	}
	env = append(env, e.taskRunnerBundleEnv(req)...)
	if isLocalTaskRunner(req) {
		env = append(env, TaskRunnerBackendEnv+"="+e.resolveTaskRunnerBackend(req))
		existing := env
		if len(inherited) > 0 && len(inherited[0]) > 0 {
			existing = append(append([]string{}, inherited[0]...), env...)
		}
		env = append(env, e.localTaskRunnerSettingsEnv(existing)...)
	}
	return env
}

func (e HostBridgeTaskExecutor) localTaskRunnerSettingsEnv(existing []string) []string {
	dir := strings.TrimSpace(e.LocalSettingsDir)
	if dir == "" {
		return nil
	}
	settings, err := runtimesettings.Load(dir)
	if err != nil {
		return nil
	}
	out := make([]string, 0, 1)
	if model := strings.TrimSpace(settings.LocalTaskRunner.OpenCodeModel); model != "" {
		out = append(out, "LOOM_OPENCODE_MODEL="+model)
	}
	return out
}

func envHasAny(env []string, names ...string) bool {
	for _, entry := range env {
		name, value, ok := strings.Cut(entry, "=")
		if !ok || strings.TrimSpace(value) == "" {
			continue
		}
		for _, want := range names {
			if name == want {
				return true
			}
		}
	}
	return false
}

// resolveTaskRunnerBackend resolves the backend CLI for the local task runner,
// mirroring service.GetWorkspaceBackend precedence (§4.3): a per-agent override
// (the worker's agent row Backend) wins, else DaemonProfile.AgentBackend, else
// the default codex. The store is consulted best-effort; any lookup failure
// falls through to the next source so the runner always receives a backend.
func (e HostBridgeTaskExecutor) resolveTaskRunnerBackend(req TaskExecRequest) string {
	if e.Store == nil {
		return defaultTaskRunnerBackend
	}
	ctx := context.Background()
	if worker := strings.TrimSpace(req.WorkerProfileID); worker != "" {
		if agent, err := e.Store.Agents().Get(ctx, req.WorkspaceKey, worker); err == nil && agent != nil {
			if backend := strings.TrimSpace(agent.Backend); backend != "" {
				return backend
			}
		}
	}
	if profile, err := e.Store.Daemon().Get(ctx, req.WorkspaceKey); err == nil && profile != nil {
		if backend := strings.TrimSpace(profile.AgentBackend); backend != "" {
			return backend
		}
	}
	return defaultTaskRunnerBackend
}

func (e HostBridgeTaskExecutor) taskRunnerBundleEnv(req TaskExecRequest) []string {
	if e.Store == nil || strings.TrimSpace(req.RunnerVersionID) == "" {
		return nil
	}
	ctx := context.Background()
	version, err := e.Store.DriverVersions().Get(ctx, req.WorkspaceKey, req.RunnerVersionID)
	if err != nil || version.BundleRef == "" {
		return nil
	}
	// The bundle is staged at registration under <driver-base>/.loom/drivers/<version>. Try the
	// current WorktreePath first (covers callers that stage the bundle into the worktree, incl. the
	// productionize_runner tests), then fall back to the retained driver base — necessary once
	// WorktreeResolver has swapped WorktreePath for a per-run target-repo worktree (which lacks the
	// bundle). Without this fall-back every bundled local-task-runner fails with
	// task_runner_invoker_failed: "flue-workflow runner requires LOOM_TASK_RUNNER_SERVER_PATH".
	for _, base := range []string{strings.TrimSpace(e.WorktreePath), strings.TrimSpace(e.driverBundleBaseDir)} {
		if base == "" {
			continue
		}
		bundleRoot, err := safeBundleRoot(base, version.BundleRef)
		if err != nil {
			continue
		}
		manifest, serverPath, err := verifyBundleManifest(bundleRoot, version.BundleDigest)
		if err != nil {
			continue
		}
		encoded, err := json.Marshal(manifest)
		if err != nil {
			continue
		}
		return []string{
			"LOOM_TASK_RUNNER_BUNDLE_ROOT=" + bundleRoot,
			"LOOM_TASK_RUNNER_SERVER_PATH=" + serverPath,
			"LOOM_TASK_RUNNER_MANIFEST_JSON=" + string(encoded),
		}
	}
	return nil
}

func taskRunPlacementJSON(placement domain.TaskRunPlacement) string {
	if placement.Empty() {
		return "{}"
	}
	b, err := json.Marshal(placement)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func (e HostBridgeTaskExecutor) finalizeAndApplyPatch(ctx context.Context, req TaskExecRequest, runner bridgeTaskRunnerResult, patch []byte, result TaskExecResult) (TaskExecResult, error) {
	if e.Store == nil {
		return TaskExecResult{}, fmt.Errorf("store required for patch artifact finalization: %w", domain.ErrInvalid)
	}
	finalized, baseRef, err := e.createPatchArtifact(ctx, req, runner, patch)
	if err != nil {
		return TaskExecResult{}, err
	}
	result.ArtifactIDs = normalizeArtifactIDs(append(result.ArtifactIDs, finalized.ArtifactID))
	if result.ArtifactsRef == "" {
		result.ArtifactsRef = "artifacts://" + req.TaskRunID
	}
	if result.RuntimeMetadata == nil {
		result.RuntimeMetadata = map[string]string{}
	}
	result.RuntimeMetadata["patch_artifact_id"] = finalized.ArtifactID
	result.RuntimeMetadata["patch_content_hash"] = finalized.ContentHash
	if strings.TrimSpace(e.WorktreePath) == "" || strings.TrimSpace(baseRef) == "" {
		result.Status = domain.TaskRunFailed
		if result.ExitCode == 0 {
			result.ExitCode = 1
		}
		result.ErrorClass = "patch_back_base_required"
		result.ErrorMessage = "patch artifact requires worktree path and base ref for local patch-back"
		result.RuntimeMetadata["patch_back_status"] = PatchBackBaseUnreachable
		return result, nil
	}
	return e.applyPatchBack(ctx, baseRef, patch, result)
}

func (e HostBridgeTaskExecutor) createPatchArtifact(ctx context.Context, req TaskExecRequest, runner bridgeTaskRunnerResult, patch []byte) (*domain.Artifact, string, error) {
	artifactID := firstNonEmpty(runner.PatchArtifactID, runner.PatchArtifactIDCamel)
	if artifactID == "" {
		artifactID = "patch-" + req.TaskRunID
	}
	summary := firstNonEmpty(runner.PatchSummary, runner.PatchSummaryCamel, "task patch")
	mimeType := firstNonEmpty(runner.PatchMIMEType, runner.PatchMIMETypeCamel, "text/x-diff")
	baseRef := firstNonEmpty(runner.PatchBaseRef, runner.PatchBaseRefCamel, runner.BaseRef, runner.BaseRefCamel)
	metadata := map[string]string{
		"driver_run_id":            req.DriverRunID,
		"runner":                   req.Runner,
		"runner_ref":               req.RunnerRef,
		"runner_kind":              req.RunnerKind,
		"runner_entrypoint":        req.RunnerEntrypoint,
		"runner_driver_version_id": req.RunnerVersionID,
		"provider_profile":         req.ProviderProfile,
	}
	if baseRef != "" {
		metadata["patch_base_ref"] = baseRef
	}
	if _, err := e.Store.Artifacts().Create(ctx, store.ArtifactCreate{
		WorkspaceKey:    req.WorkspaceKey,
		ArtifactID:      artifactID,
		TaskID:          req.TaskID,
		OwnerType:       "task_run",
		OwnerID:         req.TaskRunID,
		Type:            "patch",
		Summary:         summary,
		MIMEType:        mimeType,
		Visibility:      firstNonEmpty(runner.PatchVisibility, runner.PatchVisibilityCamel),
		RedactionStatus: firstNonEmpty(runner.PatchRedactionStatus, runner.PatchRedactionStatusAlt),
		DurableStatus:   "declared",
		Metadata:        metadata,
	}); err != nil {
		return nil, "", fmt.Errorf("create patch artifact: %w", err)
	}
	uploaded, err := e.Store.Artifacts().UploadContent(ctx, req.WorkspaceKey, artifactID, store.ArtifactContentUpload{
		Body:     bytes.NewReader(patch),
		MIMEType: mimeType,
	})
	if err != nil {
		return nil, "", fmt.Errorf("upload patch artifact: %w", err)
	}
	finalized, err := e.Store.Artifacts().Finalize(ctx, req.WorkspaceKey, artifactID, store.ArtifactFinalize{
		ContentHash: &uploaded.ContentHash,
	})
	if err != nil {
		return nil, "", fmt.Errorf("finalize patch artifact: %w", err)
	}
	return finalized, baseRef, nil
}

func (e HostBridgeTaskExecutor) applyPatchBack(ctx context.Context, baseRef string, patch []byte, result TaskExecResult) (TaskExecResult, error) {
	patchBack, err := ApplyPatchBack(ctx, PatchBackOptions{
		WorktreePath: e.WorktreePath,
		BaseRef:      baseRef,
		Patch:        patch,
	})
	if err != nil {
		return TaskExecResult{}, err
	}
	result.RuntimeMetadata["patch_back_status"] = patchBack.Status
	if patchBack.BaseSHA != "" {
		result.RuntimeMetadata["patch_back_base_sha"] = patchBack.BaseSHA
	}
	if patchBack.CurrentHEAD != "" {
		result.RuntimeMetadata["patch_back_head_sha"] = patchBack.CurrentHEAD
	}
	if patchBack.Applied {
		return result, nil
	}
	result.Status = domain.TaskRunFailed
	if result.ExitCode == 0 {
		result.ExitCode = 1
	}
	result.ErrorClass = firstNonEmpty(patchBack.ErrorClass, patchBack.Status)
	result.ErrorMessage = patchBack.ErrorMessage
	result.RuntimeMetadata["patch_preserved"] = "true"
	return result, nil
}

func firstNonNilStrings(values ...[]string) []string {
	for _, value := range values {
		if len(value) > 0 {
			return value
		}
	}
	return nil
}

// durableTaskRootManifest captures the exact repository/root binding before
// the runner starts. The Root Manager removes the physical manifest when a
// successful run is released, so the compact JSON copy in TaskRun runtime
// metadata is the durable audit record used by handoff and review evidence.
func durableTaskRootManifest(path string) (string, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var manifest taskroot.RootManifest
	if err := json.Unmarshal(payload, &manifest); err != nil {
		return "", fmt.Errorf("decode %s: %w", path, err)
	}
	canonical, err := json.Marshal(manifest)
	if err != nil {
		return "", fmt.Errorf("encode %s: %w", path, err)
	}
	return string(canonical), nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func firstNonNilMap(values ...map[string]string) map[string]string {
	for _, value := range values {
		if len(value) > 0 {
			return value
		}
	}
	return nil
}

func mergeStringMaps(values ...map[string]string) map[string]string {
	out := map[string]string{}
	for _, value := range values {
		for key, val := range value {
			if strings.TrimSpace(key) != "" {
				out[key] = val
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func firstNonZeroInt64(values ...int64) int64 {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

func firstNonZeroFloat64(values ...float64) float64 {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

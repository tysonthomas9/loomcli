package driver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/sessions/transcript"
	"github.com/tysonthomas9/loomcli/internal/store"
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

func (e HostBridgeTaskExecutor) ExecuteTask(ctx context.Context, req TaskExecRequest) (result TaskExecResult, err error) {
	if taskProviderIsNoop(req.ProviderProfile) {
		return LocalTaskExecutor{}.ExecuteTask(ctx, req)
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
	runner = &runnerResult
	// Pre-persist validation gate (§4.2): the decoded runner result must be a
	// non-empty terminal result with a zero exit when completed. An invalid
	// result fails closed (invalid_task_result, exit 1) and NEVER reaches the
	// artifact/patch/log/transcript persistence below — we must not stamp real
	// evidence onto a run the runner did not actually finish.
	if reason, ok := validateBridgeTaskRunnerResult(runnerResult); !ok {
		result = invalidBridgeTaskExecResult(runnerResult, reason)
		return result, nil
	}
	result = runnerResult.taskExecResult()
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
	patch, err := e.readPatch(ctx, runnerResult)
	if err != nil {
		return TaskExecResult{}, err
	}
	if len(patch) == 0 {
		return result, nil
	}
	return e.finalizeAndApplyPatch(ctx, req, runnerResult, patch, result)
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

// validateBridgeTaskRunnerResult mirrors §4.1/§4.2: a decoded runner result is
// valid only when it carries a terminal status and — for completed — a zero
// exit code. Empty/`{}`/`null` results decode to a zero struct whose status is
// "" (non-terminal) and so are rejected. Returns (reason, false) when invalid.
func validateBridgeTaskRunnerResult(r bridgeTaskRunnerResult) (string, bool) {
	status := strings.TrimSpace(string(r.Status))
	if status == "" {
		return "task runner result missing terminal status", false
	}
	if !r.Status.IsTerminal() {
		return fmt.Sprintf("task runner result status %q is not terminal", status), false
	}
	if r.Status == domain.TaskRunCompleted {
		exit := bridgeResultExitCode(r)
		if exit != 0 {
			return fmt.Sprintf("task runner reported completed with non-zero exit code %d", exit), false
		}
	}
	return "", true
}

// bridgeResultExitCode resolves the runner exit code from either casing,
// defaulting to 0 when unset.
func bridgeResultExitCode(r bridgeTaskRunnerResult) int {
	if r.ExitCode != nil {
		return *r.ExitCode
	}
	if r.ExitCodeCamel != nil {
		return *r.ExitCodeCamel
	}
	return 0
}

// invalidBridgeTaskExecResult builds the fail-closed result for an invalid
// runner result: failed/exit 1/invalid_task_result, carrying the runner's own
// runtime metadata (so the failure is traceable) but no artifact/log refs.
func invalidBridgeTaskExecResult(r bridgeTaskRunnerResult, reason string) TaskExecResult {
	metadata := cloneStringMap(firstNonNilMap(r.RuntimeMetadata, r.RuntimeMetadataCamel))
	if metadata == nil {
		metadata = map[string]string{}
	}
	metadata["invalid_task_result_reason"] = reason
	errorMessage := firstNonEmpty(r.ErrorMessage, r.ErrorMessageCamel, reason)
	return TaskExecResult{
		Status:          domain.TaskRunFailed,
		ExitCode:        1,
		RuntimeMetadata: metadata,
		ErrorClass:      "invalid_task_result",
		ErrorMessage:    errorMessage,
	}
}

func taskProviderIsNoop(provider string) bool {
	switch strings.TrimSpace(provider) {
	case "local-noop", "noop":
		return true
	default:
		return false
	}
}

func taskExecHasNamedRunner(req TaskExecRequest) bool {
	return strings.TrimSpace(req.Runner) != "" ||
		strings.TrimSpace(req.RunnerKind) != "" ||
		strings.TrimSpace(req.RunnerEntrypoint) != "" ||
		strings.TrimSpace(req.RunnerRef) != ""
}

func (e HostBridgeTaskExecutor) startFlueTaskSession(ctx context.Context, req TaskExecRequest) (*flueTaskSession, error) {
	if e.Store == nil || !taskExecUsesFlueRuntime(req) {
		return nil, nil
	}
	sessionID := flueTaskSessionID(req)
	metadata := flueTaskSessionMetadata(req, sessionID)
	status := domain.AgentSessionRunning
	if _, err := e.Store.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey:    req.WorkspaceKey,
		SessionID:       sessionID,
		AgentID:         flueTaskAgentID(req),
		NodeID:          req.NodeID,
		Kind:            domain.AgentSessionKindTask,
		TaskID:          req.TaskID,
		ParentSessionID: req.ParentSessionID,
		Status:          status,
		Phase:           "implementation",
		Metadata:        metadata,
	}); err != nil {
		if !errors.Is(err, domain.ErrAlreadyExists) {
			return nil, fmt.Errorf("create flue task agent session: %w", err)
		}
		existing, getErr := e.Store.AgentSessions().Get(ctx, req.WorkspaceKey, sessionID)
		if getErr != nil {
			return nil, fmt.Errorf("get existing flue task agent session: %w", getErr)
		}
		metadata = mergeStringMaps(existing.Metadata, metadata)
		if _, updateErr := e.Store.AgentSessions().Update(ctx, req.WorkspaceKey, sessionID, store.AgentSessionUpdate{
			NodeID:   optionalString(req.NodeID),
			TaskID:   optionalString(req.TaskID),
			Status:   &status,
			Phase:    optionalString("implementation"),
			Metadata: &metadata,
		}); updateErr != nil {
			return nil, fmt.Errorf("update existing flue task agent session: %w", updateErr)
		}
	}
	hbCtx, cancel := context.WithCancel(ctx)
	go heartbeatFlueTaskSession(hbCtx, e.Store, req.WorkspaceKey, sessionID, 30*time.Second)
	return &flueTaskSession{SessionID: sessionID, Metadata: metadata, cancel: cancel}, nil
}

func heartbeatFlueTaskSession(ctx context.Context, st store.Store, workspaceKey, sessionID string, interval time.Duration) {
	if st == nil || workspaceKey == "" || sessionID == "" || interval <= 0 {
		return
	}
	timer := time.NewTimer(interval)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			_, _ = st.AgentSessions().Heartbeat(ctx, workspaceKey, sessionID)
			timer.Reset(interval)
		}
	}
}

func (e HostBridgeTaskExecutor) finishFlueTaskSession(ctx context.Context, req TaskExecRequest, session *flueTaskSession, result TaskExecResult, runner *bridgeTaskRunnerResult, execErr error) error {
	if e.Store == nil || session == nil {
		return nil
	}
	if session.cancel != nil {
		session.cancel()
	}
	status := flueTaskSessionStatus(result, execErr)
	metadata := mergeStringMaps(session.Metadata, result.RuntimeMetadata)
	if runner != nil {
		if sessionID := firstNonEmpty(runner.SessionID, runner.SessionIDCamel); sessionID != "" {
			metadata["driver_runner_session_id"] = sessionID
		}
	}
	if result.LogsRef != "" {
		metadata["logs_ref"] = result.LogsRef
	}
	if result.ArtifactsRef != "" {
		metadata["artifacts_ref"] = result.ArtifactsRef
	}
	if execErr != nil {
		metadata["task_runner_error"] = execErr.Error()
	}
	exitCode := result.ExitCode
	if status != domain.AgentSessionCompleted && exitCode == 0 {
		exitCode = 1
	}
	exitCodePtr := &exitCode
	finishedAt := time.Now().UTC()
	finishedAtPtr := &finishedAt
	errorClass := result.ErrorClass
	if execErr != nil && errorClass == "" {
		errorClass = "task_runner_error"
	}
	summary := "task run completed"
	if status != domain.AgentSessionCompleted {
		summary = firstNonEmpty(result.ErrorMessage, "task run failed")
	}
	return updateFlueAgentSession(ctx, e.Store, req.WorkspaceKey, session.SessionID, store.AgentSessionUpdate{
		Status:     &status,
		FinishedAt: &finishedAtPtr,
		Summary:    &summary,
		ErrorClass: optionalString(errorClass),
		ExitCode:   &exitCodePtr,
		Metadata:   &metadata,
	})
}

func updateFlueAgentSession(ctx context.Context, st store.Store, workspaceKey, sessionID string, patch store.AgentSessionUpdate) error {
	if _, err := st.AgentSessions().Update(ctx, workspaceKey, sessionID, patch); err != nil {
		return fmt.Errorf("update flue task agent session: %w", err)
	}
	return nil
}

func flueTaskSessionStatus(result TaskExecResult, execErr error) domain.AgentSessionStatus {
	if execErr != nil {
		return domain.AgentSessionFailed
	}
	switch result.Status {
	case domain.TaskRunCompleted:
		if result.ExitCode == 0 {
			return domain.AgentSessionCompleted
		}
		return domain.AgentSessionFailed
	case domain.TaskRunCancelled:
		return domain.AgentSessionCancelled
	default:
		// Empty/non-terminal status is never success: an empty result maps to
		// failed (no fake completion).
		return domain.AgentSessionFailed
	}
}

func taskExecUsesFlueRuntime(req TaskExecRequest) bool {
	return strings.TrimSpace(req.RunnerKind) == RunnerKindFlueWorkflow
}

func flueTaskSessionID(req TaskExecRequest) string {
	return "flue-" + req.TaskRunID
}

func flueTaskAgentID(req TaskExecRequest) string {
	return firstNonEmpty(req.WorkerProfileID, req.RunnerPlacement.RunnerID, req.RunnerPlacement.Provider, "flue-task-agent")
}

func flueTaskSessionMetadata(req TaskExecRequest, sessionID string) map[string]string {
	metadata := map[string]string{
		"backend":                  "flue",
		"runtime":                  "flue",
		"task_id":                  req.TaskID,
		"task_run_id":              req.TaskRunID,
		"driver_run_id":            req.DriverRunID,
		"runner":                   req.Runner,
		"runner_ref":               req.RunnerRef,
		"runner_kind":              req.RunnerKind,
		"runner_entrypoint":        req.RunnerEntrypoint,
		"runner_driver_version_id": req.RunnerVersionID,
		"provider_profile":         req.ProviderProfile,
		"flue_session":             sessionID,
		"flue_harness":             "task-agent",
	}
	if req.DriverStepID != "" {
		metadata["driver_step_id"] = req.DriverStepID
	}
	if req.ParentSessionID != "" {
		metadata["parent_session_id"] = req.ParentSessionID
	}
	return metadata
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
	cmd.Env = append(taskRunnerBaseEnvForRequest(req, os.Environ()), e.taskRunnerEnv(req, string(input))...)
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
	cmd.Env = append(taskRunnerBaseEnvForRequest(req, os.Environ()), e.taskRunnerEnv(req, string(input))...)
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

func (e HostBridgeTaskExecutor) taskRunnerEnv(req TaskExecRequest, requestJSON string) []string {
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
	env = append(env, e.taskRunnerBundleEnv(req)...)
	if isLocalTaskRunner(req) {
		env = append(env, TaskRunnerBackendEnv+"="+e.resolveTaskRunnerBackend(req))
	}
	return env
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
	if e.Store == nil || strings.TrimSpace(e.WorktreePath) == "" || strings.TrimSpace(req.RunnerVersionID) == "" {
		return nil
	}
	ctx := context.Background()
	version, err := e.Store.DriverVersions().Get(ctx, req.WorkspaceKey, req.RunnerVersionID)
	if err != nil || version.BundleRef == "" {
		return nil
	}
	bundleRoot, err := safeBundleRoot(e.WorktreePath, version.BundleRef)
	if err != nil {
		return nil
	}
	manifest, serverPath, err := verifyBundleManifest(bundleRoot, version.BundleDigest)
	if err != nil {
		return nil
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return nil
	}
	return []string{
		"LOOM_TASK_RUNNER_BUNDLE_ROOT=" + bundleRoot,
		"LOOM_TASK_RUNNER_SERVER_PATH=" + serverPath,
		"LOOM_TASK_RUNNER_MANIFEST_JSON=" + string(encoded),
	}
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

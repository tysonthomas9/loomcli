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

const flueTaskSessionFinalizeTimeout = 10 * time.Second

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

func localWorktreeResolutionFailure(err error) TaskExecResult {
	message := "local task runner worktree is not provisioned"
	if err != nil {
		message += ": " + err.Error()
	}
	return TaskExecResult{
		Status:       domain.TaskRunFailed,
		ExitCode:     1,
		ErrorClass:   ErrorClassLocalWorktreeUnprovisioned,
		ErrorMessage: message,
		RuntimeMetadata: map[string]string{
			ErrorCodeOutputKey: ErrorClassLocalWorktreeUnprovisioned,
			RetryableOutputKey: "false",
		},
	}
}

func (e *HostBridgeTaskExecutor) resolveLocalTaskWorktree(ctx context.Context, req TaskExecRequest) (TaskWorktree, TaskExecResult, bool) {
	if !isLocalTaskRunner(req) || e.WorktreeResolver == nil {
		return TaskWorktree{}, TaskExecResult{}, false
	}
	resolved, err := e.WorktreeResolver.ResolveTaskWorktree(ctx, req, e.WorktreePath)
	if err != nil {
		return TaskWorktree{}, localWorktreeResolutionFailure(err), true
	}
	if strings.TrimSpace(resolved.Path) != "" {
		// Retain the driver base (the pre-swap WorktreePath) so taskRunnerBundleEnv can still find
		// the runner bundle at <base>/.loom/drivers/<version>; the per-run worktree below is a git
		// worktree of the target repo and does not carry the bundle.
		if strings.TrimSpace(e.driverBundleBaseDir) == "" {
			e.driverBundleBaseDir = e.WorktreePath
		}
		e.WorktreePath = resolved.Path
	}
	return resolved, TaskExecResult{}, false
}

func refuseUntrustedTaskRunnerPreflight(opts TaskRunRequestOptions) error {
	trust := taskRunnerTrustLevel(opts.RunnerTrustLevel)
	if trust.Trusted() {
		return nil
	}
	return fmt.Errorf("%s: child runner %q is untrusted and the host bridge does not isolate runner code: %w", ErrorClassSandboxRequired, opts.Runner, domain.ErrInvalid)
}

func refuseUntrustedTaskRunnerExecution(req TaskExecRequest) (TaskExecResult, bool) {
	if !taskExecHasNamedRunner(req) {
		return TaskExecResult{}, false
	}
	trust := taskRunnerTrustLevel(req.RunnerTrustLevel)
	if trust.Trusted() {
		return TaskExecResult{}, false
	}
	runner := firstNonEmpty(req.Runner, req.RunnerEntrypoint, req.RunnerKind, "<unknown>")
	return TaskExecResult{
		Status:       domain.TaskRunFailed,
		ExitCode:     1,
		ErrorClass:   ErrorClassSandboxRequired,
		ErrorMessage: fmt.Sprintf("child runner %q is untrusted and the host bridge does not isolate runner code", runner),
		RuntimeMetadata: map[string]string{
			ErrorCodeOutputKey:       ErrorClassSandboxRequired,
			RetryableOutputKey:       "false",
			"runner_trust_level":     string(domain.DriverTrustUntrusted),
			SandboxLauncherOutputKey: SandboxProviderProcess,
		},
	}, true
}

func taskRunnerTrustLevel(trust domain.DriverTrustLevel) domain.DriverTrustLevel {
	if trust.Trusted() {
		return domain.DriverTrustTrusted
	}
	return domain.DriverTrustUntrusted
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
	heartbeatDone := startFlueTaskSessionHeartbeat(hbCtx, e.Store, req.WorkspaceKey, sessionID, 30*time.Second)
	return &flueTaskSession{
		SessionID:     sessionID,
		Metadata:      metadata,
		cancel:        cancel,
		heartbeatDone: heartbeatDone,
	}, nil
}

func startFlueTaskSessionHeartbeat(
	ctx context.Context,
	st store.Store,
	workspaceKey string,
	sessionID string,
	interval time.Duration,
) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		heartbeatFlueTaskSession(ctx, st, workspaceKey, sessionID, interval)
	}()
	return done
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
	stopFlueTaskSessionHeartbeat(session)
	// Execution cancellation is itself a normal terminal outcome. Once the
	// heartbeat is drained, use an independent bounded context so a canceled
	// task cannot leave its AgentSession running without further heartbeats.
	finalizeCtx, cancelFinalize := context.WithTimeout(context.WithoutCancel(ctx), flueTaskSessionFinalizeTimeout)
	defer cancelFinalize()
	status := flueTaskSessionStatus(result, execErr)
	metadata := mergeStringMaps(session.Metadata, result.RuntimeMetadata)
	if metadata == nil {
		metadata = make(map[string]string)
	}
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
	return updateFlueAgentSession(finalizeCtx, e.Store, req.WorkspaceKey, session.SessionID, store.AgentSessionUpdate{
		Status:     &status,
		FinishedAt: &finishedAtPtr,
		Summary:    &summary,
		ErrorClass: optionalString(errorClass),
		ExitCode:   &exitCodePtr,
		Metadata:   &metadata,
	})
}

func stopFlueTaskSessionHeartbeat(session *flueTaskSession) {
	if session.cancel != nil {
		session.cancel()
	}
	if session.heartbeatDone != nil {
		// Heartbeat is a non-CAS read/modify/write in the Redis store. Drain an
		// in-flight call before writing terminal state so a stale running record
		// cannot be committed after completion.
		<-session.heartbeatDone
	}
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

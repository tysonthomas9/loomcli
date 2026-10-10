package driver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/localworkspace"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/taskcopy"
	"github.com/tysonthomas9/loomcli/internal/store"
)

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
	errorClass := ErrorClassLocalWorktreeUnprovisioned
	if err != nil {
		message += ": " + err.Error()
		var coded interface{ Code() string }
		if errors.As(err, &coded) && coded.Code() == "task_copy_create_failed" {
			errorClass = coded.Code()
		}
	}
	return TaskExecResult{
		Status:       domain.TaskRunFailed,
		ExitCode:     1,
		ErrorClass:   errorClass,
		ErrorMessage: message,
		RuntimeMetadata: map[string]string{
			ErrorCodeOutputKey: errorClass,
			RetryableOutputKey: "false",
		},
	}
}

func withTaskWorktreeMetadata(result TaskExecResult, wt TaskWorktree) TaskExecResult {
	if wt.Path == "" {
		return result
	}
	result.RuntimeMetadata = mergeStringMaps(result.RuntimeMetadata, map[string]string{
		"worktree_path":    wt.Path,
		"task_copy_path":   wt.Path,
		"attempt_id":       wt.AttemptID,
		"attempt_base_sha": wt.BaseSHA,
		"repo_name":        wt.RepoName,
		"source_repo_id":   wt.SourceRepoID,
		"worktree_source":  "local_workspace_state",
		"task_copy_kind":   wt.Kind,
		"task_copy_reason": wt.Reason,
		"source_repo_path": wt.SourcePath,
	})
	return result
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
		e.taskCopyBaseSHA = resolved.BaseSHA
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
	if ctx.Err() != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
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

// A task starts while its only open blocker's code awaits review when that
// blocker is in its epic (Tyson, 2026-10-09). Its task copy is built on the
// blocker's newest frozen revision, pinned as local lineage, whether a lead
// delegated it or not; otherwise it would run without the code it depends
// on. A task whose blockers are closed keeps its usual base.

// CodeReviewBaseLookup names the task whose frozen revision task is built on
// while that task's code awaits review; found is false for any other task.
type CodeReviewBaseLookup func(ctx context.Context, workspace, task string) (string, bool, error)

var codeReviewBases atomic.Pointer[CodeReviewBaseLookup]

// UseCodeReviewBases sets the code-review base lookup DefaultStackLineageLookup
// uses. The processes that own the FleetDB connection set it at startup: loom
// serve and the loom driver commands register cli.CodeReviewBase. Unset, no
// task copy is built behind code review.
func UseCodeReviewBases(lookup CodeReviewBaseLookup) {
	codeReviewBases.Store(&lookup)
}

func registeredCodeReviewBases() CodeReviewBaseLookup {
	if lookup := codeReviewBases.Load(); lookup != nil {
		return *lookup
	}
	return nil
}

// codeReviewBase returns task's code-review base in repo. A blocker with no
// change in repo (its code is in other repositories) is none: the task keeps
// its usual base there, as it would once the blocker closed. A failed lookup
// stops the task copy: guessing would build the task without its blocker's
// code.
func (l StackLineageLookup) codeReviewBase(ctx context.Context, workspace, repo, task string) (string, bool, error) {
	if l.CodeReviewBase == nil {
		return "", false, nil
	}
	predecessor, found, err := l.CodeReviewBase(ctx, workspace, task)
	if err != nil {
		return "", false, loomgit.NewError(loomgit.LineageUnresolved, "read the blocker whose code awaits review", err)
	}
	if !found {
		return "", false, nil
	}
	inRepo, err := taskcopy.TaskHasChange(ctx, workspace, predecessor, repo)
	return predecessor, inRepo && err == nil, err
}

type codeReviewBaseLookup interface {
	codeReviewBase(context.Context, string, string, string) (string, bool, error)
}

// choosesBase reports whether a delegated task names its own base: a base
// revision or a conflict resolution.
func choosesBase(input json.RawMessage) (bool, error) {
	_, hasRevision, err := baseRevisionFromInput(input)
	if err != nil || hasRevision {
		return hasRevision, err
	}
	_, hasResolution, err := conflictResolutionFromInput(input)
	return hasResolution, err
}

type localPredecessorLookup interface {
	PredecessorForTask(context.Context, string, string, string) (string, bool, error)
}

func (l StackLineageLookup) PredecessorForTask(ctx context.Context, workspaceKey, repoName, taskID string) (string, bool, error) {
	if predecessor, found, err := l.codeReviewBase(ctx, workspaceKey, repoName, taskID); err != nil || found {
		return predecessor, found, err
	}
	_, node, byTask, ok, err := findTaskStack(ctx, l.Store, workspaceKey, repoName, taskID)
	if err != nil || !ok || node.BaseTaskID == "" {
		return "", false, err
	}
	if _, exists := byTask[node.BaseTaskID]; !exists {
		return "", false, loomgit.NewError(loomgit.LineageUnresolved, "predecessor missing from local stack", nil)
	}
	return node.BaseTaskID, true, nil
}

// codeReviewLineageBase builds a lead-delegated task on its blocker's frozen
// revision while that blocker's code awaits review in its epic, instead of on
// the lead's working area. found is false when the lead's base applies.
func (r LocalTaskWorktreeResolver) codeReviewLineageBase(ctx context.Context, req TaskExecRequest, repoPath string, selected *domain.Repo) (bool, string, taskcopy.LineageBase, error) {
	lookup, ok := r.Lineage.(codeReviewBaseLookup)
	if !ok {
		return false, "", taskcopy.LineageBase{}, nil
	}
	if req.ParentSessionID == "" {
		return false, "", taskcopy.LineageBase{}, nil // The plain path asks PredecessorForTask.
	}
	chosen, err := choosesBase(req.Input)
	if err != nil || chosen {
		return false, "", taskcopy.LineageBase{}, err
	}
	predecessor, found, err := lookup.codeReviewBase(ctx, req.WorkspaceKey, selected.Name, req.TaskID)
	if err != nil || !found {
		return false, "", taskcopy.LineageBase{}, err
	}
	base, err := taskcopy.ResolveLineageBase(ctx, repoPath, req.WorkspaceKey, req.TaskID, predecessor, selected.Name)
	return err == nil, base.Ref, base, err
}

// resolveTaskLineageBase selects an immutable local predecessor head when one
// exists. Roots and tasks without local lineage retain their branch selection.
func (r LocalTaskWorktreeResolver) resolveTaskLineageBase(ctx context.Context, req TaskExecRequest, repoPath string, selected *domain.Repo) (string, taskcopy.LineageBase, error) {
	if lookup, ok := r.Lineage.(localPredecessorLookup); ok {
		predecessor, found, err := lookup.PredecessorForTask(ctx, req.WorkspaceKey, selected.Name, req.TaskID)
		if err != nil {
			return "", taskcopy.LineageBase{}, err
		}
		if found {
			base, err := taskcopy.ResolveLineageBase(ctx, repoPath, req.WorkspaceKey, req.TaskID, predecessor, selected.Name)
			return base.Ref, base, err
		}
	}
	branch, err := r.baseBranchForTask(ctx, req.WorkspaceKey, selected, req)
	if err != nil {
		return "", taskcopy.LineageBase{}, err
	}
	sha, err := localworkspace.ResolveTaskBase(repoPath, repoRemote(selected), branch)
	if err != nil {
		return "", taskcopy.LineageBase{}, fmt.Errorf("resolve task copy base for repo %q: %w", selected.Name, err)
	}
	return sha, taskcopy.LineageBase{}, nil
}

// ResolveDependentBase is the frozen blocker revision a daemon-run dependent
// task starts from (P1.28), selected and pinned as a TaskRun copy's base is.
// found is false for a task with no local predecessor.
func ResolveDependentBase(ctx context.Context, workspace, repo, repoPath, task string) (string, bool, error) {
	lookup, ok := DefaultStackLineageLookup().(localPredecessorLookup)
	if !ok {
		return "", false, nil
	}
	predecessor, found, err := lookup.PredecessorForTask(ctx, workspace, repo, task)
	if err != nil || !found {
		return "", false, err
	}
	base, err := taskcopy.ResolveLineageBase(ctx, repoPath, workspace, task, predecessor, repo)
	if err != nil {
		return "", false, err
	}
	if err := taskcopy.RecordLineageBase(ctx, workspace, task, repo, base); err != nil {
		return "", false, err
	}
	return base.SHA, true, nil
}

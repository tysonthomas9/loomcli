package driver

import (
	"context"
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"

	"github.com/tysonthomas9/loomcli/internal/domain"
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
	e.repositoryRemote = strings.TrimSpace(resolved.RepositoryRemote)
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
			"runner_trust_level":     string(workflowcatalog.DriverTrustUntrusted),
			SandboxLauncherOutputKey: SandboxProviderProcess,
		},
	}, true
}

func taskRunnerTrustLevel(trust workflowcatalog.DriverTrustLevel) workflowcatalog.DriverTrustLevel {
	if trust.Trusted() {
		return workflowcatalog.DriverTrustTrusted
	}
	return workflowcatalog.DriverTrustUntrusted
}

func taskExecUsesFlueRuntime(req TaskExecRequest) bool {
	return strings.TrimSpace(req.RunnerKind) == RunnerKindFlueWorkflow
}

package driver

import (
	"context"
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

// validateBridgeTaskRunnerResult rejects non-terminal runner output and false
// success, before bridge-owned artifact or patch persistence can begin.
func validateBridgeTaskRunnerResult(r bridgeTaskRunnerResult) (string, bool) {
	status := strings.TrimSpace(string(r.Status))
	if status == "" {
		return "task runner result missing terminal status", false
	}
	if !r.Status.IsTerminal() {
		return fmt.Sprintf("task runner result status %q is not terminal", status), false
	}
	if r.Status == domain.TaskRunCompleted && bridgeResultExitCode(r) != 0 {
		return fmt.Sprintf("task runner reported completed with non-zero exit code %d", bridgeResultExitCode(r)), false
	}
	return "", true
}

func bridgeResultExitCode(r bridgeTaskRunnerResult) int {
	if r.ExitCode != nil {
		return *r.ExitCode
	}
	if r.ExitCodeCamel != nil {
		return *r.ExitCodeCamel
	}
	return 0
}

func invalidBridgeTaskExecResult(r bridgeTaskRunnerResult, reason string) TaskExecResult {
	metadata := cloneStringMap(firstNonNilMap(r.RuntimeMetadata, r.RuntimeMetadataCamel))
	if metadata == nil {
		metadata = map[string]string{}
	}
	metadata["invalid_task_result_reason"] = reason
	return TaskExecResult{
		Status:          domain.TaskRunFailed,
		ExitCode:        1,
		RuntimeMetadata: metadata,
		ErrorClass:      "invalid_task_result",
		ErrorMessage:    firstNonEmpty(r.ErrorMessage, r.ErrorMessageCamel, reason),
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
		if strings.TrimSpace(e.driverBundleBaseDir) == "" {
			e.driverBundleBaseDir = e.WorktreePath
		}
		e.WorktreePath = resolved.Path
	}
	return resolved, TaskExecResult{}, false
}

func refuseUntrustedTaskRunnerPreflight(opts TaskRunRequestOptions) error {
	if taskRunnerTrustLevel(opts.RunnerTrustLevel).Trusted() {
		return nil
	}
	return fmt.Errorf("%s: child runner %q is untrusted and the host bridge does not isolate runner code: %w", ErrorClassSandboxRequired, opts.Runner, domain.ErrInvalid)
}

func refuseUntrustedTaskRunnerExecution(req TaskExecRequest) (TaskExecResult, bool) {
	if !taskExecHasNamedRunner(req) || taskRunnerTrustLevel(req.RunnerTrustLevel).Trusted() {
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

func taskExecUsesFlueRuntime(req TaskExecRequest) bool {
	return strings.TrimSpace(req.RunnerKind) == RunnerKindFlueWorkflow
}

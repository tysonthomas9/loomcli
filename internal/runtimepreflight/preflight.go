// Package runtimepreflight evaluates whether a resolved AI backend can run
// the bundled local task runner.
package runtimepreflight

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/tysonthomas9/loomcli/internal/backendnames"
	"github.com/tysonthomas9/loomcli/internal/cli/backends"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/localbackend"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// Request identifies the local task runner target to evaluate.
type Request struct {
	WorkspaceKey    string
	AgentName       string
	AgentRequired   bool
	BackendOverride string
}

// BackendSource records which precedence level selected the backend.
type BackendSource string

const (
	BackendSourceOverride  BackendSource = "override"
	BackendSourceAgent     BackendSource = "agent"
	BackendSourceWorkspace BackendSource = "workspace"
	BackendSourceDefault   BackendSource = "default"
)

// ErrorClass is a stable local task runner readiness failure class.
type ErrorClass string

const (
	ErrorClassUnavailable      ErrorClass = "local_backend_unavailable"
	ErrorClassUnsupported      ErrorClass = "local_backend_unsupported"
	ErrorClassAuthMissing      ErrorClass = "local_backend_auth_missing"
	ErrorClassUnhealthy        ErrorClass = "local_backend_unhealthy"
	ErrorClassResolutionFailed ErrorClass = "local_backend_resolution_failed"
)

// HealthStatus is the raw backend-adapter health projection.
type HealthStatus = backends.HealthStatus

// Result is the canonical local task runner readiness verdict.
type Result struct {
	Backend       string        `json:"backend,omitempty"`
	BackendSource BackendSource `json:"backend_source,omitempty"`
	Ready         bool          `json:"ready"`
	Health        *HealthStatus `json:"health,omitempty"`
	ErrorClass    ErrorClass    `json:"error_class,omitempty"`
	Message       string        `json:"message"`
	Remediation   []string      `json:"remediation,omitempty"`
}

// NotReadyError is the automatic-gate projection of a completed not-ready
// verdict. Result remains available to callers that need structured output.
type NotReadyError struct {
	Result Result
}

func (e *NotReadyError) Error() string {
	if e == nil {
		return "local task runner is not ready"
	}
	message := e.Result.Message
	if e.Result.ErrorClass != "" {
		message += fmt.Sprintf(" (%s)", e.Result.ErrorClass)
	}
	if len(e.Result.Remediation) > 0 {
		message += "; next: " + e.Result.Remediation[0]
	}
	return message
}

// PreflightClass exposes the canonical class without requiring callers to
// import this package's concrete error type.
func (e *NotReadyError) PreflightClass() string {
	if e == nil {
		return ""
	}
	return string(e.Result.ErrorClass)
}

type targetStore interface {
	Agents() store.AgentStore
	Daemon() store.DaemonProfileStore
}

type healthProbe func(name string) (HealthStatus, bool)

var (
	healthCheckerMu sync.RWMutex
	healthChecker   healthProbe = backends.CheckBackendHealth
)

// CheckLocalTaskRunner resolves a target and returns the most complete safe
// verdict available. Its error is non-nil only when evaluation did not finish.
func CheckLocalTaskRunner(ctx context.Context, st targetStore, req Request) (Result, error) {
	healthCheckerMu.RLock()
	probe := healthChecker
	healthCheckerMu.RUnlock()
	return checkLocalTaskRunner(ctx, st, req, probe)
}

func checkLocalTaskRunner(ctx context.Context, st targetStore, req Request, probe healthProbe) (Result, error) {
	backend, source, err := localbackend.Resolve(
		ctx,
		st,
		req.WorkspaceKey,
		req.AgentName,
		req.AgentRequired,
		req.BackendOverride,
	)
	result := Result{Backend: backend, BackendSource: BackendSource(source)}
	if err != nil {
		return resolutionFailure(result, req, err), err
	}
	if err := ctx.Err(); err != nil {
		return evaluationFailure(result, backend), err
	}
	status, ok := probe(backend)
	if err := ctx.Err(); err != nil {
		return evaluationFailure(result, backend), err
	}
	if !ok {
		result.ErrorClass = ErrorClassUnavailable
		result.Message = fmt.Sprintf("backend %s is not available for health checks", backend)
		result.Remediation = []string{chooseSupportedBackendRemediation()}
		return result, nil
	}
	status = boundedHealthStatus(status)
	result.Health = &status
	return classifyLocalTaskRunner(result, status), nil
}

func classifyLocalTaskRunner(result Result, status HealthStatus) Result {
	backend := result.Backend
	if !backendnames.IsLocalTaskRunnerBackend(backend) {
		result.ErrorClass = ErrorClassUnsupported
		result.Message = fmt.Sprintf("backend %s is not supported by the local task runner", backend)
		result.Remediation = []string{chooseSupportedBackendRemediation()}
		return result
	}
	if status.Installed && status.Healthy {
		result.Ready = true
		result.Message = fmt.Sprintf("backend %s is ready for the local task runner", backend)
		return result
	}

	switch {
	case !status.Installed:
		result.ErrorClass = ErrorClassUnavailable
		result.Message = fmt.Sprintf("backend %s CLI is not installed", backend)
		result.Remediation = []string{
			fmt.Sprintf("install the %s CLI", backend),
			chooseSupportedBackendRemediation(),
		}
	case !status.APIKeySet:
		result.ErrorClass = ErrorClassAuthMissing
		result.Message = fmt.Sprintf("backend %s is installed but not authenticated", backend)
		result.Remediation = []string{
			fmt.Sprintf("authenticate the %s CLI or configure credentials", backend),
			"retry the preflight check",
		}
	default:
		result.ErrorClass = ErrorClassUnhealthy
		result.Message = fmt.Sprintf("backend %s is installed and authenticated but not healthy", backend)
		result.Remediation = []string{
			fmt.Sprintf("run loom backend info %s", backend),
			"repair the backend and retry",
		}
	}
	return result
}

// RequireLocalTaskRunner returns operational failures unchanged and converts
// only a completed not-ready verdict into NotReadyError.
func RequireLocalTaskRunner(ctx context.Context, st targetStore, req Request) error {
	result, err := CheckLocalTaskRunner(ctx, st, req)
	if err != nil {
		return err
	}
	if result.Ready {
		return nil
	}
	return &NotReadyError{Result: result}
}

// SetHealthCheckerForTest overrides the backend health checker and returns a
// restore function. External suites use it to avoid depending on host CLIs.
func SetHealthCheckerForTest(fn func(name string) (HealthStatus, bool)) (restore func()) {
	healthCheckerMu.Lock()
	previous := healthChecker
	healthChecker = fn
	healthCheckerMu.Unlock()
	return func() {
		healthCheckerMu.Lock()
		healthChecker = previous
		healthCheckerMu.Unlock()
	}
}

func resolutionFailure(result Result, req Request, err error) Result {
	result.ErrorClass = ErrorClassResolutionFailed
	agentName := strings.TrimSpace(req.AgentName)
	workspaceKey := strings.TrimSpace(req.WorkspaceKey)
	switch {
	case agentName != "" && errors.Is(err, domain.ErrNotFound):
		result.Message = fmt.Sprintf("agent %q was not found in workspace %q", agentName, workspaceKey)
		result.Remediation = []string{"verify the agent name and workspace, then retry"}
	case agentName != "" && workspaceKey == "":
		result.Message = fmt.Sprintf("agent %q requires an active workspace", agentName)
		result.Remediation = []string{"select a workspace and retry"}
	case agentName != "":
		result.Message = fmt.Sprintf("backend configuration for agent %q in workspace %q could not be read", agentName, workspaceKey)
		result.Remediation = []string{"repair workspace connectivity or configuration and retry"}
	case workspaceKey != "":
		result.Message = fmt.Sprintf("backend configuration for workspace %q could not be read", workspaceKey)
		result.Remediation = []string{"repair workspace connectivity or configuration and retry"}
	default:
		result.Message = "an active workspace is required when no backend override is provided"
		result.Remediation = []string{"select a workspace or pass --ai-backend"}
	}
	return result
}

func evaluationFailure(result Result, backend string) Result {
	result.ErrorClass = ErrorClassResolutionFailed
	result.Message = fmt.Sprintf("health evaluation for backend %s could not be completed", backend)
	result.Remediation = []string{"retry the preflight check"}
	return result
}

func chooseSupportedBackendRemediation() string {
	return "choose a runner-supported backend: " + strings.Join(backendnames.LocalTaskRunnerBackends(), ", ")
}

// Adapter-authored strings remain passthrough, matching loom backend health,
// but are capped so an external adapter cannot make reports unbounded.
func boundedHealthStatus(status HealthStatus) HealthStatus {
	status.Version = boundedHealthText(status.Version)
	status.Message = boundedHealthText(status.Message)
	return status
}

func boundedHealthText(value string) string {
	const maxRunes = 4096
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	return string(runes[:maxRunes-1]) + "…"
}

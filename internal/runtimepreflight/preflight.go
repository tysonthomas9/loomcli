// Package runtimepreflight holds fail-closed checks that run BEFORE a runner
// is queued. Today it guards the local task runner: the real local runner
// shells out to the user-selected backend CLI (claude/codex/opencode/gemini/
// cursor), so if that binary or its auth is missing the run would fake-complete
// or fail deep in the worker. Preflight resolves the effective backend and
// runs that backend's HealthCheck up front so `loom epic run` and the UI
// epic-start path fail with a clear, actionable message instead.
//
// The package is deliberately neutral (no CLI/webui imports) so both the CLI
// (internal/cli/epic) and the webui handler (internal/webui/handlers/workflows)
// can depend on it without an import cycle.
package runtimepreflight

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/backendnames"
	"github.com/tysonthomas9/loomcli/internal/cli/backends"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// LocalTaskRunnerEntrypoint is the runner name that routes to the bundled
// local task runner (which execs the backend CLI). Preflight only fires for
// this runner; daytona/openshell/other explicit runners are not gated here.
const LocalTaskRunnerEntrypoint = "local-task-runner"

// DefaultBackend mirrors the workspace default backend (Settings "Project
// Default Backend") used when no DaemonProfile.AgentBackend is set.
const DefaultBackend = backendnames.Codex

const (
	// ErrorClassBackendUnavailable identifies a backend that cannot be used by
	// the local task runner because its CLI or health check is unavailable.
	ErrorClassBackendUnavailable = "local_backend_unavailable"
	// ErrorClassBackendAuthMissing identifies a backend that is installed but
	// cannot run because provider auth is missing.
	ErrorClassBackendAuthMissing = "local_backend_auth_missing"
)

// HealthStatus describes backend readiness for local-runner preflight tests
// without making webui packages import the CLI backend package directly.
type HealthStatus = backends.HealthStatus

// LocalTaskRunnerPreflightResult is the structured form of the local runner
// backend readiness check. Message mirrors the user-facing error returned by
// PreflightLocalTaskRunner when Ready is false.
type LocalTaskRunnerPreflightResult struct {
	Backend    string
	Health     HealthStatus
	Ready      bool
	ErrorClass string
	Message    string
}

// healthChecker reports a backend's installation/auth status by name. It is a
// package var so tests can stub the backend registry without registering real
// backends. Defaults to backends.CheckBackendHealth, which reads the global
// backend registry populated by internal/cli/backends init().
var healthChecker = backends.CheckBackendHealth

// daemonGetter is the minimal store surface preflight needs: the per-workspace
// daemon profile carrying AgentBackend. Implemented by store.Store via
// store.DaemonProfileStore.
type daemonGetter interface {
	Daemon() store.DaemonProfileStore
}

// ResolveLocalBackend returns the effective backend for the local task runner
// in workspace ws, mirroring service.GetWorkspaceBackend precedence: the
// DaemonProfile.AgentBackend when set, else the default (codex). A per-agent
// override is not known at epic-run/queue time (no specific agent is bound
// yet), so the workspace default is authoritative here.
func ResolveLocalBackend(ctx context.Context, st daemonGetter, ws string) string {
	if st == nil {
		return DefaultBackend
	}
	profile, err := st.Daemon().Get(ctx, ws)
	if err != nil || profile == nil {
		return DefaultBackend
	}
	if backend := strings.TrimSpace(profile.AgentBackend); backend != "" {
		return backend
	}
	return DefaultBackend
}

// CheckLocalTaskRunner resolves the effective local-task-runner backend (or
// uses backendOverride when non-empty) and returns structured readiness state.
func CheckLocalTaskRunner(ctx context.Context, st daemonGetter, ws, backendOverride string) LocalTaskRunnerPreflightResult {
	backend := strings.TrimSpace(backendOverride)
	if backend == "" {
		backend = ResolveLocalBackend(ctx, st, ws)
	}

	status, ok := healthChecker(backend)
	result := LocalTaskRunnerPreflightResult{
		Backend: backend,
		Health:  status,
		Ready:   ok && status.Healthy,
	}
	if result.Ready {
		return result
	}
	if !ok {
		result.ErrorClass = ErrorClassBackendUnavailable
		result.Message = fmt.Sprintf("local task runner backend %q is not available for health checks; "+
			"set a supported Project Default Backend (claude, codex, opencode, gemini, cursor) (%s)",
			backend, result.ErrorClass)
		return result
	}

	switch {
	case !status.Installed:
		result.ErrorClass = ErrorClassBackendUnavailable
		result.Message = fmt.Sprintf("local task runner cannot start: backend %q CLI is not installed (%s); "+
			"install it or switch the Project Default Backend (%s)",
			backend, healthMessage(status), result.ErrorClass)
	case !status.APIKeySet:
		result.ErrorClass = ErrorClassBackendAuthMissing
		result.Message = fmt.Sprintf("local task runner cannot start: backend %q is missing auth (%s); "+
			"set the provider credentials or switch the Project Default Backend (%s)",
			backend, healthMessage(status), result.ErrorClass)
	default:
		result.ErrorClass = ErrorClassBackendUnavailable
		result.Message = fmt.Sprintf("local task runner cannot start: backend %q is not ready (%s) (%s)",
			backend, healthMessage(status), result.ErrorClass)
	}
	return result
}

// PreflightLocalTaskRunner resolves the effective backend for workspace ws and
// runs that backend's HealthCheck. It returns a clear, actionable error if the
// local runner cannot execute (backend binary not on PATH, or provider auth
// missing); nil when the backend is healthy.
//
// This is fail-closed by design: a missing binary or missing auth must stop the
// run from being queued rather than letting it surface as a fake completion or
// an opaque deep failure.
func PreflightLocalTaskRunner(ctx context.Context, st daemonGetter, ws string) error {
	result := CheckLocalTaskRunner(ctx, st, ws, "")
	if result.Ready {
		return nil
	}
	if result.Message != "" {
		return errors.New(result.Message)
	}
	return fmt.Errorf("local task runner cannot start: backend %q is not ready", result.Backend)
}

// SetHealthCheckerForTest overrides the backend health checker and returns a
// restore function. Intended for tests that must exercise preflight without a
// real backend CLI/auth on the host (or that depend on local runs queuing
// without gating on the host's backend state).
func SetHealthCheckerForTest(fn func(name string) (HealthStatus, bool)) (restore func()) {
	prev := healthChecker
	healthChecker = fn
	return func() { healthChecker = prev }
}

func healthMessage(status HealthStatus) string {
	if msg := strings.TrimSpace(status.Message); msg != "" {
		return msg
	}
	return "no detail reported"
}

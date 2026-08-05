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
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/backendnames"
	"github.com/tysonthomas9/loomcli/internal/cli/backends"
	"github.com/tysonthomas9/loomcli/internal/localnodeconfig"
)

// LocalTaskRunnerEntrypoint is the runner name that routes to the bundled
// local task runner (which execs the backend CLI). Preflight only fires for
// this runner; daytona/openshell/other explicit runners are not gated here.
const LocalTaskRunnerEntrypoint = "local-task-runner"

// DefaultBackend is used when the local node has no workspace provider
// override in machine-local state.
const DefaultBackend = backendnames.Codex

// HealthStatus describes backend readiness for local-runner preflight tests
// without making webui packages import the CLI backend package directly.
type HealthStatus = backends.HealthStatus

// healthChecker reports a backend's installation/auth status by name. It is a
// package var so tests can stub the backend registry without registering real
// backends. Defaults to backends.CheckBackendHealth, which reads the global
// backend registry populated by internal/cli/backends init().
var healthChecker = backends.CheckBackendHealth

// ResolveLocalBackend returns the effective backend for the local task runner
// in workspace ws. A per-agent override is not known at queue time, so the
// machine-local workspace provider is authoritative here.
func ResolveLocalBackend(ws string) string {
	if backend, err := localnodeconfig.RuntimeProvider(ws); err == nil && backend != "" {
		return backend
	}
	return DefaultBackend
}

// PreflightLocalTaskRunner resolves the effective backend for workspace ws and
// runs that backend's HealthCheck. It returns a clear, actionable error if the
// local runner cannot execute (backend binary not on PATH, or provider auth
// missing); nil when the backend is healthy.
//
// This is fail-closed by design: a missing binary or missing auth must stop the
// run from being queued rather than letting it surface as a fake completion or
// an opaque deep failure.
func PreflightLocalTaskRunner(_ context.Context, ws string) error {
	backend := ResolveLocalBackend(ws)

	status, ok := healthChecker(backend)
	if !ok {
		// Unknown/unregistered backend, or one without a HealthCheck. Fail
		// closed: we cannot prove the local runner can execute.
		return fmt.Errorf("local task runner backend %q is not available for health checks; "+
			"set a supported Project Default Backend (claude, codex, opencode, gemini, cursor)", backend)
	}
	if status.Healthy {
		return nil
	}

	// Distinguish the two fail classes for a precise message; the error-class
	// registry (§4.5) names these local_backend_unavailable / _auth_missing.
	switch {
	case !status.Installed:
		return fmt.Errorf("local task runner cannot start: backend %q CLI is not installed (%s); "+
			"install it or switch the Project Default Backend (local_backend_unavailable)",
			backend, healthMessage(status))
	case !status.APIKeySet:
		return fmt.Errorf("local task runner cannot start: backend %q is missing auth (%s); "+
			"set the provider credentials or switch the Project Default Backend (local_backend_auth_missing)",
			backend, healthMessage(status))
	default:
		return fmt.Errorf("local task runner cannot start: backend %q is not ready (%s)",
			backend, healthMessage(status))
	}
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

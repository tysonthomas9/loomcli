package tsruntime

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/localbackend"
	"github.com/tysonthomas9/loomcli/internal/runtimepreflight"
)

func TestInvokeNonInteractiveChecksAndLaunchesActiveBackend(t *testing.T) {
	restoreTSRuntimeSeams(t)
	active := cli.TestingActiveBackend()
	previousBackend := *active
	*active = "gemini"
	t.Cleanup(func() { *active = previousBackend })
	t.Setenv("LOOM_DAEMON_LEAF_RUNNER", localbackend.LocalTaskRunnerEntrypoint)

	checked := ""
	restoreHealth := runtimepreflight.SetHealthCheckerForTest(func(name string) (runtimepreflight.HealthStatus, bool) {
		checked = name
		return runtimepreflight.HealthStatus{Installed: true, Healthy: true}, true
	})
	t.Cleanup(restoreHealth)
	resolveTaskRunnerBundleServerPath = func() (string, error) { return "/bundle/server.mjs", nil }
	launched := ""
	runBundledTaskRunner = func(_ context.Context, opts driver.BundledRunnerOptions) (json.RawMessage, error) {
		launched = opts.Backend
		return json.RawMessage(`{"status":"completed"}`), nil
	}

	if err := (agentInvoker{}).InvokeNonInteractive(t.TempDir(), "prompt", "worker-a", nil, nil); err != nil {
		t.Fatalf("InvokeNonInteractive: %v", err)
	}
	if checked != "gemini" || launched != "gemini" {
		t.Fatalf("checked/launched backend = %q/%q, want exact active backend gemini", checked, launched)
	}
}

func TestInvokeNonInteractivePreflightFailureStopsLocalLaunch(t *testing.T) {
	restoreTSRuntimeSeams(t)
	active := cli.TestingActiveBackend()
	previousBackend := *active
	*active = "codex"
	t.Cleanup(func() { *active = previousBackend })
	t.Setenv("LOOM_DAEMON_LEAF_RUNNER", localbackend.LocalTaskRunnerEntrypoint)
	restoreHealth := runtimepreflight.SetHealthCheckerForTest(func(string) (runtimepreflight.HealthStatus, bool) {
		return runtimepreflight.HealthStatus{Installed: false}, true
	})
	t.Cleanup(restoreHealth)
	resolveTaskRunnerBundleServerPath = func() (string, error) { return "/bundle/server.mjs", nil }
	runBundledTaskRunner = func(context.Context, driver.BundledRunnerOptions) (json.RawMessage, error) {
		t.Fatal("RunBundledTaskRunner called after failed preflight")
		return nil, nil
	}

	err := (agentInvoker{}).InvokeNonInteractive(t.TempDir(), "prompt", "worker-a", nil, nil)
	var notReady *runtimepreflight.NotReadyError
	if !errors.As(err, &notReady) || notReady.PreflightClass() != string(runtimepreflight.ErrorClassUnavailable) {
		t.Fatalf("InvokeNonInteractive error = %v, want typed unavailable preflight", err)
	}
}

func TestInvokeNonInteractiveRemoteRunnerBypassesLocalPreflight(t *testing.T) {
	restoreTSRuntimeSeams(t)
	t.Setenv("LOOM_DAEMON_LEAF_RUNNER", driver.DaytonaTaskRunnerEntrypoint)
	restoreHealth := runtimepreflight.SetHealthCheckerForTest(func(string) (runtimepreflight.HealthStatus, bool) {
		t.Fatal("remote runner invoked local backend preflight")
		return runtimepreflight.HealthStatus{}, false
	})
	t.Cleanup(restoreHealth)
	resolveTaskRunnerBundleServerPath = func() (string, error) { return "/bundle/server.mjs", nil }
	runBundledTaskRunner = func(context.Context, driver.BundledRunnerOptions) (json.RawMessage, error) {
		return json.RawMessage(`{"status":"completed"}`), nil
	}

	if err := (agentInvoker{}).InvokeNonInteractive(t.TempDir(), "prompt", "worker-a", nil, nil); err != nil {
		t.Fatalf("InvokeNonInteractive: %v", err)
	}
}

func restoreTSRuntimeSeams(t *testing.T) {
	t.Helper()
	previousResolve := resolveTaskRunnerBundleServerPath
	previousRun := runBundledTaskRunner
	t.Cleanup(func() {
		resolveTaskRunnerBundleServerPath = previousResolve
		runBundledTaskRunner = previousRun
	})
}

package tsruntime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
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

func TestInvokeNonInteractivePreflightSkipsVersionProbe(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake CLI fixture is a /bin/sh script")
	}
	restoreTSRuntimeSeams(t)
	active := cli.TestingActiveBackend()
	previousBackend := *active
	*active = "gemini"
	t.Cleanup(func() { *active = previousBackend })
	t.Setenv("LOOM_DAEMON_LEAF_RUNNER", localbackend.LocalTaskRunnerEntrypoint)

	dir := t.TempDir()
	marker := filepath.Join(dir, "version-probed")
	binary := filepath.Join(dir, "gemini")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\n: > \"$LOOM_TEST_VERSION_MARKER\"\necho 9.9.9\n"), 0o755); err != nil {
		t.Fatalf("write fake gemini: %v", err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("GEMINI_API_KEY", "test-key")
	t.Setenv("LOOM_TEST_VERSION_MARKER", marker)
	resolveTaskRunnerBundleServerPath = func() (string, error) { return "/bundle/server.mjs", nil }
	runBundledTaskRunner = func(context.Context, driver.BundledRunnerOptions) (json.RawMessage, error) {
		return json.RawMessage(`{"status":"completed"}`), nil
	}

	if err := (agentInvoker{}).InvokeNonInteractive(t.TempDir(), "prompt", "worker-a", nil, nil); err != nil {
		t.Fatalf("InvokeNonInteractive: %v", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("tsruntime preflight spawned --version; marker stat error = %v", err)
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

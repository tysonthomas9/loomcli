package runtimepreflight

import (
	"context"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli/backends"
	"github.com/tysonthomas9/loomcli/internal/localnodeconfig"
)

func setRuntimeProvider(t *testing.T, backend string) {
	t.Helper()
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	if err := localnodeconfig.SetRuntimeProvider("TEST", backend); err != nil {
		t.Fatalf("set runtime provider: %v", err)
	}
}

func TestResolveLocalBackend(t *testing.T) {
	setRuntimeProvider(t, "claude")
	if got := ResolveLocalBackend("TEST"); got != "claude" {
		t.Fatalf("explicit runtime provider = %q, want claude", got)
	}
	if got := ResolveLocalBackend("OTHER"); got != DefaultBackend {
		t.Fatalf("unset runtime provider = %q, want default %q", got, DefaultBackend)
	}
}

func TestPreflightLocalTaskRunnerHealthy(t *testing.T) {
	restore := SetHealthCheckerForTest(func(name string) (backends.HealthStatus, bool) {
		if name != "codex" {
			t.Fatalf("health checked backend %q, want resolved default codex", name)
		}
		return backends.HealthStatus{Healthy: true, Installed: true, APIKeySet: true}, true
	})
	defer restore()

	setRuntimeProvider(t, "")
	if err := PreflightLocalTaskRunner(context.Background(), "TEST"); err != nil {
		t.Fatalf("healthy backend should pass preflight, got %v", err)
	}
}

func TestPreflightLocalTaskRunnerBinaryMissing(t *testing.T) {
	restore := SetHealthCheckerForTest(func(string) (backends.HealthStatus, bool) {
		return backends.HealthStatus{Installed: false, APIKeySet: false, Message: "codex binary not found on PATH"}, true
	})
	defer restore()

	setRuntimeProvider(t, "codex")
	err := PreflightLocalTaskRunner(context.Background(), "TEST")
	if err == nil {
		t.Fatal("missing binary must fail preflight (fail-closed)")
	}
	if !strings.Contains(err.Error(), "local_backend_unavailable") {
		t.Fatalf("error = %v, want local_backend_unavailable", err)
	}
	if !strings.Contains(err.Error(), "codex") {
		t.Fatalf("error = %v, want to name the backend (codex)", err)
	}
}

func TestPreflightLocalTaskRunnerAuthMissing(t *testing.T) {
	restore := SetHealthCheckerForTest(func(string) (backends.HealthStatus, bool) {
		return backends.HealthStatus{Installed: true, APIKeySet: false, Message: "OPENAI_API_KEY not set"}, true
	})
	defer restore()

	setRuntimeProvider(t, "codex")
	err := PreflightLocalTaskRunner(context.Background(), "TEST")
	if err == nil {
		t.Fatal("missing auth must fail preflight (fail-closed)")
	}
	if !strings.Contains(err.Error(), "local_backend_auth_missing") {
		t.Fatalf("error = %v, want local_backend_auth_missing", err)
	}
}

func TestPreflightLocalTaskRunnerUnknownBackend(t *testing.T) {
	restore := SetHealthCheckerForTest(func(string) (backends.HealthStatus, bool) {
		// Unregistered / no health-check support => (zero, false).
		return backends.HealthStatus{}, false
	})
	defer restore()

	setRuntimeProvider(t, "made-up")
	err := PreflightLocalTaskRunner(context.Background(), "TEST")
	if err == nil {
		t.Fatal("unknown backend must fail closed")
	}
	if !strings.Contains(err.Error(), "made-up") {
		t.Fatalf("error = %v, want to name the unknown backend", err)
	}
}

func TestPreflightResolvesConfiguredBackend(t *testing.T) {
	var checked string
	restore := SetHealthCheckerForTest(func(name string) (backends.HealthStatus, bool) {
		checked = name
		return backends.HealthStatus{Healthy: true, Installed: true, APIKeySet: true}, true
	})
	defer restore()

	setRuntimeProvider(t, "gemini")
	if err := PreflightLocalTaskRunner(context.Background(), "TEST"); err != nil {
		t.Fatalf("preflight error: %v", err)
	}
	if checked != "gemini" {
		t.Fatalf("health-checked backend = %q, want configured gemini", checked)
	}
}

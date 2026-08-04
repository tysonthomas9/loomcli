package runtimepreflight

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli/backends"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// fakeDaemonStore implements store.DaemonProfileStore for resolving the
// workspace AgentBackend without a full store.
type fakeDaemonStore struct {
	profile *domain.DaemonProfile
	err     error
}

func (f fakeDaemonStore) Get(context.Context, string) (*domain.DaemonProfile, error) {
	return f.profile, f.err
}

func (f fakeDaemonStore) Upsert(context.Context, *domain.DaemonProfile) (*domain.DaemonProfile, error) {
	return f.profile, nil
}

// fakeGetter implements the minimal daemonGetter surface preflight needs.
type fakeGetter struct{ ds store.DaemonProfileStore }

func (g fakeGetter) Daemon() store.DaemonProfileStore { return g.ds }

func getterWithBackend(backend string) fakeGetter {
	return fakeGetter{ds: fakeDaemonStore{profile: &domain.DaemonProfile{AgentBackend: backend}}}
}

func TestResolveLocalBackend(t *testing.T) {
	ctx := context.Background()

	if got := ResolveLocalBackend(ctx, getterWithBackend("claude"), "TEST"); got != "claude" {
		t.Fatalf("explicit AgentBackend = %q, want claude", got)
	}
	if got := ResolveLocalBackend(ctx, getterWithBackend("  "), "TEST"); got != DefaultBackend {
		t.Fatalf("blank AgentBackend = %q, want default %q", got, DefaultBackend)
	}
	if got := ResolveLocalBackend(ctx, fakeGetter{ds: fakeDaemonStore{profile: nil}}, "TEST"); got != DefaultBackend {
		t.Fatalf("nil profile = %q, want default %q", got, DefaultBackend)
	}
	if got := ResolveLocalBackend(ctx, fakeGetter{ds: fakeDaemonStore{err: errors.New("boom")}}, "TEST"); got != DefaultBackend {
		t.Fatalf("store error = %q, want default %q", got, DefaultBackend)
	}
	if got := ResolveLocalBackend(ctx, nil, "TEST"); got != DefaultBackend {
		t.Fatalf("nil getter = %q, want default %q", got, DefaultBackend)
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

	if err := PreflightLocalTaskRunner(context.Background(), getterWithBackend(""), "TEST"); err != nil {
		t.Fatalf("healthy backend should pass preflight, got %v", err)
	}
}

func TestPreflightLocalTaskRunnerBinaryMissing(t *testing.T) {
	restore := SetHealthCheckerForTest(func(string) (backends.HealthStatus, bool) {
		return backends.HealthStatus{Installed: false, APIKeySet: false, Message: "codex binary not found on PATH"}, true
	})
	defer restore()

	err := PreflightLocalTaskRunner(context.Background(), getterWithBackend("codex"), "TEST")
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

	err := PreflightLocalTaskRunner(context.Background(), getterWithBackend("codex"), "TEST")
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

	err := PreflightLocalTaskRunner(context.Background(), getterWithBackend("made-up"), "TEST")
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

	if err := PreflightLocalTaskRunner(context.Background(), getterWithBackend("gemini"), "TEST"); err != nil {
		t.Fatalf("preflight error: %v", err)
	}
	if checked != "gemini" {
		t.Fatalf("health-checked backend = %q, want configured gemini", checked)
	}
}

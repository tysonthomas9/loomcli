package epic

import (
	"context"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli/backends"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/runtimepreflight"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// TestRunnerNeedsLocalPreflight pins the R4 gate: the local task runner must be
// preflighted, and an empty/whitespace runner resolves to local-task-runner
// downstream so it must be gated identically (it previously slipped through the
// exact-string match). Explicit non-local runners are NOT gated.
func TestRunnerNeedsLocalPreflight(t *testing.T) {
	cases := []struct {
		name   string
		runner string
		want   bool
	}{
		{"explicit local", runtimepreflight.LocalTaskRunnerEntrypoint, true},
		{"empty resolves to local", "", true},
		{"whitespace resolves to local", "   ", true},
		{"local with surrounding space", "  local-task-runner  ", true},
		{"explicit daytona not gated", "daytona", false},
		{"other explicit runner not gated", "openshell", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runnerNeedsLocalPreflight(tc.runner); got != tc.want {
				t.Fatalf("runnerNeedsLocalPreflight(%q) = %v, want %v", tc.runner, got, tc.want)
			}
		})
	}
}

// daemonGetterStub satisfies the surface PreflightLocalTaskRunner needs so the
// gated branch can be exercised without a full store.
type daemonGetterStub struct{ backend string }

func (s daemonGetterStub) Daemon() store.DaemonProfileStore {
	return daemonProfileStoreStub(s)
}

type daemonProfileStoreStub struct{ backend string }

func (s daemonProfileStoreStub) Get(context.Context, string) (*domain.DaemonProfile, error) {
	return &domain.DaemonProfile{AgentBackend: s.backend}, nil
}

func (s daemonProfileStoreStub) Upsert(context.Context, *domain.DaemonProfile) (*domain.DaemonProfile, error) {
	return &domain.DaemonProfile{AgentBackend: s.backend}, nil
}

// TestEmptyRunnerPreflightsFailClosed proves that when the gate fires for an
// empty runner, the same fail-closed preflight runs (backend CLI/auth missing
// stops the run) — i.e. `--runner ""` is no longer a silent bypass.
func TestEmptyRunnerPreflightsFailClosed(t *testing.T) {
	restore := runtimepreflight.SetHealthCheckerForTest(func(string) (backends.HealthStatus, bool) {
		return backends.HealthStatus{Installed: false, APIKeySet: false, Message: "binary not found on PATH"}, true
	})
	defer restore()

	for _, runner := range []string{"", "   ", runtimepreflight.LocalTaskRunnerEntrypoint} {
		if !runnerNeedsLocalPreflight(runner) {
			t.Fatalf("runner %q must be gated for preflight", runner)
		}
		err := runtimepreflight.PreflightLocalTaskRunner(context.Background(), daemonGetterStub{}, "TEST")
		if err == nil {
			t.Fatalf("runner %q: missing backend must fail closed", runner)
		}
		if !strings.Contains(err.Error(), "local_backend_unavailable") {
			t.Fatalf("runner %q: error = %v, want local_backend_unavailable", runner, err)
		}
	}
}

// TestExplicitNonLocalRunnerSkipsPreflight confirms the gate does not fire for a
// non-local runner, so its own runtime owns readiness.
func TestExplicitNonLocalRunnerSkipsPreflight(t *testing.T) {
	if runnerNeedsLocalPreflight("daytona") {
		t.Fatal("daytona must not trigger local preflight")
	}
}

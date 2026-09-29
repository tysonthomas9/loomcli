package epic

import (
	"context"
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli/backends"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/localbackend"
	"github.com/tysonthomas9/loomcli/internal/runtimepreflight"
	"github.com/tysonthomas9/loomcli/internal/runtimepreflight/preflighttest"
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
		{"explicit local", localbackend.LocalTaskRunnerEntrypoint, true},
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

// daemonGetterStub satisfies the target-store surface preflight needs so the
// gated branch can be exercised without a full store.
type daemonGetterStub struct{ backend string }

func (s daemonGetterStub) Daemon() store.DaemonProfileStore {
	return daemonProfileStoreStub(s)
}

func (s daemonGetterStub) Agents() store.AgentStore { return nil }

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

	for _, runner := range []string{"", "   ", localbackend.LocalTaskRunnerEntrypoint} {
		if !runnerNeedsLocalPreflight(runner) {
			t.Fatalf("runner %q must be gated for preflight", runner)
		}
		err := runtimepreflight.RequireLocalTaskRunner(context.Background(), daemonGetterStub{}, runtimepreflight.Request{WorkspaceKey: "TEST"})
		if err == nil {
			t.Fatalf("runner %q: missing backend must fail closed", runner)
		}
		var notReady *runtimepreflight.NotReadyError
		if !errors.As(err, &notReady) || notReady.Result.ErrorClass != runtimepreflight.ErrorClassUnavailable {
			t.Fatalf("runner %q: error = %T %v, want typed local_backend_unavailable", runner, err, err)
		}
	}
}

func TestStepOneGateParityEpic(t *testing.T) {
	fixture := preflighttest.LoadGateParityFixture(t)
	st := memstore.New()
	if _, err := st.Daemon().Upsert(context.Background(), &domain.DaemonProfile{
		WorkspaceKey: fixture.Workspace,
		AgentBackend: fixture.Backend,
	}); err != nil {
		t.Fatalf("upsert daemon profile: %v", err)
	}
	restore := runtimepreflight.SetHealthCheckerForTest(func(string) (runtimepreflight.HealthStatus, bool) {
		return fixture.Health, true
	})
	t.Cleanup(restore)
	err := preflightEpicRun(context.Background(), st, fixture.Workspace, localbackend.LocalTaskRunnerEntrypoint)
	preflighttest.AssertGateParityError(t, err, fixture)
}

// TestExplicitNonLocalRunnerSkipsPreflight confirms the gate does not fire for a
// non-local runner, so its own runtime owns readiness.
func TestExplicitNonLocalRunnerSkipsPreflight(t *testing.T) {
	if runnerNeedsLocalPreflight("daytona") {
		t.Fatal("daytona must not trigger local preflight")
	}
}

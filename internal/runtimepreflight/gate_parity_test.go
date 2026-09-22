package runtimepreflight_test

import (
	"context"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/runtimepreflight"
	"github.com/tysonthomas9/loomcli/internal/runtimepreflight/preflighttest"
)

func TestStepOneGateParityCanonicalCheck(t *testing.T) {
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
	result, err := runtimepreflight.CheckLocalTaskRunner(context.Background(), st, runtimepreflight.Request{WorkspaceKey: fixture.Workspace})
	if err != nil {
		t.Fatalf("CheckLocalTaskRunner() error = %v", err)
	}
	preflighttest.AssertGateParityResult(t, result, fixture)
}

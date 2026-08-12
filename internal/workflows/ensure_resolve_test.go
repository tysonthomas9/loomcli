// EnsureAndResolveDriver tests live in the external workflows_test package so
// the test can seed a memstore without joining the workflows package build
// (mirrors internal/trigger's cron tests, which avoid the memstore import
// lattice the same way).
package workflows_test

import (
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
	workflowdefs "github.com/tysonthomas9/loomcli/internal/workflows"
)

// EnsureAndResolveDriver is the shared workflow-name→driver resolve path for
// every HTTP surface that accepts a workflow name (workflow runs, trigger
// bindings). Non-builtin names must resolve directly with no self-heal side
// effects, and unknown names must surface ErrNotFound untouched. The builtin
// heal path itself needs the flue build toolchain, so it is covered by the
// runtime e2e rather than a unit test here.
func TestEnsureAndResolveDriverNonBuiltins(t *testing.T) {
	st := memstore.New()
	ctx := t.Context()

	if _, err := workflowdefs.EnsureAndResolveDriver(ctx, st, "WS", "no-such-workflow"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown workflow err = %v, want ErrNotFound", err)
	}
	drivers, err := st.Drivers().List(ctx, "WS", store.DriverFilter{})
	if err != nil {
		t.Fatalf("List drivers: %v", err)
	}
	if len(drivers) != 0 {
		t.Fatalf("failed resolve registered %d drivers, want 0 (no heal side effects)", len(drivers))
	}

	if _, err := st.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey: "WS", DriverID: "custom-flow", Name: "custom-flow",
		OwnerType: domain.DriverOwnerSystem, Status: domain.DriverStatusActive,
	}); err != nil {
		t.Fatalf("Create driver: %v", err)
	}
	drv, err := workflowdefs.EnsureAndResolveDriver(ctx, st, "WS", "custom-flow")
	if err != nil {
		t.Fatalf("EnsureAndResolveDriver(custom-flow) = %v", err)
	}
	if drv.DriverID != "custom-flow" {
		t.Fatalf("resolved driver = %q, want custom-flow", drv.DriverID)
	}
}

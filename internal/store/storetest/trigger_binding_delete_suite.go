package storetest

import (
	"errors"
	"fmt"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// TriggerBindingDeleteHarness supplies an isolated workspace and store for one
// TriggerBinding delete conformance case.
type TriggerBindingDeleteHarness struct {
	Workspace string
	Store     store.Store
}

// RunTriggerBindingDeleteConformance pins the binding/service referential
// behavior shared by memstore and fleet-db.
func RunTriggerBindingDeleteConformance(t *testing.T, newHarness func(testing.TB) *TriggerBindingDeleteHarness) {
	t.Helper()
	t.Run("delete existing", func(t *testing.T) {
		h := newHarness(t)
		seedTriggerBindingDeleteFixture(t, h)
		if err := h.Store.TriggerBindings().Delete(t.Context(), h.Workspace, "binding-one"); err != nil {
			t.Fatalf("delete existing binding: %v", err)
		}
		if _, err := h.Store.TriggerBindings().Get(t.Context(), h.Workspace, "binding-one"); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("get deleted binding err = %v, want ErrNotFound", err)
		}
	})

	t.Run("delete missing", func(t *testing.T) {
		h := newHarness(t)
		seedTriggerBindingDeleteFixture(t, h)
		if err := h.Store.TriggerBindings().Delete(t.Context(), h.Workspace, "missing"); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("delete missing binding err = %v, want ErrNotFound", err)
		}
	})

	t.Run("delete leaves other bindings intact", func(t *testing.T) {
		h := newHarness(t)
		seedTriggerBindingDeleteFixture(t, h)
		if err := h.Store.TriggerBindings().Delete(t.Context(), h.Workspace, "binding-one"); err != nil {
			t.Fatalf("delete first binding: %v", err)
		}
		if got, err := h.Store.TriggerBindings().Get(t.Context(), h.Workspace, "binding-two"); err != nil || got.BindingID != "binding-two" {
			t.Fatalf("other binding = %#v, err %v", got, err)
		}
	})

	t.Run("service delete remains blocked until bindings are removed", func(t *testing.T) {
		h := newHarness(t)
		seedTriggerBindingDeleteFixture(t, h)
		if err := h.Store.AgentServices().Delete(t.Context(), h.Workspace, "service-one"); !errors.Is(err, domain.ErrInvalidTransition) {
			t.Fatalf("delete bound service err = %v, want ErrInvalidTransition", err)
		}
		for _, id := range []string{"binding-one", "binding-two"} {
			if err := h.Store.TriggerBindings().Delete(t.Context(), h.Workspace, id); err != nil {
				t.Fatalf("delete binding %s: %v", id, err)
			}
		}
		if err := h.Store.AgentServices().Delete(t.Context(), h.Workspace, "service-one"); err != nil {
			t.Fatalf("delete unbound service: %v", err)
		}
	})
}

func seedTriggerBindingDeleteFixture(t testing.TB, h *TriggerBindingDeleteHarness) {
	t.Helper()
	ctx := t.Context()
	if h == nil || h.Store == nil || h.Workspace == "" {
		t.Fatal("trigger binding delete harness requires store and workspace")
	}
	if _, err := h.Store.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey: h.Workspace, DriverID: "driver-one", Name: "driver-one", Status: domain.DriverStatusActive,
	}); err != nil {
		t.Fatalf("create driver: %v", err)
	}
	if _, err := h.Store.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey: h.Workspace, VersionID: "version-one", DriverID: "driver-one", Version: 1,
		SourceDigest: "sha256:source", BundleDigest: "sha256:bundle", ValidationStatus: domain.DriverVersionValidationPassed,
	}); err != nil {
		t.Fatalf("create driver version: %v", err)
	}
	if _, err := h.Store.AgentServices().Create(ctx, store.AgentServiceCreate{
		WorkspaceKey: h.Workspace, ServiceID: "service-one", Name: "service-one",
		TriggerKind: domain.AgentServiceTriggerKindCron, DesiredState: domain.AgentServiceDesiredRunning,
		DriverID: "driver-one", DriverVersionID: "version-one",
	}); err != nil {
		t.Fatalf("create agent service: %v", err)
	}
	for i, id := range []string{"binding-one", "binding-two"} {
		if _, err := h.Store.TriggerBindings().Create(ctx, store.TriggerBindingCreate{
			WorkspaceKey: h.Workspace, BindingID: id, Name: id, SourceKind: "cron",
			RouteKey: fmt.Sprintf("cron.service-one.%d", i+1), DriverID: "driver-one", DriverVersionID: "version-one",
			TargetAgentServiceID: "service-one", TargetEntrypoint: "run", Schedule: "@daily", Enabled: true,
		}); err != nil {
			t.Fatalf("create trigger binding %s: %v", id, err)
		}
	}
}

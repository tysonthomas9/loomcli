package storetest

import (
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// DriverRunAttributionHarness supplies an isolated workspace and store for one
// driver-run attribution conformance case.
type DriverRunAttributionHarness struct {
	Workspace string
	Store     store.Store
}

// RunDriverRunAttributionConformance pins agent-service attribution as an
// admission-time snapshot, not a foreign key from DriverRun to AgentService.
func RunDriverRunAttributionConformance(t *testing.T, newHarness func(testing.TB) *DriverRunAttributionHarness) {
	t.Helper()
	t.Run("AgentServiceIDSurvivesServiceDelete", func(t *testing.T) {
		h := seedDriverRunAttributionFixture(t, newHarness, true)
		created, err := h.Store.DriverRuns().Create(t.Context(), store.DriverRunCreate{
			WorkspaceKey: h.Workspace, RunID: "run-attributed", DriverID: "driver-one",
			DriverVersionID: "version-one", AgentServiceID: "service-one",
		})
		if err != nil {
			t.Fatalf("create attributed driver run: %v", err)
		}
		if created.AgentServiceID != "service-one" {
			t.Fatalf("created agent_service_id = %q, want service-one", created.AgentServiceID)
		}
		if err := h.Store.AgentServices().Delete(t.Context(), h.Workspace, "service-one"); err != nil {
			t.Fatalf("delete attributed agent service: %v", err)
		}
		persisted, err := h.Store.DriverRuns().Get(t.Context(), h.Workspace, created.RunID)
		if err != nil {
			t.Fatalf("get driver run after service delete: %v", err)
		}
		if persisted.AgentServiceID != "service-one" {
			t.Fatalf("persisted agent_service_id = %q, want service-one", persisted.AgentServiceID)
		}
	})

	t.Run("UnknownAgentServiceIDIsRejected", func(t *testing.T) {
		h := seedDriverRunAttributionFixture(t, newHarness, false)
		_, err := h.Store.DriverRuns().Create(t.Context(), store.DriverRunCreate{
			WorkspaceKey: h.Workspace, RunID: "run-unknown-service", DriverID: "driver-one",
			DriverVersionID: "version-one", AgentServiceID: "missing-service",
		})
		if !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("create with unknown agent_service_id err = %v, want ErrNotFound", err)
		}
	})
}

func seedDriverRunAttributionFixture(t testing.TB, newHarness func(testing.TB) *DriverRunAttributionHarness, withService bool) *DriverRunAttributionHarness {
	t.Helper()
	h := newHarness(t)
	if h == nil || h.Store == nil || h.Workspace == "" {
		t.Fatal("driver run attribution harness requires store and workspace")
	}
	ctx := t.Context()
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
	if withService {
		if _, err := h.Store.AgentServices().Create(ctx, store.AgentServiceCreate{
			WorkspaceKey: h.Workspace, ServiceID: "service-one", Name: "service-one",
			TriggerKind: domain.AgentServiceTriggerKindCron, DesiredState: domain.AgentServiceDesiredRunning,
			DriverID: "driver-one", DriverVersionID: "version-one",
		}); err != nil {
			t.Fatalf("create agent service: %v", err)
		}
	}
	return h
}

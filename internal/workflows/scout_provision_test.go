package workflows

import (
	"context"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestEnsureScoutAgentFreshWorkspace(t *testing.T) {
	st := newScoutProvisionStore(t)
	svc, err := EnsureScoutAgent(context.Background(), st, "SCOUT")
	if err != nil {
		t.Fatalf("EnsureScoutAgent: %v", err)
	}
	assertScoutService(t, svc)
	binding, err := st.TriggerBindings().Get(t.Context(), "SCOUT", scoutTriggerBindingID)
	if err != nil {
		t.Fatalf("get scout binding: %v", err)
	}
	assertScoutBinding(t, binding, svc)
}

func TestEnsureScoutAgentRepeatDoesNotDuplicate(t *testing.T) {
	st := newScoutProvisionStore(t)
	first, err := EnsureScoutAgent(context.Background(), st, "SCOUT")
	if err != nil {
		t.Fatalf("first ensure: %v", err)
	}
	second, err := EnsureScoutAgent(context.Background(), st, "SCOUT")
	if err != nil {
		t.Fatalf("second ensure: %v", err)
	}
	if first.ServiceID != second.ServiceID || first.UpdatedAt != second.UpdatedAt {
		t.Fatalf("repeat ensure changed stable record: first=%#v second=%#v", first, second)
	}
	services, err := st.AgentServices().List(t.Context(), "SCOUT", store.AgentServiceFilter{IncludeDeleted: true})
	if err != nil {
		t.Fatalf("list services: %v", err)
	}
	bindings, err := st.TriggerBindings().List(t.Context(), "SCOUT", store.TriggerBindingFilter{RouteKey: scoutRouteKey})
	if err != nil {
		t.Fatalf("list bindings: %v", err)
	}
	if len(services) != 1 || len(bindings) != 1 {
		t.Fatalf("services=%d bindings=%d, want one of each", len(services), len(bindings))
	}
}

func TestEnsureScoutAgentRepairsMissingBinding(t *testing.T) {
	st := newScoutProvisionStore(t)
	driverRecord := ensureScoutBuiltinForTest(t, st)
	svc, err := st.AgentServices().Create(t.Context(), store.AgentServiceCreate{
		WorkspaceKey: "SCOUT", ServiceID: scoutAgentServiceID, Name: "Scout",
		Kind: domain.AgentServiceKindCron, DesiredState: domain.AgentServiceDesiredRunning,
		DriverID: driverRecord.DriverID, DriverVersionID: driverRecord.ActiveVersionID, CreatedBy: "system",
	})
	if err != nil {
		t.Fatalf("seed scout service: %v", err)
	}

	ensured, err := EnsureScoutAgent(context.Background(), st, "SCOUT")
	if err != nil {
		t.Fatalf("EnsureScoutAgent: %v", err)
	}
	binding, err := st.TriggerBindings().Get(t.Context(), "SCOUT", scoutTriggerBindingID)
	if err != nil {
		t.Fatalf("get repaired binding: %v", err)
	}
	assertScoutBinding(t, binding, svc)
	if ensured.ServiceID != svc.ServiceID {
		t.Fatalf("ensured service = %#v, want existing service", ensured)
	}
}

func TestEnsureScoutAgentRepairsMissingRecord(t *testing.T) {
	st := newScoutProvisionStore(t)
	driverRecord := ensureScoutBuiltinForTest(t, st)
	if _, err := st.TriggerBindings().Create(t.Context(), store.TriggerBindingCreate{
		WorkspaceKey: "SCOUT", BindingID: scoutTriggerBindingID, Name: "stale scout",
		SourceKind: "cron", RouteKey: scoutRouteKey,
		DriverID: driverRecord.DriverID, DriverVersionID: driverRecord.ActiveVersionID,
		TargetEntrypoint: "run", ConcurrencyPolicy: domain.TriggerBindingConcurrencyAllow,
		Schedule: "@daily", Enabled: false,
	}); err != nil {
		t.Fatalf("seed unattached binding: %v", err)
	}

	svc, err := EnsureScoutAgent(context.Background(), st, "SCOUT")
	if err != nil {
		t.Fatalf("EnsureScoutAgent: %v", err)
	}
	assertScoutService(t, svc)
	binding, err := st.TriggerBindings().Get(t.Context(), "SCOUT", scoutTriggerBindingID)
	if err != nil {
		t.Fatalf("get repaired binding: %v", err)
	}
	assertScoutBinding(t, binding, svc)
}

func newScoutProvisionStore(t *testing.T) *memstore.Store {
	t.Helper()
	st := memstore.New()
	if _, err := st.Workspaces().Create(t.Context(), store.WorkspaceCreate{Key: "SCOUT", Name: "Scout Workspace"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	installFakeWorkflowBuildDeps(t)
	t.Chdir(t.TempDir())
	return st
}

func ensureScoutBuiltinForTest(t *testing.T, st store.Store) *domain.Driver {
	t.Helper()
	if err := EnsureBuiltinWorkflow(t.Context(), st, "SCOUT", BuiltinScoutWorkflowName); err != nil {
		t.Fatalf("ensure scout builtin: %v", err)
	}
	driverRecord, err := st.Drivers().Get(t.Context(), "SCOUT", BuiltinScoutWorkflowName)
	if err != nil {
		t.Fatalf("get scout driver: %v", err)
	}
	return driverRecord
}

func assertScoutService(t *testing.T, svc *domain.AgentService) {
	t.Helper()
	if svc == nil || svc.ServiceID != "scout" || svc.Name != "Scout" || svc.Kind != domain.AgentServiceKindCron ||
		svc.DesiredState != domain.AgentServiceDesiredRunning || svc.RoleName != "" || svc.DriverID != BuiltinScoutWorkflowName ||
		svc.DriverVersionID == "" || svc.CreatedBy != "system" || svc.DeletedAt != nil {
		t.Fatalf("scout service = %#v", svc)
	}
}

func assertScoutBinding(t *testing.T, binding *domain.TriggerBinding, svc *domain.AgentService) {
	t.Helper()
	if binding == nil || binding.BindingID != "binding-cron-scout-weekly" || binding.RouteKey != "cron.scout.weekly" ||
		binding.SourceKind != "cron" || binding.Schedule != "@weekly" || binding.ScheduleTimezone != "" ||
		binding.ConcurrencyPolicy != domain.TriggerBindingConcurrencyForbid || !binding.Enabled ||
		binding.DriverID != svc.DriverID || binding.DriverVersionID != svc.DriverVersionID || binding.TargetAgentServiceID != svc.ServiceID {
		t.Fatalf("scout binding = %#v", binding)
	}
	if binding.ActorFilter == nil || len(binding.ActorFilter.ExcludeActorKinds) != 2 ||
		binding.ActorFilter.ExcludeActorKinds[0] != "driver-run" || binding.ActorFilter.ExcludeActorKinds[1] != "task-run" {
		t.Fatalf("scout actor filter = %#v", binding.ActorFilter)
	}
}

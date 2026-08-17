package agentprovision

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/agentstate"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/scriptedroles"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestEnsureAgentInstanceFromCatalog(t *testing.T) {
	for _, roleName := range []string{scriptedroles.ScoutRoleName, scriptedroles.EpicRunnerRoleName} {
		spec, ok := scriptedroles.ForRole(roleName)
		if !ok || spec.DefaultInstance == nil {
			continue
		}
		t.Run(roleName, func(t *testing.T) {
			st, workspaceDir := newProvisionStore(t)
			svc, err := EnsureAgentInstance(t.Context(), st, "SCOUT", workspaceDir, roleName)
			if err != nil {
				t.Fatalf("EnsureAgentInstance: %v", err)
			}
			assertCatalogService(t, svc, spec)
			role, err := st.Roles().Get(t.Context(), "SCOUT", roleName)
			if err != nil {
				t.Fatalf("get role: %v", err)
			}
			if role.Kind != domain.RoleKindWorker || role.Prompt != "" || role.PromptFile == "" {
				t.Fatalf("seeded role = %#v", role)
			}
			body, err := os.ReadFile(role.PromptFile)
			if err != nil || string(body) != spec.DefaultRole.Prompt {
				t.Fatalf("seeded prompt = %q err=%v", body, err)
			}
			binding, err := st.TriggerBindings().Get(t.Context(), "SCOUT", spec.DefaultInstance.Binding.BindingID)
			if err != nil {
				t.Fatalf("get binding: %v", err)
			}
			assertCatalogBinding(t, binding, svc, spec)

			second, err := EnsureAgentInstance(t.Context(), st, "SCOUT", workspaceDir, roleName)
			if err != nil {
				t.Fatalf("repeat ensure: %v", err)
			}
			if second.UpdatedAt != svc.UpdatedAt {
				t.Fatalf("repeat ensure changed stable service: first=%v second=%v", svc.UpdatedAt, second.UpdatedAt)
			}
		})
	}
}

func TestEnsureAgentInstanceMovesLegacyJournalOnceWithoutOverwrite(t *testing.T) {
	st, workspaceDir := newProvisionStore(t)
	spec, ok := scriptedroles.ForRole(scriptedroles.ScoutRoleName)
	if !ok || spec.DefaultInstance == nil {
		t.Fatal("scout catalog default is missing")
	}
	legacyPath := filepath.Join(workspaceDir, spec.JournalFilename)
	targetPath := agentstate.JournalPath(workspaceDir, spec.DefaultInstance.ServiceID, spec.JournalFilename)
	if err := os.WriteFile(legacyPath, []byte("legacy journal\n"), 0o600); err != nil {
		t.Fatalf("write legacy journal: %v", err)
	}

	if _, err := EnsureAgentInstance(t.Context(), st, "SCOUT", workspaceDir, scriptedroles.ScoutRoleName); err != nil {
		t.Fatalf("EnsureAgentInstance: %v", err)
	}
	if _, err := os.Stat(legacyPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy journal still exists after move: %v", err)
	}
	if got, err := os.ReadFile(targetPath); err != nil || string(got) != "legacy journal\n" {
		t.Fatalf("namespaced journal = %q, err %v", got, err)
	}

	if err := os.WriteFile(legacyPath, []byte("late legacy\n"), 0o600); err != nil {
		t.Fatalf("write late legacy journal: %v", err)
	}
	if _, err := EnsureAgentInstance(t.Context(), st, "SCOUT", workspaceDir, scriptedroles.ScoutRoleName); err != nil {
		t.Fatalf("repeat EnsureAgentInstance: %v", err)
	}
	if got, err := os.ReadFile(targetPath); err != nil || string(got) != "legacy journal\n" {
		t.Fatalf("repeat ensure overwrote namespaced journal = %q, err %v", got, err)
	}
	if got, err := os.ReadFile(legacyPath); err != nil || string(got) != "late legacy\n" {
		t.Fatalf("late legacy journal should remain untouched = %q, err %v", got, err)
	}
}

func TestEnsureAgentInstanceAdoptsRoleWithoutTouchingPromptAndSwapsDriverRef(t *testing.T) {
	st, workspaceDir := newProvisionStore(t)
	ctx := t.Context()
	customPrompt := "user-owned scout prompt"
	if _, err := st.Roles().Create(ctx, store.RoleCreate{
		WorkspaceKey: "SCOUT", Name: scriptedroles.ScoutRoleName,
		Kind: string(domain.RoleKindInteractive), Prompt: customPrompt,
	}); err != nil {
		t.Fatalf("seed role: %v", err)
	}
	seedWorkflow(t, st, "SCOUT", scriptedroles.ScoutWorkflowName)
	driver, err := st.Drivers().Get(ctx, "SCOUT", scriptedroles.ScoutWorkflowName)
	if err != nil {
		t.Fatalf("get driver: %v", err)
	}
	if _, err := st.AgentServices().Create(ctx, store.AgentServiceCreate{
		WorkspaceKey: "SCOUT", ServiceID: "scout", Name: "stale",
		TriggerKind: domain.AgentServiceTriggerKindEvent, DesiredState: domain.AgentServiceDesiredStopped,
		DriverID: driver.DriverID, DriverVersionID: driver.ActiveVersionID,
	}); err != nil {
		t.Fatalf("seed driver-ref service: %v", err)
	}

	svc, err := EnsureAgentInstance(ctx, st, "SCOUT", workspaceDir, scriptedroles.ScoutRoleName)
	if err != nil {
		t.Fatalf("EnsureAgentInstance: %v", err)
	}
	if svc.RoleName != scriptedroles.ScoutRoleName || svc.DriverID != "" || svc.DriverVersionID != "" {
		t.Fatalf("service XOR swap = %#v", svc)
	}
	role, err := st.Roles().Get(ctx, "SCOUT", scriptedroles.ScoutRoleName)
	if err != nil {
		t.Fatalf("get adopted role: %v", err)
	}
	if role.Kind != domain.RoleKindWorker || role.Prompt != customPrompt || role.PromptFile != "" {
		t.Fatalf("adopted role = %#v", role)
	}
}

func TestEnsureAgentInstanceRepairsRouteKeyCollisionThroughAlternateLookup(t *testing.T) {
	st, workspaceDir := newProvisionStore(t)
	ctx := t.Context()
	spec, _ := scriptedroles.ForRole(scriptedroles.ScoutRoleName)
	seedWorkflow(t, st, "SCOUT", spec.WorkflowName)
	driver, _ := st.Drivers().Get(ctx, "SCOUT", spec.WorkflowName)
	if _, err := st.TriggerBindings().Create(ctx, store.TriggerBindingCreate{
		WorkspaceKey: "SCOUT", BindingID: "alternate-id", Name: "stale",
		SourceKind: "cron", RouteKey: spec.DefaultInstance.Binding.RouteKey,
		DriverID: driver.DriverID, DriverVersionID: driver.ActiveVersionID,
		TargetEntrypoint: "stale", Schedule: "@daily", Enabled: false,
	}); err != nil {
		t.Fatalf("seed alternate binding: %v", err)
	}

	svc, err := EnsureAgentInstance(ctx, st, "SCOUT", workspaceDir, spec.RoleName)
	if err != nil {
		t.Fatalf("EnsureAgentInstance: %v", err)
	}
	binding, err := st.TriggerBindings().Get(ctx, "SCOUT", "alternate-id")
	if err != nil {
		t.Fatalf("get alternate binding: %v", err)
	}
	template := spec.DefaultInstance.Binding
	if binding.Name != template.Name || binding.TargetEntrypoint != template.TargetEntrypoint ||
		binding.TargetAgentServiceID != svc.ServiceID {
		t.Fatalf("structural repair missed: %#v", binding)
	}
	// Schedule and enabled are user-owned tuning: repair converges structure
	// but must not stomp them back to the catalog template.
	if binding.Schedule != "@daily" || binding.Enabled {
		t.Fatalf("user-owned binding fields clobbered: %#v", binding)
	}
}

func TestEnsureAgentInstancePreservesOperatorScheduleEdits(t *testing.T) {
	st, workspaceDir := newProvisionStore(t)
	ctx := t.Context()
	spec, _ := scriptedroles.ForRole(scriptedroles.ScoutRoleName)
	seedWorkflow(t, st, "SCOUT", spec.WorkflowName)
	if _, err := EnsureAgentInstance(ctx, st, "SCOUT", workspaceDir, spec.RoleName); err != nil {
		t.Fatalf("first ensure: %v", err)
	}

	schedule := "@hourly"
	enabled := false
	if _, err := st.TriggerBindings().Update(ctx, "SCOUT", spec.DefaultInstance.Binding.BindingID, store.TriggerBindingUpdate{
		Schedule: &schedule, Enabled: &enabled,
	}); err != nil {
		t.Fatalf("operator schedule edit: %v", err)
	}

	if _, err := EnsureAgentInstance(ctx, st, "SCOUT", workspaceDir, spec.RoleName); err != nil {
		t.Fatalf("repeat ensure: %v", err)
	}
	binding, err := st.TriggerBindings().Get(ctx, "SCOUT", spec.DefaultInstance.Binding.BindingID)
	if err != nil {
		t.Fatalf("get binding: %v", err)
	}
	if binding.Schedule != "@hourly" || binding.Enabled {
		t.Fatalf("ensure clobbered operator edit: schedule=%q enabled=%v", binding.Schedule, binding.Enabled)
	}
}

func TestEnsureAgentInstanceRejectsArchivedTombstone(t *testing.T) {
	st, workspaceDir := newProvisionStore(t)
	ctx := t.Context()
	if _, err := st.Roles().Create(ctx, store.RoleCreate{
		WorkspaceKey: "SCOUT", Name: scriptedroles.ScoutRoleName, Kind: string(domain.RoleKindWorker),
	}); err != nil {
		t.Fatalf("seed role: %v", err)
	}
	if _, err := st.AgentServices().Create(ctx, store.AgentServiceCreate{
		WorkspaceKey: "SCOUT", ServiceID: "scout", Name: "Scout",
		TriggerKind: domain.AgentServiceTriggerKindCron, DesiredState: domain.AgentServiceDesiredRunning,
		RoleName: scriptedroles.ScoutRoleName,
	}); err != nil {
		t.Fatalf("seed service: %v", err)
	}
	if err := st.AgentServices().Delete(ctx, "SCOUT", "scout"); err != nil {
		t.Fatalf("archive service: %v", err)
	}
	_, err := EnsureAgentInstance(ctx, st, "SCOUT", workspaceDir, scriptedroles.ScoutRoleName)
	if !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("archived ensure err = %v, want ErrInvalidTransition", err)
	}
}

func TestValidateServiceID(t *testing.T) {
	for _, id := range []string{"scout", "scout-west-2", "a", strings.Repeat("a", 64)} {
		if err := ValidateServiceID(id); err != nil {
			t.Errorf("ValidateServiceID(%q) = %v, want nil", id, err)
		}
	}
	for _, id := range []string{"", "Scout", "-scout", "scout_2", "scout/2", strings.Repeat("a", 65)} {
		if err := ValidateServiceID(id); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("ValidateServiceID(%q) = %v, want ErrInvalid", id, err)
		}
	}
}

func TestCreateAgentInstanceCreatesCatalogShapedCronBinding(t *testing.T) {
	st, workspaceDir := newProvisionStore(t)
	svc, binding, err := CreateAgentInstance(t.Context(), st, "SCOUT", workspaceDir, AgentInstanceCreate{
		ServiceID: "scout-west", Name: "Scout West", RoleName: scriptedroles.ScoutRoleName,
		Binding:   AgentInstanceBinding{Kind: "cron", Schedule: "0 9 * * 1-5", Timezone: "America/Los_Angeles", Enabled: true},
		CreatedBy: "test",
	})
	if err != nil {
		t.Fatalf("CreateAgentInstance: %v", err)
	}
	if svc.ServiceID != "scout-west" || svc.RoleName != scriptedroles.ScoutRoleName || svc.TriggerKind != domain.AgentServiceTriggerKindCron {
		t.Fatalf("service = %#v", svc)
	}
	if binding.BindingID != "binding-cron-scout-west" || binding.RouteKey != "cron.scout-west" ||
		binding.Schedule != "0 9 * * 1-5" || binding.ScheduleTimezone != "America/Los_Angeles" ||
		binding.TargetAgentServiceID != svc.ServiceID || !binding.Enabled {
		t.Fatalf("binding = %#v", binding)
	}
	role, err := st.Roles().Get(t.Context(), "SCOUT", scriptedroles.ScoutRoleName)
	if err != nil || role.Kind != domain.RoleKindWorker {
		t.Fatalf("role = %#v, err %v", role, err)
	}
}

func TestCreateAgentInstancePreservesDefaultScoutBindingIdentity(t *testing.T) {
	st, workspaceDir := newProvisionStore(t)
	_, binding, err := CreateAgentInstance(t.Context(), st, "SCOUT", workspaceDir, AgentInstanceCreate{
		ServiceID: "scout", RoleName: scriptedroles.ScoutRoleName,
		Binding: AgentInstanceBinding{Kind: "cron", Schedule: "@daily", Enabled: true},
	})
	if err != nil {
		t.Fatalf("CreateAgentInstance: %v", err)
	}
	if binding.BindingID != "binding-cron-scout-weekly" || binding.RouteKey != "cron.scout.weekly" {
		t.Fatalf("default binding identity = %q/%q", binding.BindingID, binding.RouteKey)
	}
}

func TestCreateAgentInstanceRejectsDisallowedBindingKind(t *testing.T) {
	st, workspaceDir := newProvisionStore(t)
	_, _, err := CreateAgentInstance(t.Context(), st, "SCOUT", workspaceDir, AgentInstanceCreate{
		ServiceID: "epic-cron", RoleName: scriptedroles.EpicRunnerRoleName,
		Binding: AgentInstanceBinding{Kind: "cron", Schedule: "@daily", Enabled: true},
	})
	if !errors.Is(err, domain.ErrInvalid) || !strings.Contains(err.Error(), "does not allow") {
		t.Fatalf("CreateAgentInstance err = %v, want clear ErrInvalid", err)
	}
}

func newProvisionStore(t *testing.T) (*memstore.Store, string) {
	t.Helper()
	st := memstore.New()
	if _, err := st.Workspaces().Create(t.Context(), store.WorkspaceCreate{Key: "SCOUT", Name: "Scout Workspace"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	oldEnsure := ensureBuiltinWorkflow
	oldResolve := resolveDriverID
	ensureBuiltinWorkflow = func(_ context.Context, st store.Store, ws, name string) error {
		seedWorkflow(t, st, ws, name)
		return nil
	}
	resolveDriverID = func(_ context.Context, _ store.Store, _, name string) (string, error) { return name, nil }
	t.Cleanup(func() {
		ensureBuiltinWorkflow = oldEnsure
		resolveDriverID = oldResolve
	})
	return st, t.TempDir()
}

func seedWorkflow(t *testing.T, st store.Store, ws, name string) {
	t.Helper()
	if _, err := st.Drivers().Get(t.Context(), ws, name); err == nil {
		return
	} else if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("get workflow driver: %v", err)
	}
	if _, err := st.Drivers().Create(t.Context(), store.DriverCreate{
		WorkspaceKey: ws, DriverID: name, Name: name,
		OwnerType: domain.DriverOwnerSystem, Status: domain.DriverStatusActive,
	}); err != nil {
		t.Fatalf("create workflow driver: %v", err)
	}
	versionID := name + "-v1"
	if _, err := st.DriverVersions().Create(t.Context(), store.DriverVersionCreate{
		WorkspaceKey: ws, VersionID: versionID, DriverID: name, Version: 1,
		SourceDigest: "sha256:source", BundleDigest: "sha256:bundle",
		ValidationStatus: domain.DriverVersionValidationPassed,
	}); err != nil {
		t.Fatalf("create workflow version: %v", err)
	}
	if _, err := st.Drivers().Update(t.Context(), ws, name, store.DriverUpdate{ActiveVersionID: &versionID}); err != nil {
		t.Fatalf("activate workflow version: %v", err)
	}
}

func assertCatalogService(t *testing.T, svc *domain.AgentService, spec scriptedroles.ScriptedRole) {
	t.Helper()
	template := spec.DefaultInstance
	if svc == nil || svc.ServiceID != template.ServiceID || svc.Name != template.Name || svc.TriggerKind != template.TriggerKind ||
		svc.DesiredState != template.DesiredState || svc.RoleName != spec.RoleName || svc.DriverID != "" || svc.DriverVersionID != "" ||
		svc.CreatedBy != template.CreatedBy || svc.DeletedAt != nil {
		t.Fatalf("service = %#v", svc)
	}
}

func assertCatalogBinding(t *testing.T, binding *domain.TriggerBinding, svc *domain.AgentService, spec scriptedroles.ScriptedRole) {
	t.Helper()
	template := spec.DefaultInstance.Binding
	if binding == nil || binding.RouteKey != template.RouteKey || binding.SourceKind != template.SourceKind ||
		binding.Schedule != template.Schedule || binding.ScheduleTimezone != template.ScheduleTimezone ||
		binding.ConcurrencyPolicy != template.ConcurrencyPolicy || binding.Enabled != template.Enabled ||
		binding.DriverID != spec.WorkflowName || binding.DriverVersionID == "" || binding.TargetAgentServiceID != svc.ServiceID ||
		binding.TargetEntrypoint != template.TargetEntrypoint {
		t.Fatalf("binding = %#v", binding)
	}
	if binding.ActorFilter == nil || len(binding.ActorFilter.ExcludeActorKinds) != len(template.ExcludedActors) {
		t.Fatalf("actor filter = %#v", binding.ActorFilter)
	}
}

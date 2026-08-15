package agentprovision

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/roleprompts"
	"github.com/tysonthomas9/loomcli/internal/scriptedroles"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/workflows"
)

var (
	ensureBuiltinWorkflow = workflows.EnsureBuiltinWorkflow
	resolveDriverID       = workflows.ResolveDriverID
)

// EnsureAgentInstance materializes the catalog role's default standing
// instance. The workspace directory is required only when the create-only role
// seed carries a prompt that must be published as a PromptFile.
func EnsureAgentInstance(ctx context.Context, st store.Store, workspaceKey, workspaceDir, roleName string) (*domain.AgentService, error) {
	workspaceKey = strings.TrimSpace(workspaceKey)
	workspaceDir = strings.TrimSpace(workspaceDir)
	roleName = strings.TrimSpace(roleName)
	if st == nil || workspaceKey == "" || roleName == "" {
		return nil, fmt.Errorf("store, workspace key, and role name are required: %w", domain.ErrInvalid)
	}
	spec, ok := scriptedroles.ForRole(roleName)
	if !ok {
		return nil, fmt.Errorf("role %q is not scripted: %w", roleName, domain.ErrInvalid)
	}
	if spec.DefaultInstance == nil {
		return nil, fmt.Errorf("scripted role %q has no default instance: %w", roleName, domain.ErrInvalid)
	}
	if err := ensureRole(ctx, st, workspaceKey, workspaceDir, spec); err != nil {
		return nil, fmt.Errorf("ensure %s role: %w", roleName, err)
	}
	if err := ensureBuiltinWorkflow(ctx, st, workspaceKey, spec.WorkflowName); err != nil {
		return nil, fmt.Errorf("ensure %s workflow: %w", roleName, err)
	}
	driverID, versionID, err := activeWorkflowVersion(ctx, st, workspaceKey, spec.WorkflowName)
	if err != nil {
		return nil, err
	}
	svc, err := ensureService(ctx, st, workspaceKey, spec)
	if err != nil {
		return nil, fmt.Errorf("ensure %s agent service: %w", roleName, err)
	}
	if _, err := ensureBinding(ctx, st, workspaceKey, spec, svc, driverID, versionID); err != nil {
		return nil, fmt.Errorf("ensure %s trigger binding: %w", roleName, err)
	}
	return svc, nil
}

func ensureRole(ctx context.Context, st store.Store, workspaceKey, workspaceDir string, spec scriptedroles.ScriptedRole) error {
	promptFile := ""
	if spec.DefaultRole.Prompt != "" {
		if workspaceDir == "" {
			return fmt.Errorf("workspace path unavailable; cannot publish role prompt: %w", domain.ErrInvalid)
		}
		var err error
		promptFile, err = roleprompts.Publish(workspaceDir, spec.RoleName, spec.DefaultRole.Prompt)
		if err != nil {
			return fmt.Errorf("publish role prompt: %w", err)
		}
	}
	create := store.RoleCreate{
		WorkspaceKey: workspaceKey,
		Name:         spec.RoleName,
		Kind:         string(spec.DefaultRole.Kind),
		Description:  spec.DefaultRole.Description,
		PromptFile:   promptFile,
	}
	_, err := Ensure(ctx, create, Reconciler[domain.Role, store.RoleCreate, store.RoleUpdate]{
		Get: func(ctx context.Context) (*domain.Role, error) {
			return st.Roles().Get(ctx, workspaceKey, spec.RoleName)
		},
		Create: st.Roles().Create,
		Diff: func(role *domain.Role) (store.RoleUpdate, bool) {
			if role.Kind == domain.RoleKindWorker {
				return store.RoleUpdate{}, false
			}
			kind := string(domain.RoleKindWorker)
			return store.RoleUpdate{ExpectedUpdatedAt: &role.UpdatedAt, Kind: &kind}, true
		},
		Patch: func(ctx context.Context, _ *domain.Role, patch store.RoleUpdate) (*domain.Role, error) {
			updated, err := st.Roles().Update(ctx, workspaceKey, spec.RoleName, patch)
			if !errors.Is(err, domain.ErrConflict) {
				return updated, err
			}
			latest, getErr := st.Roles().Get(ctx, workspaceKey, spec.RoleName)
			if getErr != nil {
				return nil, getErr
			}
			if latest.Kind == domain.RoleKindWorker {
				return latest, nil
			}
			kind := string(domain.RoleKindWorker)
			return st.Roles().Update(ctx, workspaceKey, spec.RoleName, store.RoleUpdate{ExpectedUpdatedAt: &latest.UpdatedAt, Kind: &kind})
		},
	})
	return err
}

func activeWorkflowVersion(ctx context.Context, st store.Store, workspaceKey, workflowName string) (string, string, error) {
	driverID, err := resolveDriverID(ctx, st, workspaceKey, workflowName)
	if err != nil {
		return "", "", fmt.Errorf("resolve %s driver: %w", workflowName, err)
	}
	driverRecord, err := st.Drivers().Get(ctx, workspaceKey, driverID)
	if err != nil {
		return "", "", fmt.Errorf("get %s driver: %w", workflowName, err)
	}
	versionID := strings.TrimSpace(driverRecord.ActiveVersionID)
	if versionID == "" {
		return "", "", fmt.Errorf("%s driver has no active version: %w", workflowName, domain.ErrInvalid)
	}
	if _, err := st.DriverVersions().Get(ctx, workspaceKey, versionID); err != nil {
		return "", "", fmt.Errorf("get %s active driver version: %w", workflowName, err)
	}
	return driverRecord.DriverID, versionID, nil
}

func ensureService(ctx context.Context, st store.Store, workspaceKey string, spec scriptedroles.ScriptedRole) (*domain.AgentService, error) {
	template := spec.DefaultInstance
	create := store.AgentServiceCreate{
		WorkspaceKey: workspaceKey,
		ServiceID:    template.ServiceID,
		Name:         template.Name,
		TriggerKind:  template.TriggerKind,
		DesiredState: template.DesiredState,
		RoleName:     spec.RoleName,
		CreatedBy:    template.CreatedBy,
	}
	return Ensure(ctx, create, Reconciler[domain.AgentService, store.AgentServiceCreate, store.AgentServiceUpdate]{
		Get: func(ctx context.Context) (*domain.AgentService, error) {
			return st.AgentServices().Get(ctx, workspaceKey, template.ServiceID)
		},
		Create: st.AgentServices().Create,
		Archived: func(svc *domain.AgentService) bool {
			return svc != nil && svc.DeletedAt != nil
		},
		Diff: func(svc *domain.AgentService) (store.AgentServiceUpdate, bool) {
			patch := store.AgentServiceUpdate{}
			if svc.Name != template.Name {
				patch.Name = ptr(template.Name)
			}
			if svc.TriggerKind != template.TriggerKind {
				patch.TriggerKind = &template.TriggerKind
			}
			if svc.DesiredState != template.DesiredState {
				patch.DesiredState = &template.DesiredState
			}
			if svc.RoleName != spec.RoleName {
				patch.RoleName = ptr(spec.RoleName)
			}
			if svc.DriverID != "" {
				patch.DriverID = ptr("")
			}
			if svc.DriverVersionID != "" {
				patch.DriverVersionID = ptr("")
			}
			return patch, agentServicePatchChanged(patch)
		},
		Patch: func(ctx context.Context, _ *domain.AgentService, patch store.AgentServiceUpdate) (*domain.AgentService, error) {
			return st.AgentServices().Update(ctx, workspaceKey, template.ServiceID, patch)
		},
	})
}

func ensureBinding(ctx context.Context, st store.Store, workspaceKey string, spec scriptedroles.ScriptedRole, svc *domain.AgentService, driverID, versionID string) (*domain.TriggerBinding, error) {
	template := spec.DefaultInstance.Binding
	create := store.TriggerBindingCreate{
		WorkspaceKey:         workspaceKey,
		BindingID:            template.BindingID,
		Name:                 template.Name,
		SourceKind:           template.SourceKind,
		RouteKey:             template.RouteKey,
		DriverID:             driverID,
		DriverVersionID:      versionID,
		TargetEntrypoint:     template.TargetEntrypoint,
		TargetAgentServiceID: svc.ServiceID,
		ConcurrencyPolicy:    template.ConcurrencyPolicy,
		ActorFilter:          &domain.TriggerActorFilter{ExcludeActorKinds: append([]string(nil), template.ExcludedActors...)},
		Schedule:             template.Schedule,
		ScheduleTimezone:     template.ScheduleTimezone,
		Enabled:              template.Enabled,
	}
	return Ensure(ctx, create, Reconciler[domain.TriggerBinding, store.TriggerBindingCreate, store.TriggerBindingUpdate]{
		Get: func(ctx context.Context) (*domain.TriggerBinding, error) {
			binding, err := st.TriggerBindings().Get(ctx, workspaceKey, template.BindingID)
			if errors.Is(err, domain.ErrNotFound) {
				return st.TriggerBindings().GetByRouteKey(ctx, workspaceKey, template.RouteKey)
			}
			return binding, err
		},
		Create: st.TriggerBindings().Create,
		Diff: func(binding *domain.TriggerBinding) (store.TriggerBindingUpdate, bool) {
			patch := store.TriggerBindingUpdate{}
			setStringPatch(binding.Name, template.Name, &patch.Name)
			setStringPatch(binding.SourceKind, template.SourceKind, &patch.SourceKind)
			setStringPatch(binding.RouteKey, template.RouteKey, &patch.RouteKey)
			setStringPatch(binding.DriverID, driverID, &patch.DriverID)
			setStringPatch(binding.DriverVersionID, versionID, &patch.DriverVersionID)
			setStringPatch(binding.TargetEntrypoint, template.TargetEntrypoint, &patch.TargetEntrypoint)
			setStringPatch(binding.TargetAgentServiceID, svc.ServiceID, &patch.TargetAgentServiceID)
			setStringPatch(binding.Schedule, template.Schedule, &patch.Schedule)
			setStringPatch(binding.ScheduleTimezone, template.ScheduleTimezone, &patch.ScheduleTimezone)
			if binding.ConcurrencyPolicy != template.ConcurrencyPolicy {
				patch.ConcurrencyPolicy = &template.ConcurrencyPolicy
			}
			if binding.Enabled != template.Enabled {
				patch.Enabled = &template.Enabled
			}
			if binding.ActorFilter == nil || !slices.Equal(binding.ActorFilter.ExcludeActorKinds, template.ExcludedActors) || len(binding.ActorFilter.AllowActors) != 0 {
				patch.ActorFilter = &domain.TriggerActorFilter{ExcludeActorKinds: append([]string(nil), template.ExcludedActors...)}
			}
			return patch, triggerBindingPatchChanged(patch)
		},
		Patch: func(ctx context.Context, binding *domain.TriggerBinding, patch store.TriggerBindingUpdate) (*domain.TriggerBinding, error) {
			return st.TriggerBindings().Update(ctx, workspaceKey, binding.BindingID, patch)
		},
	})
}

func agentServicePatchChanged(patch store.AgentServiceUpdate) bool {
	return patch.Name != nil || patch.TriggerKind != nil || patch.DesiredState != nil || patch.RoleName != nil || patch.DriverID != nil || patch.DriverVersionID != nil
}

func triggerBindingPatchChanged(patch store.TriggerBindingUpdate) bool {
	return patch.Name != nil || patch.SourceKind != nil || patch.RouteKey != nil || patch.DriverID != nil || patch.DriverVersionID != nil || patch.TargetEntrypoint != nil || patch.TargetAgentServiceID != nil || patch.ConcurrencyPolicy != nil || patch.ActorFilter != nil || patch.Schedule != nil || patch.ScheduleTimezone != nil || patch.Enabled != nil
}

func setStringPatch(current, wanted string, target **string) {
	if current != wanted {
		*target = ptr(wanted)
	}
}

func ptr(value string) *string { return &value }

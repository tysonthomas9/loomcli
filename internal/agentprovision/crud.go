package agentprovision

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/scriptedroles"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/trigger"
)

var serviceIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// AgentInstanceBinding is the user-selected binding for a new instance.
// B4 exposes cron creation; Kind remains explicit so catalog policy is checked
// at this seam rather than inferred from a schedule string.
type AgentInstanceBinding struct {
	Kind     string
	Schedule string
	Timezone string
	Enabled  bool
}

// AgentInstanceCreate is the durable input shared by the WebUI and CLI.
type AgentInstanceCreate struct {
	ServiceID string
	Name      string
	RoleName  string
	Binding   AgentInstanceBinding
	CreatedBy string
}

// ValidateServiceID enforces the B4 path-segment grammar.
func ValidateServiceID(id string) error {
	if !serviceIDPattern.MatchString(id) {
		return fmt.Errorf("agent service id %q must match [a-z0-9][a-z0-9-]{0,63}: %w", id, domain.ErrInvalid)
	}
	return nil
}

// CreateAgentInstance creates one user-managed instance of a compiled
// scripted role and its catalog-shaped trigger binding.
func CreateAgentInstance(ctx context.Context, st store.Store, workspaceKey, workspaceDir string, in AgentInstanceCreate) (*domain.AgentService, *domain.TriggerBinding, error) {
	workspaceKey = strings.TrimSpace(workspaceKey)
	workspaceDir = strings.TrimSpace(workspaceDir)
	in.ServiceID = strings.TrimSpace(in.ServiceID)
	in.Name = strings.TrimSpace(in.Name)
	in.RoleName = strings.TrimSpace(in.RoleName)
	in.Binding.Kind = strings.TrimSpace(in.Binding.Kind)
	in.Binding.Schedule = strings.TrimSpace(in.Binding.Schedule)
	in.Binding.Timezone = strings.TrimSpace(in.Binding.Timezone)
	if st == nil || workspaceKey == "" {
		return nil, nil, fmt.Errorf("store and workspace key are required: %w", domain.ErrInvalid)
	}
	if err := ValidateServiceID(in.ServiceID); err != nil {
		return nil, nil, err
	}
	spec, ok := scriptedroles.ForRole(in.RoleName)
	if !ok {
		return nil, nil, fmt.Errorf("role %q is not a scripted role and cannot be instantiated: %w", in.RoleName, domain.ErrInvalid)
	}
	if !slices.Contains(spec.AllowedBindingKinds, in.Binding.Kind) {
		return nil, nil, fmt.Errorf("scripted role %q does not allow %q trigger bindings: %w", in.RoleName, in.Binding.Kind, domain.ErrInvalid)
	}
	if in.Binding.Kind != "cron" {
		return nil, nil, fmt.Errorf("trigger binding kind %q is not supported by instance CRUD: %w", in.Binding.Kind, domain.ErrInvalid)
	}
	if spec.DefaultInstance == nil || spec.DefaultInstance.Binding.SourceKind != in.Binding.Kind {
		return nil, nil, fmt.Errorf("scripted role %q has no %q binding shape: %w", in.RoleName, in.Binding.Kind, domain.ErrInvalid)
	}
	if in.Binding.Schedule == "" {
		return nil, nil, fmt.Errorf("binding schedule is required: %w", domain.ErrInvalid)
	}
	if _, err := trigger.NextFire(in.Binding.Schedule, in.Binding.Timezone, time.Now().UTC()); err != nil {
		return nil, nil, fmt.Errorf("invalid binding schedule: %w", errors.Join(domain.ErrInvalid, err))
	}
	if existing, err := findAgentServiceRecord(ctx, st, workspaceKey, in.ServiceID); err == nil {
		if existing.DeletedAt != nil {
			return nil, nil, fmt.Errorf("agent service %q is an archived tombstone and cannot be resurrected: %w", in.ServiceID, domain.ErrInvalidTransition)
		}
		return nil, nil, fmt.Errorf("agent service %q already exists: %w", in.ServiceID, domain.ErrAlreadyExists)
	} else if !errors.Is(err, domain.ErrNotFound) {
		return nil, nil, err
	}
	if err := ensureRole(ctx, st, workspaceKey, workspaceDir, spec); err != nil {
		return nil, nil, fmt.Errorf("ensure %s role: %w", in.RoleName, err)
	}
	if err := ensureBuiltinWorkflow(ctx, st, workspaceKey, spec.WorkflowName); err != nil {
		return nil, nil, fmt.Errorf("ensure %s workflow: %w", in.RoleName, err)
	}
	driverID, versionID, err := activeWorkflowVersion(ctx, st, workspaceKey, spec.WorkflowName)
	if err != nil {
		return nil, nil, err
	}
	if in.Name == "" {
		in.Name = in.ServiceID
	}
	svc, err := st.AgentServices().Create(ctx, store.AgentServiceCreate{
		WorkspaceKey: workspaceKey, ServiceID: in.ServiceID, Name: in.Name,
		TriggerKind: domain.AgentServiceTriggerKindCron, DesiredState: domain.AgentServiceDesiredRunning,
		RoleName: spec.RoleName, CreatedBy: in.CreatedBy,
	})
	if err != nil {
		return nil, nil, err
	}
	template := spec.DefaultInstance.Binding
	bindingID, routeKey := instanceBindingIdentity(spec, in.ServiceID)
	bindingName := in.Name + " cron"
	if in.ServiceID == spec.DefaultInstance.ServiceID {
		bindingName = template.Name
	}
	binding, err := st.TriggerBindings().Create(ctx, store.TriggerBindingCreate{
		WorkspaceKey: workspaceKey, BindingID: bindingID, Name: bindingName,
		SourceKind: in.Binding.Kind, RouteKey: routeKey, DriverID: driverID, DriverVersionID: versionID,
		TargetEntrypoint: template.TargetEntrypoint, TargetAgentServiceID: svc.ServiceID,
		ConcurrencyPolicy: template.ConcurrencyPolicy,
		ActorFilter:       &domain.TriggerActorFilter{ExcludeActorKinds: append([]string(nil), template.ExcludedActors...)},
		Schedule:          in.Binding.Schedule, ScheduleTimezone: in.Binding.Timezone, Enabled: in.Binding.Enabled,
	})
	if err != nil {
		return nil, nil, err
	}
	return svc, binding, nil
}

func findAgentServiceRecord(ctx context.Context, st store.Store, workspaceKey, serviceID string) (*domain.AgentService, error) {
	if svc, err := st.AgentServices().Get(ctx, workspaceKey, serviceID); err == nil {
		return svc, nil
	} else if !errors.Is(err, domain.ErrNotFound) {
		return nil, err
	}
	services, err := st.AgentServices().List(ctx, workspaceKey, store.AgentServiceFilter{IncludeDeleted: true})
	if err != nil {
		return nil, err
	}
	for _, svc := range services {
		if svc != nil && svc.ServiceID == serviceID {
			return svc, nil
		}
	}
	return nil, domain.ErrNotFound
}

func instanceBindingIdentity(spec scriptedroles.ScriptedRole, serviceID string) (string, string) {
	if spec.DefaultInstance != nil && serviceID == spec.DefaultInstance.ServiceID {
		return spec.DefaultInstance.Binding.BindingID, spec.DefaultInstance.Binding.RouteKey
	}
	return "binding-cron-" + serviceID, "cron." + serviceID
}

// SetAgentInstanceDesiredState enables or disables one live scripted-role
// instance without changing its trigger binding.
func SetAgentInstanceDesiredState(ctx context.Context, st store.Store, workspaceKey, serviceID string, state domain.AgentServiceDesiredState) (*domain.AgentService, error) {
	if st == nil || strings.TrimSpace(workspaceKey) == "" {
		return nil, fmt.Errorf("store and workspace key are required: %w", domain.ErrInvalid)
	}
	serviceID = strings.TrimSpace(serviceID)
	if err := ValidateServiceID(serviceID); err != nil {
		return nil, err
	}
	if state != domain.AgentServiceDesiredRunning && state != domain.AgentServiceDesiredStopped {
		return nil, fmt.Errorf("desired state must be running or stopped: %w", domain.ErrInvalid)
	}
	svc, err := st.AgentServices().Get(ctx, workspaceKey, serviceID)
	if err != nil {
		return nil, err
	}
	if svc.DeletedAt != nil {
		return nil, domain.ErrNotFound
	}
	if _, ok := scriptedroles.ForRole(svc.RoleName); !ok {
		return nil, fmt.Errorf("agent service %q does not reference a scripted role: %w", serviceID, domain.ErrInvalid)
	}
	if svc.DesiredState == state {
		return svc, nil
	}
	return st.AgentServices().Update(ctx, workspaceKey, serviceID, store.AgentServiceUpdate{DesiredState: &state})
}

// DeleteAgentInstance converges disable -> delete bindings -> delete service.
// Missing records at any already-completed step are successful retries.
func DeleteAgentInstance(ctx context.Context, st store.Store, workspaceKey, serviceID string) error {
	if st == nil || strings.TrimSpace(workspaceKey) == "" {
		return fmt.Errorf("store and workspace key are required: %w", domain.ErrInvalid)
	}
	serviceID = strings.TrimSpace(serviceID)
	if err := ValidateServiceID(serviceID); err != nil {
		return err
	}
	svc, err := st.AgentServices().Get(ctx, workspaceKey, serviceID)
	if errors.Is(err, domain.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if svc == nil || svc.DeletedAt != nil {
		return nil
	}
	if svc.DesiredState != domain.AgentServiceDesiredStopped {
		stopped := domain.AgentServiceDesiredStopped
		if _, err := st.AgentServices().Update(ctx, workspaceKey, serviceID, store.AgentServiceUpdate{DesiredState: &stopped}); err != nil && !errors.Is(err, domain.ErrNotFound) {
			return fmt.Errorf("disable agent service %q: %w", serviceID, err)
		}
	}
	bindings, err := st.TriggerBindings().List(ctx, workspaceKey, store.TriggerBindingFilter{TargetAgentServiceID: serviceID})
	if err != nil {
		return fmt.Errorf("list bindings for agent service %q: %w", serviceID, err)
	}
	for _, binding := range bindings {
		if binding == nil {
			continue
		}
		if err := st.TriggerBindings().Delete(ctx, workspaceKey, binding.BindingID); err != nil && !errors.Is(err, domain.ErrNotFound) {
			return fmt.Errorf("delete trigger binding %q: %w", binding.BindingID, err)
		}
	}
	if err := st.AgentServices().Delete(ctx, workspaceKey, serviceID); err != nil && !errors.Is(err, domain.ErrNotFound) {
		return fmt.Errorf("delete agent service %q: %w", serviceID, err)
	}
	return nil
}

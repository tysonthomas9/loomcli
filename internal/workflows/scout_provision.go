package workflows

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

const (
	scoutAgentServiceID   = "scout"
	scoutAgentServiceName = "Scout"
	scoutTriggerBindingID = "binding-cron-scout-weekly"
	scoutTriggerName      = "Scout weekly"
	scoutRouteKey         = "cron.scout.weekly"
	scoutSchedule         = "@weekly"
	scoutSourceKind       = "cron"
	scoutEntrypoint       = "run"
)

var scoutExcludedActorKinds = []string{"driver-run", "task-run"}

// EnsureScoutAgent materializes the durable identity and weekly trigger for
// the built-in scout workflow. It is safe to call repeatedly and repairs
// either half when a prior provisioning attempt stopped after one write.
func EnsureScoutAgent(ctx context.Context, st store.Store, workspaceKey string) (*domain.AgentService, error) {
	if st == nil || strings.TrimSpace(workspaceKey) == "" {
		return nil, fmt.Errorf("store and workspace key are required: %w", domain.ErrInvalid)
	}
	if err := EnsureBuiltinWorkflow(ctx, st, workspaceKey, BuiltinScoutWorkflowName); err != nil {
		return nil, fmt.Errorf("ensure scout workflow: %w", err)
	}
	driverID, err := ResolveDriverID(ctx, st, workspaceKey, BuiltinScoutWorkflowName)
	if err != nil {
		return nil, fmt.Errorf("resolve scout driver: %w", err)
	}
	driverRecord, err := st.Drivers().Get(ctx, workspaceKey, driverID)
	if err != nil {
		return nil, fmt.Errorf("get scout driver: %w", err)
	}
	if strings.TrimSpace(driverRecord.ActiveVersionID) == "" {
		return nil, fmt.Errorf("scout driver has no active version: %w", domain.ErrInvalid)
	}
	if _, err := st.DriverVersions().Get(ctx, workspaceKey, driverRecord.ActiveVersionID); err != nil {
		return nil, fmt.Errorf("get scout active driver version: %w", err)
	}

	svc, err := ensureScoutAgentService(ctx, st, workspaceKey, driverRecord.DriverID, driverRecord.ActiveVersionID)
	if err != nil {
		return nil, err
	}
	if _, err := ensureScoutTriggerBinding(ctx, st, workspaceKey, svc); err != nil {
		return nil, err
	}
	return svc, nil
}

func ensureScoutAgentService(ctx context.Context, st store.Store, workspaceKey, driverID, versionID string) (*domain.AgentService, error) {
	svc, err := st.AgentServices().Get(ctx, workspaceKey, scoutAgentServiceID)
	if errors.Is(err, domain.ErrNotFound) {
		svc, err = st.AgentServices().Create(ctx, store.AgentServiceCreate{
			WorkspaceKey: workspaceKey, ServiceID: scoutAgentServiceID, Name: scoutAgentServiceName,
			Kind: agentServiceKindForSource(scoutSourceKind), DesiredState: domain.AgentServiceDesiredRunning,
			DriverID: driverID, DriverVersionID: versionID, CreatedBy: "system",
		})
		if errors.Is(err, domain.ErrAlreadyExists) {
			svc, err = st.AgentServices().Get(ctx, workspaceKey, scoutAgentServiceID)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("ensure scout agent service: %w", err)
	}
	if svc.DeletedAt != nil {
		return nil, fmt.Errorf("scout agent service is archived: %w", domain.ErrInvalidTransition)
	}

	kind := agentServiceKindForSource(scoutSourceKind)
	desired := domain.AgentServiceDesiredRunning
	emptyRole := ""
	patch := store.AgentServiceUpdate{}
	needsUpdate := false
	if svc.Name != scoutAgentServiceName {
		patch.Name = ptrScout(scoutAgentServiceName)
		needsUpdate = true
	}
	if svc.Kind != kind {
		patch.Kind = &kind
		needsUpdate = true
	}
	if svc.DesiredState != desired {
		patch.DesiredState = &desired
		needsUpdate = true
	}
	if svc.RoleName != "" {
		patch.RoleName = &emptyRole
		needsUpdate = true
	}
	if svc.DriverID != driverID {
		patch.DriverID = &driverID
		needsUpdate = true
	}
	if svc.DriverVersionID != versionID {
		patch.DriverVersionID = &versionID
		needsUpdate = true
	}
	if !needsUpdate {
		return svc, nil
	}
	updated, err := st.AgentServices().Update(ctx, workspaceKey, scoutAgentServiceID, patch)
	if err != nil {
		return nil, fmt.Errorf("repair scout agent service: %w", err)
	}
	return updated, nil
}

func ensureScoutTriggerBinding(ctx context.Context, st store.Store, workspaceKey string, svc *domain.AgentService) (*domain.TriggerBinding, error) {
	binding, err := st.TriggerBindings().Get(ctx, workspaceKey, scoutTriggerBindingID)
	if errors.Is(err, domain.ErrNotFound) {
		binding, err = st.TriggerBindings().GetByRouteKey(ctx, workspaceKey, scoutRouteKey)
	}
	if errors.Is(err, domain.ErrNotFound) {
		binding, err = st.TriggerBindings().Create(ctx, store.TriggerBindingCreate{
			WorkspaceKey: workspaceKey, BindingID: scoutTriggerBindingID, Name: scoutTriggerName,
			SourceKind: scoutSourceKind, RouteKey: scoutRouteKey,
			DriverID: svc.DriverID, DriverVersionID: svc.DriverVersionID,
			TargetEntrypoint: scoutEntrypoint, TargetAgentServiceID: svc.ServiceID,
			ConcurrencyPolicy: domain.TriggerBindingConcurrencyForbid,
			ActorFilter:       &domain.TriggerActorFilter{ExcludeActorKinds: append([]string(nil), scoutExcludedActorKinds...)},
			Schedule:          scoutSchedule, ScheduleTimezone: "", Enabled: true,
		})
		if errors.Is(err, domain.ErrAlreadyExists) {
			binding, err = st.TriggerBindings().GetByRouteKey(ctx, workspaceKey, scoutRouteKey)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("ensure scout trigger binding: %w", err)
	}

	policy := domain.TriggerBindingConcurrencyForbid
	emptyTimezone := ""
	enabled := true
	filter := &domain.TriggerActorFilter{ExcludeActorKinds: append([]string(nil), scoutExcludedActorKinds...)}
	patch := store.TriggerBindingUpdate{}
	needsUpdate := false
	setString := func(current, want string, target **string) {
		if current != want {
			*target = ptrScout(want)
			needsUpdate = true
		}
	}
	setString(binding.Name, scoutTriggerName, &patch.Name)
	setString(binding.SourceKind, scoutSourceKind, &patch.SourceKind)
	setString(binding.RouteKey, scoutRouteKey, &patch.RouteKey)
	setString(binding.DriverID, svc.DriverID, &patch.DriverID)
	setString(binding.DriverVersionID, svc.DriverVersionID, &patch.DriverVersionID)
	setString(binding.TargetEntrypoint, scoutEntrypoint, &patch.TargetEntrypoint)
	setString(binding.TargetAgentServiceID, svc.ServiceID, &patch.TargetAgentServiceID)
	setString(binding.Schedule, scoutSchedule, &patch.Schedule)
	if binding.ScheduleTimezone != emptyTimezone {
		patch.ScheduleTimezone = &emptyTimezone
		needsUpdate = true
	}
	if binding.ConcurrencyPolicy != policy {
		patch.ConcurrencyPolicy = &policy
		needsUpdate = true
	}
	if binding.Enabled != enabled {
		patch.Enabled = &enabled
		needsUpdate = true
	}
	if binding.ActorFilter == nil || !slices.Equal(binding.ActorFilter.ExcludeActorKinds, scoutExcludedActorKinds) || len(binding.ActorFilter.AllowActors) != 0 {
		patch.ActorFilter = filter
		needsUpdate = true
	}
	if !needsUpdate {
		return binding, nil
	}
	updated, err := st.TriggerBindings().Update(ctx, workspaceKey, binding.BindingID, patch)
	if err != nil {
		return nil, fmt.Errorf("repair scout trigger binding: %w", err)
	}
	return updated, nil
}

func agentServiceKindForSource(sourceKind string) domain.AgentServiceKind {
	if strings.TrimSpace(sourceKind) == scoutSourceKind {
		return domain.AgentServiceKindCron
	}
	return domain.AgentServiceKindEvent
}

func ptrScout(value string) *string { return &value }

package agents

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
	workflowdefs "github.com/tysonthomas9/loomcli/internal/infra/workflowdistribution/authoring"
	agentsmodule "github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	connectorsmodule "github.com/tysonthomas9/loomcli/internal/modules/connectors"
	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
	workflowcataloghttp "github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog/httpapi"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/agentcoord"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

type testBindingOperations struct{ store store.Store }

func (a *testBindingOperations) CreateBinding(ctx context.Context, _ authority.OperatorAuthority, command automation.CreateBindingCommand) (*automation.Binding, error) {
	if strings.TrimSpace(command.Definition.TargetAgentServiceID) != "" {
		return nil, automation.ErrManagedBinding
	}
	return a.create(ctx, command.WorkspaceKey, command.Definition)
}

func (a *testBindingOperations) CreateManagedBinding(ctx context.Context, _ authority.OperatorAuthority, command automation.CreateManagedBindingCommand) (*automation.Binding, error) {
	if strings.TrimSpace(command.AgentServiceID) == "" || command.Definition.TargetAgentServiceID != command.AgentServiceID {
		return nil, automation.ErrManagedBinding
	}
	return a.create(ctx, command.WorkspaceKey, command.Definition)
}

func (a *testBindingOperations) EnsureManagedBinding(
	ctx context.Context,
	_ authority.SystemAuthority,
	command automation.EnsureManagedBindingCommand,
) (*automation.Binding, error) {
	if strings.TrimSpace(command.AgentServiceID) == "" ||
		command.Definition.TargetAgentServiceID != command.AgentServiceID {
		return nil, automation.ErrManagedBinding
	}
	existing, err := a.GetBinding(
		ctx,
		command.WorkspaceKey,
		command.Definition.BindingID,
	)
	if err == nil {
		if existing.TargetAgentServiceID != command.AgentServiceID ||
			!testBindingMatchesDefinition(existing, command.Definition) {
			return nil, automation.ErrConflict
		}
		return existing, nil
	}
	if !errors.Is(err, automation.ErrNotFound) {
		return nil, err
	}
	created, err := a.create(ctx, command.WorkspaceKey, command.Definition)
	if err == nil {
		if created.TargetAgentServiceID != command.AgentServiceID ||
			!testBindingMatchesDefinition(created, command.Definition) {
			return nil, automation.ErrConflict
		}
		return created, nil
	}
	if !errors.Is(err, automation.ErrConflict) {
		return nil, err
	}
	existing, getErr := a.GetBinding(
		ctx,
		command.WorkspaceKey,
		command.Definition.BindingID,
	)
	if getErr != nil {
		return nil, errors.Join(err, getErr)
	}
	if existing.TargetAgentServiceID != command.AgentServiceID ||
		!testBindingMatchesDefinition(existing, command.Definition) {
		return nil, automation.ErrConflict
	}
	return existing, nil
}

func testBindingMatchesDefinition(
	existing *automation.Binding,
	expected automation.BindingDefinition,
) bool {
	if existing == nil || existing.WebhookSecret != "" {
		return false
	}
	if expected.RouteKey == "" {
		switch expected.SourceKind {
		case automation.SourceKindCron:
			expected.RouteKey = "cron:" + expected.BindingID
		case automation.SourceKindInternal:
			expected.RouteKey = "internal:" + expected.BindingID
		}
	}
	if expected.Name == "" {
		expected.Name = expected.RouteKey
		if expected.Name == "" {
			expected.Name = expected.BindingID
		}
	}
	if expected.ConcurrencyPolicy == "" {
		expected.ConcurrencyPolicy = automation.ConcurrencyOneActivePerEpic
	}
	if expected.IdempotencyPolicy == "" {
		expected.IdempotencyPolicy = "header:Idempotency-Key"
	}
	if expected.AuthPolicy == "" {
		expected.AuthPolicy = "workspace_user"
	}
	if expected.RetryMaxAttempts == 0 {
		expected.RetryMaxAttempts = automation.DefaultRetryMaxAttempts
	}
	if expected.RetryBackoffSeconds == 0 {
		expected.RetryBackoffSeconds = automation.DefaultRetryBackoffSeconds
	}
	return existing.BindingID == expected.BindingID &&
		existing.Name == expected.Name &&
		existing.SourceKind == expected.SourceKind &&
		existing.SourceRef == expected.SourceRef &&
		existing.SourceConfigRef == expected.SourceConfigRef &&
		existing.RouteKey == expected.RouteKey &&
		existing.Method == expected.Method &&
		existing.PathTemplate == expected.PathTemplate &&
		existing.Topic == expected.Topic &&
		slices.Equal(existing.EventTypePatterns, expected.EventTypePatterns) &&
		existing.FilterRef == expected.FilterRef &&
		existing.DriverID == expected.DriverID &&
		existing.DriverVersionID == expected.DriverVersionID &&
		existing.TargetEntrypoint == expected.TargetEntrypoint &&
		existing.TargetAgentServiceID == expected.TargetAgentServiceID &&
		existing.ConcurrencyPolicy == expected.ConcurrencyPolicy &&
		existing.IdempotencyPolicy == expected.IdempotencyPolicy &&
		existing.AuthPolicy == expected.AuthPolicy &&
		existing.SubjectKeyTemplate == expected.SubjectKeyTemplate &&
		testActorFiltersEqual(existing.ActorFilter, expected.ActorFilter) &&
		existing.RetryMaxAttempts == expected.RetryMaxAttempts &&
		existing.RetryBackoffSeconds == expected.RetryBackoffSeconds &&
		existing.Schedule == expected.Schedule &&
		existing.ScheduleTimezone == expected.ScheduleTimezone &&
		slices.Equal(existing.Permissions, expected.Permissions) &&
		existing.Enabled == expected.Enabled
}

func testActorFiltersEqual(left, right *automation.ActorFilter) bool {
	if left == nil || left.IsZero() {
		return right == nil || right.IsZero()
	}
	if right == nil || right.IsZero() {
		return false
	}
	return slices.Equal(left.ExcludeActorKinds, right.ExcludeActorKinds) &&
		slices.Equal(left.AllowActors, right.AllowActors)
}

func (a *testBindingOperations) create(ctx context.Context, workspace string, definition automation.BindingDefinition) (*automation.Binding, error) {
	binding, err := a.store.TriggerBindings().Create(ctx, store.TriggerBindingCreate{
		WorkspaceKey: workspace, BindingID: definition.BindingID, Name: definition.Name,
		SourceKind: definition.SourceKind, SourceRef: definition.SourceRef, SourceConfigRef: definition.SourceConfigRef,
		RouteKey: definition.RouteKey, Method: definition.Method, PathTemplate: definition.PathTemplate,
		Topic: definition.Topic, EventTypePatterns: definition.EventTypePatterns, FilterRef: definition.FilterRef,
		DriverID: definition.DriverID, DriverVersionID: definition.DriverVersionID,
		TargetEntrypoint: definition.TargetEntrypoint, TargetAgentServiceID: definition.TargetAgentServiceID,
		ConcurrencyPolicy: definition.ConcurrencyPolicy, IdempotencyPolicy: definition.IdempotencyPolicy,
		AuthPolicy: definition.AuthPolicy, SubjectKeyTemplate: definition.SubjectKeyTemplate,
		ActorFilter: definition.ActorFilter, RetryMaxAttempts: definition.RetryMaxAttempts,
		RetryBackoffSeconds: definition.RetryBackoffSeconds, Schedule: definition.Schedule,
		ScheduleTimezone: definition.ScheduleTimezone, Permissions: definition.Permissions, Enabled: definition.Enabled,
	})
	return binding, mapTestBindingError(err)
}

func (a *testBindingOperations) UpdateBinding(ctx context.Context, _ authority.OperatorAuthority, command automation.UpdateBindingCommand) (*automation.Binding, error) {
	existing, err := a.store.TriggerBindings().Get(ctx, command.WorkspaceKey, command.BindingID)
	if err != nil {
		return nil, mapTestBindingError(err)
	}
	if strings.TrimSpace(existing.TargetAgentServiceID) != "" {
		return nil, automation.ErrManagedBinding
	}
	return a.update(ctx, command.WorkspaceKey, command.BindingID, command.Patch)
}

func (a *testBindingOperations) UpdateManagedBinding(ctx context.Context, _ authority.OperatorAuthority, command automation.UpdateManagedBindingCommand) (*automation.Binding, error) {
	existing, err := a.store.TriggerBindings().Get(ctx, command.WorkspaceKey, command.BindingID)
	if err != nil {
		return nil, mapTestBindingError(err)
	}
	if strings.TrimSpace(command.AgentServiceID) == "" || existing.TargetAgentServiceID != command.AgentServiceID {
		return nil, automation.ErrManagedBinding
	}
	return a.update(ctx, command.WorkspaceKey, command.BindingID, command.Patch)
}

func (a *testBindingOperations) update(ctx context.Context, workspace, bindingID string, patch automation.BindingPatch) (*automation.Binding, error) {
	updated, err := a.store.TriggerBindings().Update(ctx, workspace, bindingID, store.TriggerBindingUpdate{
		Name: patch.Name, SourceKind: patch.SourceKind, SourceRef: patch.SourceRef,
		SourceConfigRef: patch.SourceConfigRef, RouteKey: patch.RouteKey, Method: patch.Method,
		PathTemplate: patch.PathTemplate, Topic: patch.Topic, EventTypePatterns: patch.EventTypePatterns,
		FilterRef: patch.FilterRef, DriverID: patch.DriverID, DriverVersionID: patch.DriverVersionID,
		TargetEntrypoint: patch.TargetEntrypoint, TargetAgentServiceID: patch.TargetAgentServiceID,
		ConcurrencyPolicy: patch.ConcurrencyPolicy, IdempotencyPolicy: patch.IdempotencyPolicy,
		AuthPolicy: patch.AuthPolicy, SubjectKeyTemplate: patch.SubjectKeyTemplate, ActorFilter: patch.ActorFilter,
		RetryMaxAttempts: patch.RetryMaxAttempts, RetryBackoffSeconds: patch.RetryBackoffSeconds,
		Schedule: patch.Schedule, ScheduleTimezone: patch.ScheduleTimezone, Permissions: patch.Permissions,
	})
	return updated, mapTestBindingError(err)
}

func (a *testBindingOperations) EnableBinding(ctx context.Context, _ authority.OperatorAuthority, command automation.BindingCommand) (*automation.Binding, error) {
	return a.setEnabled(ctx, command.WorkspaceKey, command.BindingID, "", true)
}

func (a *testBindingOperations) DisableBinding(ctx context.Context, _ authority.OperatorAuthority, command automation.BindingCommand) (*automation.Binding, error) {
	return a.setEnabled(ctx, command.WorkspaceKey, command.BindingID, "", false)
}

func (a *testBindingOperations) EnableManagedBinding(ctx context.Context, _ authority.OperatorAuthority, command automation.ManagedBindingCommand) (*automation.Binding, error) {
	return a.setEnabled(ctx, command.WorkspaceKey, command.BindingID, command.AgentServiceID, true)
}

func (a *testBindingOperations) DisableManagedBinding(ctx context.Context, _ authority.OperatorAuthority, command automation.ManagedBindingCommand) (*automation.Binding, error) {
	return a.setEnabled(ctx, command.WorkspaceKey, command.BindingID, command.AgentServiceID, false)
}

func (a *testBindingOperations) setEnabled(ctx context.Context, workspace, bindingID, agentServiceID string, enabled bool) (*automation.Binding, error) {
	existing, err := a.store.TriggerBindings().Get(ctx, workspace, bindingID)
	if err != nil {
		return nil, mapTestBindingError(err)
	}
	if agentServiceID == "" && existing.TargetAgentServiceID != "" || agentServiceID != "" && existing.TargetAgentServiceID != agentServiceID {
		return nil, automation.ErrManagedBinding
	}
	updated, err := a.store.TriggerBindings().Update(ctx, workspace, bindingID, store.TriggerBindingUpdate{Enabled: &enabled})
	return updated, mapTestBindingError(err)
}

func (a *testBindingOperations) DeleteBinding(ctx context.Context, _ authority.OperatorAuthority, command automation.BindingCommand) error {
	return a.delete(ctx, command.WorkspaceKey, command.BindingID, "")
}

func (a *testBindingOperations) DeleteManagedBinding(ctx context.Context, _ authority.OperatorAuthority, command automation.ManagedBindingCommand) error {
	return a.delete(ctx, command.WorkspaceKey, command.BindingID, command.AgentServiceID)
}

func (a *testBindingOperations) delete(ctx context.Context, workspace, bindingID, agentServiceID string) error {
	existing, err := a.store.TriggerBindings().Get(ctx, workspace, bindingID)
	if err != nil {
		return mapTestBindingError(err)
	}
	if agentServiceID == "" && existing.TargetAgentServiceID != "" || agentServiceID != "" && existing.TargetAgentServiceID != agentServiceID {
		return automation.ErrManagedBinding
	}
	if existing.Enabled {
		return automation.ErrBindingEnabled
	}
	return mapTestBindingError(a.store.TriggerBindings().Delete(ctx, workspace, bindingID))
}

func (a *testBindingOperations) GetBinding(ctx context.Context, workspace, bindingID string) (*automation.Binding, error) {
	binding, err := a.store.TriggerBindings().Get(ctx, workspace, bindingID)
	return binding, mapTestBindingError(err)
}

func (a *testBindingOperations) ListBindings(ctx context.Context, workspace string, filter automation.BindingFilter) ([]*automation.Binding, error) {
	bindings, err := a.store.TriggerBindings().List(ctx, workspace, store.TriggerBindingFilter{
		SourceKind: filter.SourceKind, RouteKey: filter.RouteKey, DriverID: filter.DriverID,
		TargetAgentServiceID: filter.TargetAgentServiceID, Enabled: filter.Enabled, Limit: filter.Limit,
	})
	return bindings, mapTestBindingError(err)
}

func (a *testBindingOperations) DispatchBinding(context.Context, authority.OperatorAuthority, automation.DispatchBindingCommand) (*automation.DispatchBindingResult, error) {
	return nil, automation.ErrUnavailable
}

func mapTestBindingError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return errors.Join(automation.ErrNotFound, err)
	case errors.Is(err, domain.ErrInvalid):
		return errors.Join(automation.ErrInvalid, err)
	case errors.Is(err, domain.ErrAlreadyExists), errors.Is(err, domain.ErrConflict):
		return errors.Join(automation.ErrConflict, err)
	default:
		return err
	}
}

type testOperatorAuthorityResolver struct{}

func (testOperatorAuthorityResolver) ResolveOperatorAuthority(r *http.Request, _ string, _ authority.Action) (authority.OperatorAuthority, error) {
	if r == nil || strings.TrimSpace(r.Header.Get("Authorization")) == "" {
		return authority.OperatorAuthority{}, workflowcataloghttp.ErrUnauthenticated
	}
	return authority.OperatorAuthority{}, nil
}

type testAgentRecordAuthorityResolver struct{}

func (testAgentRecordAuthorityResolver) ResolveOperatorAuthority(
	_ *http.Request,
	_ string,
	_ authority.Action,
) (authority.OperatorAuthority, error) {
	return authority.OperatorAuthority{}, nil
}

// testAgentRecordAPI gives handler tests the canonical Agents contract while
// retaining memstore as an inspectable persistence fixture. Production
// handlers never receive AgentServiceStore directly for durable record writes.
type testAgentRecordAPI struct {
	store store.Store
}

func (api *testAgentRecordAPI) GetAgent(
	ctx context.Context,
	workspace,
	agentID string,
) (*agentsmodule.Agent, error) {
	record, err := api.store.AgentServices().Get(ctx, workspace, agentID)
	return canonicalAgentRecordForTest(record), mapTestAgentRecordError(err)
}

func (api *testAgentRecordAPI) ListAgents(
	ctx context.Context,
	workspace string,
	filter agentsmodule.AgentFilter,
) ([]*agentsmodule.Agent, error) {
	records, err := api.store.AgentServices().List(ctx, workspace, store.AgentServiceFilter{
		Kind:           domain.AgentServiceKind(filter.Kind),
		DesiredState:   domain.AgentServiceDesiredState(filter.DesiredState),
		RoleName:       filter.RoleName,
		IncludeDeleted: filter.IncludeDeleted,
		Limit:          filter.Limit,
	})
	if err != nil {
		return nil, mapTestAgentRecordError(err)
	}
	out := make([]*agentsmodule.Agent, 0, len(records))
	for _, record := range records {
		out = append(out, canonicalAgentRecordForTest(record))
	}
	return out, nil
}

func (api *testAgentRecordAPI) GetRole(
	ctx context.Context,
	workspace,
	roleName string,
) (*agentsmodule.Role, error) {
	role, err := api.store.Roles().Get(ctx, workspace, roleName)
	return canonicalRoleForTest(role), mapTestAgentRecordError(err)
}

func (api *testAgentRecordAPI) ListRoles(
	ctx context.Context,
	workspace string,
) ([]*agentsmodule.Role, error) {
	roles, err := api.store.Roles().List(ctx, workspace)
	if err != nil {
		return nil, mapTestAgentRecordError(err)
	}
	out := make([]*agentsmodule.Role, 0, len(roles))
	for _, role := range roles {
		out = append(out, canonicalRoleForTest(role))
	}
	return out, nil
}

func canonicalRoleForTest(role *domain.Role) *agentsmodule.Role {
	if role == nil {
		return nil
	}
	return &agentsmodule.Role{
		WorkspaceKey: role.WorkspaceKey, Name: role.Name, Kind: string(role.Kind),
		Description: role.Description, Prompt: role.Prompt, PromptFile: role.PromptFile,
		Model: role.Model, TaskFilter: role.TaskFilter, Backend: role.Backend, Effort: role.Effort,
		PathPatterns: append([]string(nil), role.PathPatterns...), Skills: append([]string(nil), role.Skills...),
		MaxPriority: role.MaxPriority, MaxConcurrency: role.MaxConcurrency, ReadOnly: role.ReadOnly,
		AllowedTools: append([]string(nil), role.AllowedTools...), DeniedTools: append([]string(nil), role.DeniedTools...),
		MaxBudgetUSD: role.MaxBudgetUSD, CreatedAt: role.CreatedAt, UpdatedAt: role.UpdatedAt,
	}
}

func (api *testAgentRecordAPI) UpdateAgent(
	ctx context.Context,
	_ authority.OperatorAuthority,
	command agentsmodule.UpdateAgentCommand,
) (*agentsmodule.Agent, error) {
	existing, err := api.store.AgentServices().Get(ctx, command.WorkspaceKey, command.AgentID)
	if err != nil {
		return nil, mapTestAgentRecordError(err)
	}
	if !existing.UpdatedAt.Equal(command.ExpectedUpdatedAt) {
		return nil, agentsmodule.ErrConflict
	}
	patch := store.AgentServiceUpdate{
		Name:            command.Patch.Name,
		PlacementPolicy: command.Patch.PlacementPolicy,
		MaxInstances:    command.Patch.MaxInstances,
		RestartPolicy:   command.Patch.RestartPolicy,
		BudgetPolicy:    command.Patch.BudgetPolicy,
		Metadata:        command.Patch.Metadata,
	}
	if command.Patch.Kind != nil {
		value := domain.AgentServiceKind(*command.Patch.Kind)
		patch.Kind = &value
	}
	if command.Patch.Behavior != nil {
		roleName := command.Patch.Behavior.RoleName
		driverID := command.Patch.Behavior.DriverID
		driverVersionID := command.Patch.Behavior.DriverVersionID
		patch.RoleName = &roleName
		patch.DriverID = &driverID
		patch.DriverVersionID = &driverVersionID
	}
	updated, err := api.store.AgentServices().Update(ctx, command.WorkspaceKey, command.AgentID, patch)
	return canonicalAgentRecordForTest(updated), mapTestAgentRecordError(err)
}

func (api *testAgentRecordAPI) ArchiveAgent(
	ctx context.Context,
	_ authority.OperatorAuthority,
	command agentsmodule.ArchiveAgentCommand,
) (*agentsmodule.Agent, error) {
	existing, err := api.store.AgentServices().Get(ctx, command.WorkspaceKey, command.AgentID)
	if err != nil {
		return nil, mapTestAgentRecordError(err)
	}
	if !existing.UpdatedAt.Equal(command.ExpectedUpdatedAt) {
		return nil, agentsmodule.ErrConflict
	}
	if err := api.store.AgentServices().Delete(ctx, command.WorkspaceKey, command.AgentID); err != nil {
		return nil, mapTestAgentRecordError(err)
	}
	archived, err := api.store.AgentServices().Get(ctx, command.WorkspaceKey, command.AgentID)
	return canonicalAgentRecordForTest(archived), mapTestAgentRecordError(err)
}

func (api *testAgentRecordAPI) SetDesiredState(
	ctx context.Context,
	_ authority.OperatorAuthority,
	command agentsmodule.SetDesiredStateCommand,
) (*agentsmodule.Agent, error) {
	existing, err := api.store.AgentServices().Get(ctx, command.WorkspaceKey, command.AgentID)
	if err != nil {
		return nil, mapTestAgentRecordError(err)
	}
	if !existing.UpdatedAt.Equal(command.ExpectedUpdatedAt) ||
		agentsmodule.DesiredState(existing.DesiredState) != command.ExpectedState {
		return nil, agentsmodule.ErrConflict
	}
	desired := domain.AgentServiceDesiredState(command.DesiredState)
	updated, err := api.store.AgentServices().Update(
		ctx,
		command.WorkspaceKey,
		command.AgentID,
		store.AgentServiceUpdate{DesiredState: &desired},
	)
	return canonicalAgentRecordForTest(updated), mapTestAgentRecordError(err)
}

func (api *testAgentRecordAPI) ApplyLifecycle(
	ctx context.Context,
	_ authority.OperatorAuthority,
	command agentsmodule.ApplyLifecycleCommand,
) (*agentsmodule.LifecycleResult, error) {
	existing, err := api.store.AgentServices().Get(ctx, command.WorkspaceKey, command.AgentID)
	if err != nil {
		return nil, mapTestAgentRecordError(err)
	}
	if !existing.UpdatedAt.Equal(command.ExpectedUpdatedAt) {
		return nil, agentsmodule.ErrConflict
	}
	bindings, err := api.store.TriggerBindings().List(ctx, command.WorkspaceKey, store.TriggerBindingFilter{
		TargetAgentServiceID: command.AgentID,
	})
	if err != nil {
		return nil, mapTestAgentRecordError(err)
	}
	bindingIDs := make([]string, 0, len(bindings))
	grantIDs := make([]string, 0)
	for _, binding := range bindings {
		bindingIDs = append(bindingIDs, binding.BindingID)
		switch command.Action {
		case agentsmodule.LifecycleEnable, agentsmodule.LifecycleDisable:
			enabled := command.Action == agentsmodule.LifecycleEnable
			if _, err := api.store.TriggerBindings().Update(
				ctx,
				command.WorkspaceKey,
				binding.BindingID,
				store.TriggerBindingUpdate{Enabled: &enabled},
			); err != nil {
				return nil, mapTestAgentRecordError(err)
			}
		case agentsmodule.LifecycleDelete:
			grants, err := api.store.Connectors().ListGrantRecordsByBinding(
				ctx,
				command.WorkspaceKey,
				binding.BindingID,
			)
			if err != nil {
				return nil, mapTestAgentRecordError(err)
			}
			for _, grant := range grants {
				if err := api.store.Connectors().RevokeGrantRecord(
					ctx,
					command.WorkspaceKey,
					grant.GrantID,
				); err != nil {
					return nil, mapTestAgentRecordError(err)
				}
				grantIDs = append(grantIDs, grant.GrantID)
			}
			if binding.Enabled {
				disabled := false
				if _, err := api.store.TriggerBindings().Update(
					ctx,
					command.WorkspaceKey,
					binding.BindingID,
					store.TriggerBindingUpdate{Enabled: &disabled},
				); err != nil {
					return nil, mapTestAgentRecordError(err)
				}
			}
			if err := api.store.TriggerBindings().Delete(
				ctx,
				command.WorkspaceKey,
				binding.BindingID,
			); err != nil {
				return nil, mapTestAgentRecordError(err)
			}
		}
	}
	desired := domain.AgentServiceDesiredPaused
	if command.Action == agentsmodule.LifecycleEnable {
		desired = domain.AgentServiceDesiredRunning
	} else if command.Action == agentsmodule.LifecycleDelete {
		desired = domain.AgentServiceDesiredStopped
	}
	updated, err := api.store.AgentServices().Update(
		ctx,
		command.WorkspaceKey,
		command.AgentID,
		store.AgentServiceUpdate{DesiredState: &desired},
	)
	if err != nil {
		return nil, mapTestAgentRecordError(err)
	}
	if command.Action == agentsmodule.LifecycleDelete {
		if err := api.store.AgentServices().Delete(ctx, command.WorkspaceKey, command.AgentID); err != nil {
			return nil, mapTestAgentRecordError(err)
		}
		updated, err = api.store.AgentServices().Get(ctx, command.WorkspaceKey, command.AgentID)
		if err != nil {
			return nil, mapTestAgentRecordError(err)
		}
	}
	sort.Strings(bindingIDs)
	sort.Strings(grantIDs)
	return &agentsmodule.LifecycleResult{
		WorkspaceKey: command.WorkspaceKey, AgentID: command.AgentID,
		IdempotencyKey: command.IdempotencyKey, Action: command.Action,
		Agent: canonicalAgentRecordForTest(updated), BindingIDs: bindingIDs,
		GrantIDs: grantIDs, CommittedAt: updated.UpdatedAt,
	}, nil
}

func canonicalAgentRecordForTest(record *domain.AgentService) *agentsmodule.Agent {
	if record == nil {
		return nil
	}
	return &agentsmodule.Agent{
		WorkspaceKey: record.WorkspaceKey,
		AgentID:      record.ServiceID,
		Name:         record.Name,
		Kind:         agentsmodule.AgentKind(record.Kind),
		Behavior: agentsmodule.BehaviorReference{
			RoleName:        record.RoleName,
			DriverID:        record.DriverID,
			DriverVersionID: record.DriverVersionID,
		},
		DesiredState:    agentsmodule.DesiredState(record.DesiredState),
		PlacementPolicy: record.PlacementPolicy,
		MaxInstances:    record.MaxInstances,
		RestartPolicy:   record.RestartPolicy,
		BudgetPolicy:    record.BudgetPolicy,
		Metadata:        cloneStringMap(record.Metadata),
		CreatedBy:       record.CreatedBy,
		DeletedAt:       cloneAgentRecordTime(record.DeletedAt),
		CreatedAt:       record.CreatedAt,
		UpdatedAt:       record.UpdatedAt,
	}
}

func mapTestAgentRecordError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return errors.Join(agentsmodule.ErrNotFound, err)
	case errors.Is(err, domain.ErrInvalid):
		return errors.Join(agentsmodule.ErrInvalid, err)
	case errors.Is(err, domain.ErrAlreadyExists):
		return errors.Join(agentsmodule.ErrAlreadyExists, err)
	case errors.Is(err, domain.ErrConflict):
		return errors.Join(agentsmodule.ErrConflict, err)
	case errors.Is(err, domain.ErrInvalidTransition):
		return errors.Join(agentsmodule.ErrInvalidTransition, err)
	default:
		return err
	}
}

type testBindingGrantCompatibility struct {
	grants connectorsmodule.ManagementStore
}

type testTriggerConnectorCompatibility struct {
	testBindingGrantCompatibility
}

func (testTriggerConnectorCompatibility) ConfigureBindingSecret(context.Context, string, string, string, string) error {
	return nil
}

func (c testBindingGrantCompatibility) RevokeBindingGrants(ctx context.Context, workspace, bindingID string) (int, error) {
	grants, err := c.grants.ListGrantRecordsByBinding(ctx, workspace, bindingID)
	if err != nil {
		return 0, err
	}
	revoked := 0
	for _, grant := range grants {
		if grant == nil {
			continue
		}
		if err := c.grants.RevokeGrantRecord(ctx, workspace, grant.GrantID); err != nil {
			if errors.Is(err, connectorsmodule.ErrGrantRevoked) {
				continue
			}
			return revoked, err
		}
		revoked++
	}
	return revoked, nil
}

func newTestAgentsModule(agentSvc agentcoord.AgentService, st store.Store, hub *realtime.Hub, workspace string) *Module {
	config := Config{
		Store: st, Hub: hub,
		OperatorAuthority: testOperatorAuthorityResolver{}, WorkspaceFromContext: func(context.Context) string { return workspace },
	}
	if st != nil {
		config.AgentRecords = &testAgentRecordAPI{store: st}
		config.AgentRecordAuthority = testAgentRecordAuthorityResolver{}
		bindings := &testBindingOperations{store: st}
		provisioning := newTestAgentProvisioning(st, bindings)
		config.Bindings = bindings
		config.BindingGrants = testBindingGrantCompatibility{grants: st.Connectors()}
		config.Provisioning = provisioning
		config.ProvisioningAuthority = provisioning
		config.PrepareWorkflowTarget = testWorkflowTargetPreparation(st)
	}
	return New(config)
}

func testWorkflowTargetPreparation(
	st store.Store,
) func(context.Context, string, string) (*workflowcatalog.Driver, error) {
	return func(ctx context.Context, workspace, workflow string) (*workflowcatalog.Driver, error) {
		driverRecord, err := st.Drivers().Get(ctx, workspace, workflow)
		if err != nil {
			if !errors.Is(err, domain.ErrNotFound) || !workflowdefs.IsBuiltinWorkflow(workflow) {
				return nil, err
			}
			spec, ok := workflowdefs.BuiltinWorkflow(workflow)
			if !ok {
				return nil, err
			}
			digest, digestErr := workflowdefs.SourceDigest(spec.Files)
			if digestErr != nil {
				return nil, digestErr
			}
			_, _, buildErr := workflowdefs.BuildAndAuthorManaged(
				ctx,
				testUnexpectedManagedAuthoring{},
				authority.SystemAuthority{},
				workflowdefs.BuildAndRegisterOptions{
					WorkspaceKey:  workspace,
					Name:          workflow,
					Entrypoint:    spec.Entrypoint,
					Files:         spec.Files,
					Activate:      true,
					SourceRef:     workflowcatalog.BuiltinSourceRef(workflow, digest),
					SourceDigest:  digest,
					DeriveRunners: true,
				},
			)
			if buildErr != nil {
				return nil, buildErr
			}
			return nil, errors.New("test managed builtin authoring unexpectedly succeeded without a persistence adapter")
		}
		version, err := st.DriverVersions().Get(ctx, workspace, driverRecord.ActiveVersionID)
		if err != nil {
			return nil, err
		}
		root := filepath.Join(
			os.Getenv("LOOM_WORKSPACE_RUNTIME_DIR"),
			filepath.FromSlash(version.BundleRef),
		)
		for _, relative := range []string{"manifest.json", filepath.Join("dist", "server.mjs")} {
			info, statErr := os.Stat(filepath.Join(root, relative))
			if statErr != nil || info.IsDir() {
				return nil, workflowdefs.ErrBuildToolchainUnavailable
			}
		}
		return driverRecord, nil
	}
}

type testUnexpectedManagedAuthoring struct {
	workflowcatalog.VersionAuthoringAPI
}

func (testUnexpectedManagedAuthoring) AuthorManagedVersion(
	context.Context,
	authority.SystemAuthority,
	workflowcatalog.AuthorManagedVersionCommand,
) (*workflowcatalog.AuthorVersionResult, error) {
	return nil, errors.New("test managed builtin reached authoring after a successful build")
}

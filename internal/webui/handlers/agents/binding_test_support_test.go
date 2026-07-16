package agents

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	workflowcataloghttp "github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog/httpapi"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
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

type testBindingGrantCompatibility struct{ grants store.ConnectorGrantStore }

type testTriggerConnectorCompatibility struct {
	testBindingGrantCompatibility
}

func (testTriggerConnectorCompatibility) ConfigureBindingSecret(context.Context, string, string, string, string) error {
	return nil
}

func (c testBindingGrantCompatibility) RevokeBindingGrants(ctx context.Context, workspace, bindingID string) (int, error) {
	grants, err := c.grants.ListByBinding(ctx, workspace, bindingID)
	if err != nil {
		return 0, err
	}
	revoked := 0
	for _, grant := range grants {
		if grant == nil {
			continue
		}
		if err := c.grants.Revoke(ctx, workspace, grant.GrantID); err != nil {
			if errors.Is(err, domain.ErrGrantRevoked) {
				continue
			}
			return revoked, err
		}
		revoked++
	}
	return revoked, nil
}

func newTestAgentsModule(agentSvc service.AgentService, st store.Store, hub *realtime.Hub, workspace string) *Module {
	config := Config{
		AgentService: agentSvc, Store: st, Hub: hub,
		OperatorAuthority: testOperatorAuthorityResolver{}, WorkspaceFromContext: func(context.Context) string { return workspace },
	}
	if st != nil {
		config.Bindings = &testBindingOperations{store: st}
		config.BindingGrants = testBindingGrantCompatibility{grants: st.ConnectorGrants()}
	}
	return New(config)
}

package agentcomposition

import (
	"context"
	"errors"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	infrafleetdb "github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	agentsfleetdb "github.com/tysonthomas9/loomcli/internal/modules/agents/fleetdb"
)

// agentsFleetDBTransport is the composition-owned bridge between the one
// process-wide FleetDB client and Agents' consumer-owned transport contract.
// It maps DTOs and stable errors only; authority and aggregate policy remain
// inside Agents.
type agentsFleetDBTransport struct {
	transport infrafleetdb.AgentManagementTransport
}

var _ agentsfleetdb.Transport = (*agentsFleetDBTransport)(nil)

func newAgentsFleetDBTransport(client *infrafleetdb.Client) agentsfleetdb.Transport {
	if client == nil {
		return nil
	}
	return &agentsFleetDBTransport{transport: client.AgentManagement()}
}

func (transport *agentsFleetDBTransport) GetAgentService(
	ctx context.Context,
	workspace,
	serviceID string,
) (*agentsfleetdb.AgentServiceWire, error) {
	value, err := transport.transport.GetAgentService(ctx, workspace, serviceID)
	return agentServiceWire(value), translateAgentsFleetDBError(err)
}

func (transport *agentsFleetDBTransport) ListAgentServices(
	ctx context.Context,
	workspace string,
	filter agentsfleetdb.AgentServiceFilterWire,
) ([]*agentsfleetdb.AgentServiceWire, error) {
	values, err := transport.transport.ListAgentServices(ctx, workspace, infrafleetdb.AgentServiceQuery{
		Kind: domain.AgentServiceKind(filter.Kind), DesiredState: domain.AgentServiceDesiredState(filter.DesiredState),
		RoleName: filter.RoleName, IncludeDeleted: filter.IncludeDeleted, Limit: filter.Limit,
	})
	if err != nil {
		return nil, translateAgentsFleetDBError(err)
	}
	out := make([]*agentsfleetdb.AgentServiceWire, len(values))
	for index, value := range values {
		out[index] = agentServiceWire(value)
	}
	return out, nil
}

func (transport *agentsFleetDBTransport) GetRoleReference(
	ctx context.Context,
	workspace,
	roleName string,
) (*agentsfleetdb.RoleReferenceWire, error) {
	value, err := transport.transport.GetAgentRole(ctx, workspace, roleName)
	if err != nil {
		return nil, translateAgentsFleetDBError(err)
	}
	if value == nil {
		return nil, nil
	}
	return &agentsfleetdb.RoleReferenceWire{WorkspaceKey: value.WorkspaceKey, Name: value.Name}, nil
}

func (transport *agentsFleetDBTransport) GetRole(
	ctx context.Context,
	workspace,
	roleName string,
) (*agentsfleetdb.RoleWire, error) {
	value, err := transport.transport.GetAgentRole(ctx, workspace, roleName)
	return roleWire(value), translateAgentsFleetDBError(err)
}

func (transport *agentsFleetDBTransport) ListRoles(
	ctx context.Context,
	workspace string,
) ([]*agentsfleetdb.RoleWire, error) {
	values, err := transport.transport.ListAgentRoles(ctx, workspace)
	if err != nil {
		return nil, translateAgentsFleetDBError(err)
	}
	out := make([]*agentsfleetdb.RoleWire, len(values))
	for index, value := range values {
		out[index] = roleWire(value)
	}
	return out, nil
}

func (transport *agentsFleetDBTransport) CreateRole(
	ctx context.Context,
	workspace string,
	definition agents.RoleDefinition,
) (*agentsfleetdb.RoleWire, error) {
	value, err := transport.transport.CreateAgentRole(ctx, workspace, infrafleetdb.AgentRoleInput{
		Name: definition.Name, Kind: definition.Kind, Description: definition.Description,
		Prompt: definition.Prompt, PromptFile: definition.PromptFile, Model: definition.Model,
		TaskFilter: definition.TaskFilter, Backend: definition.Backend, Effort: definition.Effort,
		PathPatterns:   append([]string(nil), definition.PathPatterns...),
		Skills:         append([]string(nil), definition.Skills...),
		MaxPriority:    cloneAgentsInt(definition.MaxPriority),
		MaxConcurrency: cloneAgentsInt(definition.MaxConcurrency),
		ReadOnly:       definition.ReadOnly,
		AllowedTools:   append([]string(nil), definition.AllowedTools...),
		DeniedTools:    append([]string(nil), definition.DeniedTools...),
		MaxBudgetUSD:   cloneAgentsFloat64(definition.MaxBudgetUSD),
	})
	return roleWire(value), translateAgentsFleetDBError(err)
}

func (transport *agentsFleetDBTransport) UpdateRole(
	ctx context.Context,
	input agentsfleetdb.UpdateRoleWire,
) (*agentsfleetdb.RoleWire, error) {
	value, err := transport.transport.UpdateAgentRole(ctx, infrafleetdb.AgentRoleUpdateInput{
		WorkspaceKey: input.WorkspaceKey, RoleName: input.RoleName,
		ExpectedUpdatedAt: input.ExpectedUpdatedAt,
		Patch: infrafleetdb.AgentRolePatch{
			Kind: cloneAgentsString(input.Patch.Kind), Description: cloneAgentsString(input.Patch.Description),
			Prompt: cloneAgentsString(input.Patch.Prompt), PromptFile: cloneAgentsString(input.Patch.PromptFile),
			Model: cloneAgentsString(input.Patch.Model), TaskFilter: cloneAgentsString(input.Patch.TaskFilter),
			Backend: cloneAgentsString(input.Patch.Backend), Effort: cloneAgentsString(input.Patch.Effort),
			PathPatterns:   cloneAgentsStringSlicePointer(input.Patch.PathPatterns),
			Skills:         cloneAgentsStringSlicePointer(input.Patch.Skills),
			MaxPriority:    cloneAgentsOptionalIntPointer(input.Patch.MaxPriority),
			MaxConcurrency: cloneAgentsOptionalIntPointer(input.Patch.MaxConcurrency),
			ReadOnly:       cloneAgentsBool(input.Patch.ReadOnly),
			AllowedTools:   cloneAgentsStringSlicePointer(input.Patch.AllowedTools),
			DeniedTools:    cloneAgentsStringSlicePointer(input.Patch.DeniedTools),
			MaxBudgetUSD:   cloneAgentsOptionalFloat64Pointer(input.Patch.MaxBudgetUSD),
		},
		DelegatedActor: input.UpdatedBy,
	})
	return roleWire(value), translateAgentsFleetDBError(err)
}

func (transport *agentsFleetDBTransport) DeleteRole(
	ctx context.Context,
	input agentsfleetdb.DeleteRoleWire,
) error {
	return translateAgentsFleetDBError(transport.transport.DeleteAgentRole(ctx, infrafleetdb.AgentRoleDeleteInput{
		WorkspaceKey: input.WorkspaceKey, RoleName: input.RoleName,
		ExpectedUpdatedAt: input.ExpectedUpdatedAt, DelegatedActor: input.DeletedBy,
	}))
}

func (transport *agentsFleetDBTransport) CreateAgentService(
	ctx context.Context,
	input agentsfleetdb.CreateAgentServiceWire,
) (*agentsfleetdb.AgentServiceWire, error) {
	value, err := transport.transport.CreateAgentService(ctx, infrafleetdb.AgentServiceCreateInput{
		WorkspaceKey: input.WorkspaceKey, ServiceID: input.ServiceID, Name: input.Name,
		Kind: domain.AgentServiceKind(input.Kind), DesiredState: domain.AgentServiceDesiredState(input.DesiredState),
		RoleName: input.RoleName, DriverID: input.DriverID, DriverVersionID: input.DriverVersionID,
		ProfileName: input.ProfileName, ScheduleID: input.ScheduleID,
		EventSources:    append([]string(nil), input.EventSources...),
		TriggerRefs:     append([]string(nil), input.TriggerRefs...),
		PlacementPolicy: input.PlacementPolicy, MaxInstances: input.MaxInstances,
		LeaseID: input.LeaseID, RestartPolicy: input.RestartPolicy,
		Permissions:  append([]string(nil), input.Permissions...),
		BudgetPolicy: input.BudgetPolicy, StateRef: input.StateRef,
		Metadata: cloneAgentsMap(input.Metadata), DelegatedActor: input.CreatedBy,
	})
	return agentServiceWire(value), translateAgentsFleetDBError(err)
}

func (transport *agentsFleetDBTransport) UpdateAgentService(
	ctx context.Context,
	input agentsfleetdb.UpdateAgentServiceWire,
) (*agentsfleetdb.AgentServiceWire, error) {
	value, err := transport.transport.UpdateAgentServiceIdentity(ctx, infrafleetdb.AgentServiceUpdateInput{
		WorkspaceKey: input.WorkspaceKey, ServiceID: input.ServiceID,
		ExpectedUpdatedAt: input.ExpectedUpdatedAt,
		Patch: infrafleetdb.AgentServiceIdentityPatch{
			Name:            cloneAgentsString(input.Patch.Name),
			Kind:            agentServiceKindPointer(input.Patch.Kind),
			RoleName:        cloneAgentsString(input.Patch.RoleName),
			DriverID:        cloneAgentsString(input.Patch.DriverID),
			DriverVersionID: cloneAgentsString(input.Patch.DriverVersionID),
			ProfileName:     cloneAgentsString(input.Patch.ProfileName),
			ScheduleID:      cloneAgentsString(input.Patch.ScheduleID),
			EventSources:    cloneAgentsStringSlicePointer(input.Patch.EventSources),
			TriggerRefs:     cloneAgentsStringSlicePointer(input.Patch.TriggerRefs),
			PlacementPolicy: cloneAgentsString(input.Patch.PlacementPolicy),
			MaxInstances:    cloneAgentsInt(input.Patch.MaxInstances),
			LeaseID:         cloneAgentsString(input.Patch.LeaseID),
			RestartPolicy:   cloneAgentsString(input.Patch.RestartPolicy),
			Permissions:     cloneAgentsStringSlicePointer(input.Patch.Permissions),
			BudgetPolicy:    cloneAgentsString(input.Patch.BudgetPolicy),
			StateRef:        cloneAgentsString(input.Patch.StateRef),
			Metadata:        cloneAgentsMapPointer(input.Patch.Metadata),
		},
		DelegatedActor: input.UpdatedBy,
	})
	return agentServiceWire(value), translateAgentsFleetDBError(err)
}

func (transport *agentsFleetDBTransport) ArchiveAgentService(
	ctx context.Context,
	input agentsfleetdb.ArchiveAgentServiceWire,
) (*agentsfleetdb.AgentServiceWire, error) {
	value, err := transport.transport.ArchiveAgentService(ctx, infrafleetdb.AgentServiceArchiveInput{
		WorkspaceKey: input.WorkspaceKey, ServiceID: input.ServiceID,
		ExpectedUpdatedAt: input.ExpectedUpdatedAt, DelegatedActor: input.ArchivedBy,
	})
	return agentServiceWire(value), translateAgentsFleetDBError(err)
}

func (transport *agentsFleetDBTransport) SetAgentServiceDesiredState(
	ctx context.Context,
	input agentsfleetdb.DesiredStateWire,
) (*agentsfleetdb.AgentServiceWire, error) {
	value, err := transport.transport.SetAgentServiceDesiredState(ctx, infrafleetdb.AgentServiceDesiredStateInput{
		WorkspaceKey: input.WorkspaceKey, ServiceID: input.ServiceID,
		ExpectedState:     domain.AgentServiceDesiredState(input.ExpectedState),
		DesiredState:      domain.AgentServiceDesiredState(input.DesiredState),
		ExpectedUpdatedAt: input.ExpectedUpdatedAt, DelegatedActor: input.ChangedBy,
	})
	return agentServiceWire(value), translateAgentsFleetDBError(err)
}

func (transport *agentsFleetDBTransport) SetAgentServiceDesiredStateOwned(
	ctx context.Context,
	input agentsfleetdb.OwnedDesiredStateWire,
) (*agentsfleetdb.AgentServiceWire, error) {
	value, err := transport.transport.SetAgentServiceDesiredStateOwned(ctx, infrafleetdb.AgentServiceOwnedDesiredStateInput{
		Proof: infrafleetdb.AgentOwnershipProof{
			WorkspaceKey: input.WorkspaceKey, AgentID: input.ServiceID,
			LeaseID: input.LeaseID, LeaseToken: input.LeaseToken, OwnerID: input.OwnerID,
			RuntimeProvider: domain.RuntimeProvider(input.RuntimeProvider),
			NodeID:          input.NodeID, FencingToken: input.FencingToken,
		},
		ExpectedState:     domain.AgentServiceDesiredState(input.ExpectedState),
		DesiredState:      domain.AgentServiceDesiredState(input.DesiredState),
		ExpectedUpdatedAt: input.ExpectedUpdatedAt, IdempotencyKey: input.IdempotencyKey,
		DelegatedActor: input.OwnerID,
	})
	return agentServiceWire(value), translateAgentsFleetDBError(err)
}

func (transport *agentsFleetDBTransport) ApplyAgentServiceLifecycle(
	ctx context.Context,
	input agentsfleetdb.LifecycleWire,
) (*agentsfleetdb.LifecycleResultWire, error) {
	value, err := transport.transport.ApplyAgentServiceLifecycle(ctx, infrafleetdb.AgentServiceLifecycleInput{
		WorkspaceKey: input.WorkspaceKey, ServiceID: input.ServiceID,
		Action: input.Action, ExpectedUpdatedAt: input.ExpectedUpdatedAt,
		ExpectedGenerationID: input.ExpectedGenerationID,
		IdempotencyKey:       input.IdempotencyKey, DelegatedActor: input.ChangedBy,
	})
	if err != nil {
		return nil, translateAgentsFleetDBError(err)
	}
	if value == nil {
		return nil, nil
	}
	return &agentsfleetdb.LifecycleResultWire{
		WorkspaceKey: value.WorkspaceKey, ServiceID: value.ServiceID,
		IdempotencyKey: value.IdempotencyKey, Action: value.Action,
		Agent: agentServiceWire(value.Agent), BindingIDs: append([]string(nil), value.BindingIDs...),
		GrantIDs: append([]string(nil), value.GrantIDs...), CommittedAt: value.CommittedAt,
	}, nil
}

func (transport *agentsFleetDBTransport) AcquireAgentOwnership(
	ctx context.Context,
	input agentsfleetdb.AcquireOwnershipWire,
) (*agentsfleetdb.AgentOwnershipLeaseWire, error) {
	grant, err := transport.transport.AcquireAgentOwnership(ctx, infrafleetdb.AgentOwnershipAcquireInput{
		WorkspaceKey: input.WorkspaceKey, AgentID: input.AgentID, LeaseID: input.LeaseID,
		OwnerID: input.OwnerID, RuntimeProvider: domain.RuntimeProvider(input.RuntimeProvider),
		NodeID: input.NodeID, TTLSeconds: input.TTLSeconds, DelegatedActor: input.OwnerID,
	})
	if err != nil {
		return nil, translateAgentsFleetDBError(err)
	}
	if grant == nil {
		return nil, nil
	}
	out := agentOwnershipWire(grant.Lease)
	if out != nil {
		out.Token = grant.Token
	}
	return out, nil
}

func (transport *agentsFleetDBTransport) GetAgentOwnership(
	ctx context.Context,
	workspace,
	agentID string,
) (*agentsfleetdb.AgentOwnershipLeaseWire, error) {
	value, err := transport.transport.GetAgentOwnership(ctx, workspace, agentID)
	return agentOwnershipWire(value), translateAgentsFleetDBError(err)
}

func (transport *agentsFleetDBTransport) ListAgentOwnership(
	ctx context.Context,
	workspace string,
	filter agentsfleetdb.OwnershipFilterWire,
) ([]*agentsfleetdb.AgentOwnershipLeaseWire, error) {
	values, err := transport.transport.ListAgentOwnership(ctx, workspace, infrafleetdb.AgentOwnershipQuery{
		OwnerID: filter.OwnerID, RuntimeProvider: domain.RuntimeProvider(filter.RuntimeProvider),
		NodeID: filter.NodeID, Status: domain.AgentLeaseStatus(filter.Status), Limit: filter.Limit,
	})
	if err != nil {
		return nil, translateAgentsFleetDBError(err)
	}
	out := make([]*agentsfleetdb.AgentOwnershipLeaseWire, len(values))
	for index, value := range values {
		out[index] = agentOwnershipWire(value)
	}
	return out, nil
}

func (transport *agentsFleetDBTransport) RenewAgentOwnership(
	ctx context.Context,
	input agentsfleetdb.RenewOwnershipWire,
) (*agentsfleetdb.AgentOwnershipLeaseWire, error) {
	value, err := transport.transport.RenewAgentOwnership(ctx, infrafleetdb.AgentOwnershipRenewInput{
		Proof:          agentOwnershipProof(input.Ownership),
		TTLSeconds:     input.TTLSeconds,
		DelegatedActor: input.Ownership.OwnerID,
	})
	return agentOwnershipWire(value), translateAgentsFleetDBError(err)
}

func (transport *agentsFleetDBTransport) ReleaseAgentOwnership(
	ctx context.Context,
	input agentsfleetdb.OwnershipProofWire,
) (*agentsfleetdb.AgentOwnershipLeaseWire, error) {
	value, err := transport.transport.ReleaseAgentOwnership(ctx, infrafleetdb.AgentOwnershipReleaseInput{
		Proof: agentOwnershipProof(input), DelegatedActor: input.OwnerID,
	})
	return agentOwnershipWire(value), translateAgentsFleetDBError(err)
}

func agentServiceWire(value *domain.AgentService) *agentsfleetdb.AgentServiceWire {
	if value == nil {
		return nil
	}
	return &agentsfleetdb.AgentServiceWire{
		WorkspaceKey: value.WorkspaceKey, ServiceID: value.ServiceID,
		GenerationID: value.GenerationID, Name: value.Name,
		Kind: string(value.Kind), DesiredState: string(value.DesiredState),
		RoleName: value.RoleName, DriverID: value.DriverID, DriverVersionID: value.DriverVersionID,
		ProfileName: value.ProfileName, ScheduleID: value.ScheduleID,
		EventSources:    append([]string(nil), value.EventSources...),
		TriggerRefs:     append([]string(nil), value.TriggerRefs...),
		PlacementPolicy: value.PlacementPolicy, MaxInstances: value.MaxInstances,
		LeaseID: value.LeaseID, RestartPolicy: value.RestartPolicy,
		Permissions:  append([]string(nil), value.Permissions...),
		BudgetPolicy: value.BudgetPolicy, StateRef: value.StateRef,
		Metadata: cloneAgentsMap(value.Metadata), CreatedBy: value.CreatedBy,
		DeletedAt: cloneAgentsTime(value.DeletedAt), CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}

func roleWire(value *domain.Role) *agentsfleetdb.RoleWire {
	if value == nil {
		return nil
	}
	return &agentsfleetdb.RoleWire{
		WorkspaceKey: value.WorkspaceKey, Name: value.Name, Kind: string(value.Kind),
		Description: value.Description, Prompt: value.Prompt, PromptFile: value.PromptFile,
		Model: value.Model, TaskFilter: value.TaskFilter, Backend: value.Backend, Effort: value.Effort,
		PathPatterns:   append([]string(nil), value.PathPatterns...),
		Skills:         append([]string(nil), value.Skills...),
		MaxPriority:    cloneAgentsInt(value.MaxPriority),
		MaxConcurrency: cloneAgentsInt(value.MaxConcurrency),
		ReadOnly:       value.ReadOnly,
		AllowedTools:   append([]string(nil), value.AllowedTools...),
		DeniedTools:    append([]string(nil), value.DeniedTools...),
		MaxBudgetUSD:   cloneAgentsFloat64(value.MaxBudgetUSD),
		CreatedAt:      value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}

func agentOwnershipWire(value *domain.AgentOwnershipLease) *agentsfleetdb.AgentOwnershipLeaseWire {
	if value == nil {
		return nil
	}
	return &agentsfleetdb.AgentOwnershipLeaseWire{
		WorkspaceKey: value.WorkspaceKey, AgentID: value.AgentID, LeaseID: value.LeaseID,
		OwnerID: value.OwnerID, RuntimeProvider: string(value.RuntimeProvider), NodeID: value.NodeID,
		FencingToken: value.FencingToken, Status: string(value.Status), ExpiresAt: value.ExpiresAt,
		LastHeartbeat: value.LastHeartbeat, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}

func agentOwnershipProof(value agentsfleetdb.OwnershipProofWire) infrafleetdb.AgentOwnershipProof {
	return infrafleetdb.AgentOwnershipProof{
		WorkspaceKey: value.WorkspaceKey, AgentID: value.AgentID,
		LeaseID: value.LeaseID, LeaseToken: value.LeaseToken, OwnerID: value.OwnerID,
		RuntimeProvider: domain.RuntimeProvider(value.RuntimeProvider),
		NodeID:          value.NodeID, FencingToken: value.FencingToken,
	}
}

func translateAgentsFleetDBError(err error) error {
	if err == nil {
		return nil
	}
	var translated error
	switch {
	case errors.Is(err, domain.ErrNotFound):
		translated = agentsfleetdb.ErrTransportNotFound
	case errors.Is(err, domain.ErrInvalid), errors.Is(err, infrafleetdb.ErrAgentManagementInvalidDelegatedActor):
		translated = agentsfleetdb.ErrTransportInvalid
	case errors.Is(err, domain.ErrAlreadyExists):
		translated = agentsfleetdb.ErrTransportAlreadyExists
	case errors.Is(err, domain.ErrNotOwner):
		translated = agentsfleetdb.ErrTransportNotOwner
	case errors.Is(err, domain.ErrInvalidTransition), errors.Is(err, domain.ErrGone):
		translated = agentsfleetdb.ErrTransportInvalidTransition
	case errors.Is(err, domain.ErrConflict), errors.Is(err, domain.ErrAlreadyClaimed),
		errors.Is(err, infrafleetdb.ErrAgentRoleRevisionConflict),
		errors.Is(err, infrafleetdb.ErrAgentServiceRevisionConflict),
		errors.Is(err, infrafleetdb.ErrAgentServiceDesiredStateConflict),
		errors.Is(err, infrafleetdb.ErrAgentServiceIdempotencyConflict):
		translated = agentsfleetdb.ErrTransportConflict
	default:
		translated = agentsfleetdb.ErrTransportUnavailable
	}
	return errors.Join(translated, err)
}

func cloneAgentsMap(value map[string]string) map[string]string {
	if value == nil {
		return nil
	}
	out := make(map[string]string, len(value))
	for key, item := range value {
		out[key] = item
	}
	return out
}

func cloneAgentsMapPointer(value *map[string]string) *map[string]string {
	if value == nil {
		return nil
	}
	out := cloneAgentsMap(*value)
	return &out
}

func cloneAgentsString(value *string) *string {
	if value == nil {
		return nil
	}
	out := *value
	return &out
}

func cloneAgentsStringSlicePointer(value *[]string) *[]string {
	if value == nil {
		return nil
	}
	out := append([]string(nil), (*value)...)
	return &out
}

func cloneAgentsBool(value *bool) *bool {
	if value == nil {
		return nil
	}
	out := *value
	return &out
}

func cloneAgentsOptionalIntPointer(value **int) **int {
	if value == nil {
		return nil
	}
	out := cloneAgentsInt(*value)
	return &out
}

func cloneAgentsOptionalFloat64Pointer(value **float64) **float64 {
	if value == nil {
		return nil
	}
	out := cloneAgentsFloat64(*value)
	return &out
}

func agentServiceKindPointer(value *string) *domain.AgentServiceKind {
	if value == nil {
		return nil
	}
	out := domain.AgentServiceKind(*value)
	return &out
}

func cloneAgentsInt(value *int) *int {
	if value == nil {
		return nil
	}
	out := *value
	return &out
}

func cloneAgentsFloat64(value *float64) *float64 {
	if value == nil {
		return nil
	}
	out := *value
	return &out
}

func cloneAgentsTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	out := *value
	return &out
}

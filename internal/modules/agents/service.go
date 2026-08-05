package agents

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

// Service implements Agents over capability-owned, transport-neutral ports.
type Service struct {
	reader    AgentReader
	roles     RoleReferenceReader
	roleStore RoleStore
	identity  AgentIdentityStore
	desired   DesiredStateStore
	lifecycle LifecycleStore
	ownership OwnershipStore
	admission *authority.Admission
}

// NewWithLifecycle composes the complete operator lifecycle path without
// changing the legacy constructor used by still-migrating tests. The returned
// service fails closed if the atomic lifecycle port is absent.
func NewWithLifecycle(
	reader AgentReader,
	roles RoleReferenceReader,
	roleStore RoleStore,
	identity AgentIdentityStore,
	desired DesiredStateStore,
	ownership OwnershipStore,
	lifecycle LifecycleStore,
	admission *authority.Admission,
) (*Service, error) {
	if lifecycle == nil {
		return nil, fmt.Errorf("compose Agents lifecycle: atomic lifecycle store is required: %w", ErrUnavailable)
	}
	service, err := New(reader, roles, roleStore, identity, desired, ownership, admission)
	if err != nil {
		return nil, err
	}
	service.lifecycle = lifecycle
	return service, nil
}

var _ API = (*Service)(nil)

// New fails closed unless the complete Phase 5 Agents boundary is available.
// Composition may supply several interfaces from one adapter, but the module
// never receives a composite legacy Store or a transport client.
func New(
	reader AgentReader,
	roles RoleReferenceReader,
	roleStore RoleStore,
	identity AgentIdentityStore,
	desired DesiredStateStore,
	ownership OwnershipStore,
	admission *authority.Admission,
) (*Service, error) {
	if reader == nil || roles == nil || roleStore == nil || identity == nil ||
		desired == nil || ownership == nil || admission == nil {
		return nil, fmt.Errorf("compose Agents: all capability ports and admission are required: %w", ErrUnavailable)
	}
	return &Service{
		reader: reader, roles: roles, roleStore: roleStore, identity: identity, desired: desired,
		ownership: ownership, admission: admission,
	}, nil
}

func (s *Service) GetAgent(ctx context.Context, workspace, agentID string) (*Agent, error) {
	workspace, agentID, err := normalizeWorkspaceAndAgent(workspace, agentID)
	if err != nil {
		return nil, err
	}
	if s == nil || s.reader == nil {
		return nil, ErrUnavailable
	}
	agent, err := s.reader.GetAgent(ctx, workspace, agentID)
	if err != nil {
		return nil, fmt.Errorf("get agent %q: %w", agentID, err)
	}
	if err := validatePersistedAgent(agent, workspace, agentID); err != nil {
		return nil, err
	}
	return cloneAgent(agent), nil
}

func (s *Service) ListAgents(ctx context.Context, workspace string, filter AgentFilter) ([]*Agent, error) {
	workspace, err := normalizeWorkspace(workspace)
	if err != nil {
		return nil, err
	}
	filter, err = normalizeAgentFilter(filter)
	if err != nil {
		return nil, err
	}
	if s == nil || s.reader == nil {
		return nil, ErrUnavailable
	}
	values, err := s.reader.ListAgents(ctx, workspace, filter)
	if err != nil {
		return nil, fmt.Errorf("list agents: %w", err)
	}
	out := make([]*Agent, 0, len(values))
	for _, agent := range values {
		if err := validatePersistedAgent(agent, workspace, ""); err != nil {
			return nil, err
		}
		if filter.Kind != "" && agent.Kind != filter.Kind {
			return nil, ErrInvalidPersistedState
		}
		if filter.DesiredState != "" && agent.DesiredState != filter.DesiredState {
			return nil, ErrInvalidPersistedState
		}
		if filter.RoleName != "" && agent.Behavior.RoleName != filter.RoleName {
			return nil, ErrInvalidPersistedState
		}
		if !filter.IncludeDeleted && agent.DeletedAt != nil {
			return nil, ErrInvalidPersistedState
		}
		out = append(out, cloneAgent(agent))
	}
	return out, nil
}

//nolint:cyclop // Aggregate creation keeps authority, uniqueness, persistence, and exact-result checks in one command boundary.
func (s *Service) CreateAgent(
	ctx context.Context,
	auth authority.OperatorAuthority,
	command CreateAgentCommand,
) (*Agent, error) {
	command, err := normalizeCreateCommand(command)
	if err != nil {
		return nil, err
	}
	if err := s.requireOperator(ActionCreateAgent, command.WorkspaceKey, auth); err != nil {
		return nil, err
	}
	if err := s.validateRoleBackedBehavior(ctx, command.WorkspaceKey, command.Behavior); err != nil {
		return nil, err
	}
	mutation := CreateAgentMutation{
		WorkspaceKey: command.WorkspaceKey, AgentID: command.AgentID, Name: command.Name,
		Kind: command.Kind, Behavior: command.Behavior, DesiredState: command.DesiredState,
		ProfileName: command.ProfileName, ScheduleID: command.ScheduleID,
		EventSources: slices.Clone(command.EventSources), TriggerRefs: slices.Clone(command.TriggerRefs),
		PlacementPolicy: command.PlacementPolicy, MaxInstances: command.MaxInstances,
		LeaseID: command.LeaseID, RestartPolicy: command.RestartPolicy,
		Permissions: slices.Clone(command.Permissions), BudgetPolicy: command.BudgetPolicy,
		StateRef: command.StateRef,
		Metadata: cloneStringMap(command.Metadata), CreatedBy: auth.Subject(),
	}
	agent, err := s.identity.CreateAgent(ctx, mutation)
	if err != nil {
		return nil, fmt.Errorf("create agent %q: %w", command.AgentID, err)
	}
	if err := validatePersistedAgent(agent, command.WorkspaceKey, command.AgentID); err != nil {
		return nil, err
	}
	if agent.Name != command.Name || agent.Kind != command.Kind || agent.Behavior != command.Behavior ||
		agent.DesiredState != command.DesiredState || agent.PlacementPolicy != command.PlacementPolicy ||
		agent.ProfileName != command.ProfileName || agent.ScheduleID != command.ScheduleID ||
		!slices.Equal(agent.EventSources, command.EventSources) || !slices.Equal(agent.TriggerRefs, command.TriggerRefs) ||
		agent.MaxInstances != command.MaxInstances || agent.RestartPolicy != command.RestartPolicy ||
		agent.LeaseID != command.LeaseID || !slices.Equal(agent.Permissions, command.Permissions) ||
		agent.BudgetPolicy != command.BudgetPolicy || !equalStringMap(agent.Metadata, command.Metadata) ||
		agent.StateRef != command.StateRef ||
		agent.CreatedBy != auth.Subject() ||
		agent.DeletedAt != nil {
		return nil, ErrInvalidPersistedState
	}
	return cloneAgent(agent), nil
}

func (s *Service) UpdateAgent(
	ctx context.Context,
	auth authority.OperatorAuthority,
	command UpdateAgentCommand,
) (*Agent, error) {
	command, err := normalizeUpdateCommand(command)
	if err != nil {
		return nil, err
	}
	if err := s.requireOperator(ActionUpdateAgent, command.WorkspaceKey, auth); err != nil {
		return nil, err
	}
	if command.Patch.Behavior != nil {
		if err := s.validateRoleBackedBehavior(ctx, command.WorkspaceKey, *command.Patch.Behavior); err != nil {
			return nil, err
		}
	}
	agent, err := s.identity.UpdateAgent(ctx, UpdateAgentMutation{
		WorkspaceKey: command.WorkspaceKey, AgentID: command.AgentID,
		ExpectedUpdatedAt: command.ExpectedUpdatedAt, Patch: command.Patch,
		UpdatedBy: auth.Subject(),
	})
	if err != nil {
		return nil, fmt.Errorf("update agent %q: %w", command.AgentID, err)
	}
	if err := validatePersistedAgent(agent, command.WorkspaceKey, command.AgentID); err != nil {
		return nil, err
	}
	if err := validatePatchedAgent(agent, command); err != nil {
		return nil, err
	}
	return cloneAgent(agent), nil
}

func (s *Service) ArchiveAgent(
	ctx context.Context,
	auth authority.OperatorAuthority,
	command ArchiveAgentCommand,
) (*Agent, error) {
	command, err := normalizeArchiveCommand(command)
	if err != nil {
		return nil, err
	}
	if err := s.requireOperator(ActionArchiveAgent, command.WorkspaceKey, auth); err != nil {
		return nil, err
	}
	agent, err := s.identity.ArchiveAgent(ctx, ArchiveAgentMutation{
		WorkspaceKey: command.WorkspaceKey, AgentID: command.AgentID,
		ExpectedUpdatedAt: command.ExpectedUpdatedAt, ArchivedBy: auth.Subject(),
	})
	if err != nil {
		return nil, fmt.Errorf("archive agent %q: %w", command.AgentID, err)
	}
	if err := validatePersistedAgent(agent, command.WorkspaceKey, command.AgentID); err != nil {
		return nil, err
	}
	if agent.DeletedAt == nil || agent.DeletedAt.Before(command.ExpectedUpdatedAt) ||
		agent.UpdatedAt.Before(command.ExpectedUpdatedAt) {
		return nil, ErrInvalidPersistedState
	}
	return cloneAgent(agent), nil
}

func (s *Service) SetDesiredState(
	ctx context.Context,
	auth authority.OperatorAuthority,
	command SetDesiredStateCommand,
) (*Agent, error) {
	command, err := normalizeSetDesiredStateCommand(command)
	if err != nil {
		return nil, err
	}
	if err := s.requireOperator(ActionSetDesiredState, command.WorkspaceKey, auth); err != nil {
		return nil, err
	}
	agent, err := s.desired.SetDesiredState(ctx, DesiredStateMutation{
		WorkspaceKey: command.WorkspaceKey, AgentID: command.AgentID,
		ExpectedState: command.ExpectedState, DesiredState: command.DesiredState,
		ExpectedUpdatedAt: command.ExpectedUpdatedAt, ChangedBy: auth.Subject(),
	})
	if err != nil {
		return nil, fmt.Errorf("set desired state for agent %q: %w", command.AgentID, err)
	}
	if err := validateDesiredStateResult(agent, command.WorkspaceKey, command.AgentID, command.DesiredState); err != nil {
		return nil, err
	}
	if agent.UpdatedAt.Before(command.ExpectedUpdatedAt) {
		return nil, ErrInvalidPersistedState
	}
	return cloneAgent(agent), nil
}

func (s *Service) SetDesiredStateOwned(
	ctx context.Context,
	auth authority.SystemAuthority,
	ownership OwnershipProof,
	command SetDesiredStateOwnedCommand,
) (*Agent, error) {
	ownership, err := normalizeOwnershipProof(ownership)
	if err != nil {
		return nil, err
	}
	command, err = normalizeSetDesiredStateOwnedCommand(command)
	if err != nil {
		return nil, err
	}
	if err := s.requireSystemOwner(ActionSetDesiredStateOwned, ownership, auth); err != nil {
		return nil, err
	}
	agent, err := s.desired.SetDesiredStateOwned(ctx, OwnedDesiredStateMutation{
		Ownership: ownership, ExpectedState: command.ExpectedState,
		DesiredState: command.DesiredState, ExpectedUpdatedAt: command.ExpectedUpdatedAt,
		IdempotencyKey: command.IdempotencyKey,
	})
	if err != nil {
		return nil, fmt.Errorf("owner-fenced desired state for agent %q: %w", ownership.AgentID, err)
	}
	if err := validateDesiredStateResult(agent, ownership.WorkspaceKey, ownership.AgentID, command.DesiredState); err != nil {
		return nil, err
	}
	if agent.UpdatedAt.Before(command.ExpectedUpdatedAt) {
		return nil, ErrInvalidPersistedState
	}
	return cloneAgent(agent), nil
}

//nolint:cyclop,funlen // Lifecycle transitions enforce the complete ownership and desired-state matrix atomically.
func (s *Service) ApplyLifecycle(
	ctx context.Context,
	auth authority.OperatorAuthority,
	command ApplyLifecycleCommand,
) (*LifecycleResult, error) {
	command.WorkspaceKey = strings.TrimSpace(command.WorkspaceKey)
	command.AgentID = strings.TrimSpace(command.AgentID)
	command.Action = LifecycleAction(strings.TrimSpace(string(command.Action)))
	command.IdempotencyKey = strings.TrimSpace(command.IdempotencyKey)
	if command.WorkspaceKey == "" || command.AgentID == "" ||
		command.ExpectedUpdatedAt.IsZero() || command.IdempotencyKey == "" ||
		len(command.IdempotencyKey) > 128 {
		return nil, fmt.Errorf("lifecycle coordinates, revision, and idempotency key are required: %w", ErrInvalid)
	}
	switch command.Action {
	case LifecycleEnable, LifecycleDisable, LifecycleDelete:
	default:
		return nil, fmt.Errorf("lifecycle action %q: %w", command.Action, ErrInvalid)
	}
	if command.ExpectedGenerationID == "" {
		if command.Action != LifecycleDelete {
			return nil, fmt.Errorf("lifecycle expected generation is required: %w", ErrInvalid)
		}
	} else if !ValidGenerationID(command.ExpectedGenerationID) {
		return nil, fmt.Errorf("lifecycle expected generation is invalid: %w", ErrInvalid)
	}
	if err := s.requireOperator(ActionApplyLifecycle, command.WorkspaceKey, auth); err != nil {
		return nil, err
	}
	if s.lifecycle == nil {
		return nil, ErrUnavailable
	}
	result, err := s.lifecycle.ApplyLifecycle(ctx, ApplyLifecycleMutation{
		WorkspaceKey: command.WorkspaceKey, AgentID: command.AgentID,
		Action: command.Action, ExpectedUpdatedAt: command.ExpectedUpdatedAt,
		ExpectedGenerationID: command.ExpectedGenerationID,
		IdempotencyKey:       command.IdempotencyKey, ChangedBy: auth.Subject(),
	})
	if err != nil {
		return nil, fmt.Errorf("apply lifecycle to agent %q: %w", command.AgentID, err)
	}
	if result == nil || result.Agent == nil ||
		result.WorkspaceKey != command.WorkspaceKey || result.AgentID != command.AgentID ||
		result.IdempotencyKey != command.IdempotencyKey || result.Action != command.Action ||
		result.Agent.WorkspaceKey != command.WorkspaceKey || result.Agent.AgentID != command.AgentID ||
		!ValidGenerationID(result.Agent.GenerationID) ||
		(command.ExpectedGenerationID != "" &&
			result.Agent.GenerationID != command.ExpectedGenerationID) ||
		!result.Agent.UpdatedAt.Equal(result.CommittedAt) {
		return nil, ErrInvalidPersistedState
	}
	return cloneLifecycleResult(result), nil
}

func cloneLifecycleResult(result *LifecycleResult) *LifecycleResult {
	if result == nil {
		return nil
	}
	out := *result
	out.Agent = cloneAgent(result.Agent)
	out.BindingIDs = append([]string(nil), result.BindingIDs...)
	out.GrantIDs = append([]string(nil), result.GrantIDs...)
	return &out
}

func (s *Service) AcquireOwnership(
	ctx context.Context,
	auth authority.SystemAuthority,
	command AcquireOwnershipCommand,
) (*OwnershipGrant, error) {
	command, err := normalizeAcquireOwnershipCommand(command)
	if err != nil {
		return nil, err
	}
	if err := s.requireSystem(ActionAcquireOwnership, command.WorkspaceKey, auth); err != nil {
		return nil, err
	}
	grant, err := s.ownership.AcquireOwnership(ctx, AcquireOwnershipMutation{
		WorkspaceKey: command.WorkspaceKey, AgentID: command.AgentID, LeaseID: command.LeaseID,
		OwnerID: auth.Subject(), RuntimeProvider: command.RuntimeProvider,
		NodeID: command.NodeID, TTL: command.TTL,
	})
	if err != nil {
		return nil, fmt.Errorf("acquire ownership for agent %q: %w", command.AgentID, err)
	}
	if err := validateGrant(grant, command, auth.Subject()); err != nil {
		return nil, err
	}
	return cloneGrant(grant), nil
}

func (s *Service) GetOwnership(ctx context.Context, workspace, agentID string) (*AgentOwnershipLease, error) {
	workspace, agentID, err := normalizeWorkspaceAndAgent(workspace, agentID)
	if err != nil {
		return nil, err
	}
	if s == nil || s.ownership == nil {
		return nil, ErrUnavailable
	}
	lease, err := s.ownership.GetOwnership(ctx, workspace, agentID)
	if err != nil {
		return nil, fmt.Errorf("get ownership for agent %q: %w", agentID, err)
	}
	if err := validatePersistedLease(lease, workspace, agentID); err != nil {
		return nil, err
	}
	return cloneLease(lease), nil
}

func (s *Service) ListOwnership(
	ctx context.Context,
	workspace string,
	filter OwnershipFilter,
) ([]*AgentOwnershipLease, error) {
	workspace, err := normalizeWorkspace(workspace)
	if err != nil {
		return nil, err
	}
	filter, err = normalizeOwnershipFilter(filter)
	if err != nil {
		return nil, err
	}
	if s == nil || s.ownership == nil {
		return nil, ErrUnavailable
	}
	values, err := s.ownership.ListOwnership(ctx, workspace, filter)
	if err != nil {
		return nil, fmt.Errorf("list agent ownership: %w", err)
	}
	out := make([]*AgentOwnershipLease, 0, len(values))
	for _, lease := range values {
		if err := validatePersistedLease(lease, workspace, ""); err != nil {
			return nil, err
		}
		if filter.OwnerID != "" && lease.OwnerID != filter.OwnerID ||
			filter.RuntimeProvider != "" && lease.RuntimeProvider != filter.RuntimeProvider ||
			filter.NodeID != "" && lease.NodeID != filter.NodeID ||
			filter.Status != "" && lease.Status != filter.Status {
			return nil, ErrInvalidPersistedState
		}
		out = append(out, cloneLease(lease))
	}
	return out, nil
}

func (s *Service) RenewOwnership(
	ctx context.Context,
	auth authority.SystemAuthority,
	ownership OwnershipProof,
	ttl time.Duration,
) (*AgentOwnershipLease, error) {
	ownership, err := normalizeOwnershipProof(ownership)
	if err != nil {
		return nil, err
	}
	if ttl <= 0 {
		return nil, fmt.Errorf("ownership ttl must be positive: %w", ErrInvalid)
	}
	if err := s.requireSystemOwner(ActionRenewOwnership, ownership, auth); err != nil {
		return nil, err
	}
	lease, err := s.ownership.RenewOwnership(ctx, RenewOwnershipMutation{Ownership: ownership, TTL: ttl})
	if err != nil {
		return nil, fmt.Errorf("renew ownership for agent %q: %w", ownership.AgentID, err)
	}
	if err := validateLeaseForProof(lease, ownership, OwnershipActive); err != nil {
		return nil, err
	}
	return cloneLease(lease), nil
}

func (s *Service) ReleaseOwnership(
	ctx context.Context,
	auth authority.SystemAuthority,
	ownership OwnershipProof,
) (*AgentOwnershipLease, error) {
	ownership, err := normalizeOwnershipProof(ownership)
	if err != nil {
		return nil, err
	}
	if err := s.requireSystemOwner(ActionReleaseOwnership, ownership, auth); err != nil {
		return nil, err
	}
	lease, err := s.ownership.ReleaseOwnership(ctx, ownership)
	if err != nil {
		return nil, fmt.Errorf("release ownership for agent %q: %w", ownership.AgentID, err)
	}
	if err := validateLeaseForProof(lease, ownership, OwnershipReleased); err != nil {
		return nil, err
	}
	return cloneLease(lease), nil
}

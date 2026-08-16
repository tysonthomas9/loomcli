// Package fleetdb adapts a process-wide FleetDB transport to Agents-owned
// ports. It owns wire mapping only: capability policy and response validation
// remain in agents, while composition supplies the shared authenticated
// transport.
package fleetdb

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/agents"
)

var (
	ErrTransportNotFound          = errors.New("agents fleetdb transport: not found")
	ErrTransportInvalid           = errors.New("agents fleetdb transport: invalid request")
	ErrTransportAlreadyExists     = errors.New("agents fleetdb transport: already exists")
	ErrTransportConflict          = errors.New("agents fleetdb transport: conflict")
	ErrTransportNotOwner          = errors.New("agents fleetdb transport: not owner")
	ErrTransportInvalidTransition = errors.New("agents fleetdb transport: invalid transition")
	ErrTransportUnavailable       = errors.New("agents fleetdb transport: unavailable")
)

// AgentServiceWire mirrors FleetDB's existing AgentService resource. Fields
// outside the canonical Agents model remain present so composition can map the
// existing client without treating them as Agents-owned policy.
type AgentServiceWire struct {
	WorkspaceKey    string            `json:"workspace_key"`
	ServiceID       string            `json:"service_id"`
	GenerationID    string            `json:"generation_id"`
	Name            string            `json:"name"`
	Kind            string            `json:"kind"`
	DesiredState    string            `json:"desired_state"`
	RoleName        string            `json:"role_name,omitempty"`
	DriverID        string            `json:"driver_id,omitempty"`
	DriverVersionID string            `json:"driver_version_id,omitempty"`
	ProfileName     string            `json:"profile_name,omitempty"`
	ScheduleID      string            `json:"schedule_id,omitempty"`
	EventSources    []string          `json:"event_sources,omitempty"`
	TriggerRefs     []string          `json:"trigger_refs,omitempty"`
	PlacementPolicy string            `json:"placement_policy,omitempty"`
	MaxInstances    int               `json:"max_instances"`
	LeaseID         string            `json:"lease_id,omitempty"`
	RestartPolicy   string            `json:"restart_policy,omitempty"`
	Permissions     []string          `json:"permissions,omitempty"`
	BudgetPolicy    string            `json:"budget_policy,omitempty"`
	StateRef        string            `json:"state_ref,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty"`
	CreatedBy       string            `json:"created_by,omitempty"`
	DeletedAt       *time.Time        `json:"deleted_at,omitempty"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
}

type RoleReferenceWire struct {
	WorkspaceKey string `json:"workspace_key"`
	Name         string `json:"name"`
}

type RoleWire struct {
	WorkspaceKey   string    `json:"workspace_key"`
	Name           string    `json:"name"`
	Kind           string    `json:"kind,omitempty"`
	Description    string    `json:"description,omitempty"`
	Prompt         string    `json:"prompt,omitempty"`
	PromptFile     string    `json:"prompt_file,omitempty"`
	Model          string    `json:"model,omitempty"`
	TaskFilter     string    `json:"task_filter,omitempty"`
	Backend        string    `json:"backend,omitempty"`
	Effort         string    `json:"effort,omitempty"`
	PathPatterns   []string  `json:"path_patterns,omitempty"`
	Skills         []string  `json:"skills,omitempty"`
	MaxPriority    *int      `json:"max_priority,omitempty"`
	MaxConcurrency *int      `json:"max_concurrency,omitempty"`
	ReadOnly       bool      `json:"read_only,omitempty"`
	AllowedTools   []string  `json:"allowed_tools,omitempty"`
	DeniedTools    []string  `json:"denied_tools,omitempty"`
	MaxBudgetUSD   *float64  `json:"max_budget_usd,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type RolePatchWire struct {
	Kind           *string
	Description    *string
	Prompt         *string
	PromptFile     *string
	Model          *string
	TaskFilter     *string
	Backend        *string
	Effort         *string
	PathPatterns   *[]string
	Skills         *[]string
	MaxPriority    **int
	MaxConcurrency **int
	ReadOnly       *bool
	AllowedTools   *[]string
	DeniedTools    *[]string
	MaxBudgetUSD   **float64
}

type UpdateRoleWire struct {
	WorkspaceKey      string
	RoleName          string
	ExpectedUpdatedAt time.Time
	Patch             RolePatchWire
	UpdatedBy         string
}

type DeleteRoleWire struct {
	WorkspaceKey      string
	RoleName          string
	ExpectedUpdatedAt time.Time
	DeletedBy         string
}

type AgentServiceFilterWire struct {
	Kind           string
	DesiredState   string
	RoleName       string
	IncludeDeleted bool
	Limit          int
}

type CreateAgentServiceWire struct {
	WorkspaceKey    string
	ServiceID       string
	Name            string
	Kind            string
	DesiredState    string
	RoleName        string
	DriverID        string
	DriverVersionID string
	ProfileName     string
	ScheduleID      string
	EventSources    []string
	TriggerRefs     []string
	PlacementPolicy string
	MaxInstances    int
	LeaseID         string
	RestartPolicy   string
	Permissions     []string
	BudgetPolicy    string
	StateRef        string
	Metadata        map[string]string
	// CreatedBy is server-derived by the Agents authority boundary. A
	// transport maps it to FleetDB's trusted actor channel, never to a
	// caller-controlled created_by request field.
	CreatedBy string
}

type AgentServicePatchWire struct {
	Name            *string
	Kind            *string
	RoleName        *string
	DriverID        *string
	DriverVersionID *string
	ProfileName     *string
	ScheduleID      *string
	EventSources    *[]string
	TriggerRefs     *[]string
	PlacementPolicy *string
	MaxInstances    *int
	LeaseID         *string
	RestartPolicy   *string
	Permissions     *[]string
	BudgetPolicy    *string
	StateRef        *string
	Metadata        *map[string]string
}

type UpdateAgentServiceWire struct {
	WorkspaceKey      string
	ServiceID         string
	ExpectedUpdatedAt time.Time
	Patch             AgentServicePatchWire
	// UpdatedBy is a trusted audit actor mapped to FleetDB's delegated actor
	// header; it is never serialized as caller-controlled request data.
	UpdatedBy string
}

type ArchiveAgentServiceWire struct {
	WorkspaceKey      string
	ServiceID         string
	ExpectedUpdatedAt time.Time
	// ArchivedBy is a trusted audit actor, not request-body authority.
	ArchivedBy string
}

type DesiredStateWire struct {
	WorkspaceKey      string
	ServiceID         string
	ExpectedState     string
	DesiredState      string
	ExpectedUpdatedAt time.Time
	// ChangedBy is a trusted audit actor, not request-body authority.
	ChangedBy string
}

// OwnedDesiredStateWire is one indivisible FleetDB command envelope. A
// Transport implementation must validate the full current ownership
// generation and Agent revision in the same durable operation as the desired
// state transition. Implementations must not emulate it with Get + PATCH.
type OwnedDesiredStateWire struct {
	WorkspaceKey      string
	ServiceID         string
	LeaseID           string
	LeaseToken        string `json:"-"`
	OwnerID           string
	RuntimeProvider   string
	NodeID            string
	FencingToken      int64
	ExpectedState     string
	DesiredState      string
	ExpectedUpdatedAt time.Time
	IdempotencyKey    string
}

// AgentOwnershipLeaseWire mirrors FleetDB's lease response. Token is consumed
// into OwnershipGrant only on Acquire and stripped from every lease
// projection returned by this adapter.
type AgentOwnershipLeaseWire struct {
	WorkspaceKey    string    `json:"workspace_key"`
	AgentID         string    `json:"agent_id"`
	LeaseID         string    `json:"lease_id"`
	OwnerID         string    `json:"owner_id"`
	RuntimeProvider string    `json:"runtime_provider,omitempty"`
	NodeID          string    `json:"node_id,omitempty"`
	Token           string    `json:"token,omitempty"`
	FencingToken    int64     `json:"fencing_token"`
	Status          string    `json:"status"`
	ExpiresAt       time.Time `json:"expires_at"`
	LastHeartbeat   time.Time `json:"last_heartbeat"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type OwnershipFilterWire struct {
	OwnerID         string
	RuntimeProvider string
	NodeID          string
	Status          string
	Limit           int
}

type AcquireOwnershipWire struct {
	WorkspaceKey    string
	AgentID         string
	LeaseID         string
	OwnerID         string
	RuntimeProvider string
	NodeID          string
	TTLSeconds      int
}

type OwnershipProofWire struct {
	WorkspaceKey    string
	AgentID         string
	LeaseID         string
	LeaseToken      string `json:"-"`
	OwnerID         string
	RuntimeProvider string
	NodeID          string
	FencingToken    int64
}

type RenewOwnershipWire struct {
	Ownership  OwnershipProofWire
	TTLSeconds int
}

type LifecycleWire struct {
	WorkspaceKey         string
	ServiceID            string
	Action               string
	ExpectedUpdatedAt    time.Time
	ExpectedGenerationID string
	IdempotencyKey       string
	ChangedBy            string
}

type LifecycleResultWire struct {
	WorkspaceKey   string
	ServiceID      string
	IdempotencyKey string
	Action         string
	Agent          *AgentServiceWire
	BindingIDs     []string
	GrantIDs       []string
	CommittedAt    time.Time
}

type ManagedReviewerPresetWire struct {
	PresetID    string                                `json:"preset_id"`
	Revision    int64                                 `json:"revision"`
	Fingerprint string                                `json:"fingerprint"`
	Role        agents.ManagedReviewerRoleDefinition  `json:"role"`
	Agent       agents.ManagedReviewerAgentDefinition `json:"agent"`
}

type ManagedReviewerWire struct {
	WorkspaceKey string
	AgentID      string
	DesiredState string
	Preset       ManagedReviewerPresetWire
	ActorID      string
}

type ManagedReviewerResultWire struct {
	PresetID          string
	PresetRevision    int64
	PresetFingerprint string
	Role              *RoleWire
	Agent             *AgentServiceWire
	Changed           bool
}

// LifecycleTransport is intentionally separate from the still-migrating
// baseline transport. Production composition requires it through
// agents.NewWithLifecycle; older fakes cannot accidentally trigger a
// sequential fallback.
type LifecycleTransport interface {
	ApplyAgentServiceLifecycle(context.Context, LifecycleWire) (*LifecycleResultWire, error)
}

// Transport is implemented by composition over the one process-wide FleetDB
// client. Every method represents one FleetDB request or atomic service
// command. In particular SetAgentServiceDesiredStateOwned must never be
// decomposed into generic AgentService and ownership-lease calls.
type Transport interface {
	GetAgentService(context.Context, string, string) (*AgentServiceWire, error)
	ListAgentServices(context.Context, string, AgentServiceFilterWire) ([]*AgentServiceWire, error)
	GetRoleReference(context.Context, string, string) (*RoleReferenceWire, error)
	GetRole(context.Context, string, string) (*RoleWire, error)
	ListRoles(context.Context, string) ([]*RoleWire, error)
	CreateRole(context.Context, string, agents.RoleDefinition) (*RoleWire, error)
	UpdateRole(context.Context, UpdateRoleWire) (*RoleWire, error)
	DeleteRole(context.Context, DeleteRoleWire) error
	CreateAgentService(context.Context, CreateAgentServiceWire) (*AgentServiceWire, error)
	UpdateAgentService(context.Context, UpdateAgentServiceWire) (*AgentServiceWire, error)
	ArchiveAgentService(context.Context, ArchiveAgentServiceWire) (*AgentServiceWire, error)
	SetAgentServiceDesiredState(context.Context, DesiredStateWire) (*AgentServiceWire, error)
	SetAgentServiceDesiredStateOwned(context.Context, OwnedDesiredStateWire) (*AgentServiceWire, error)
	ConvergeManagedReviewer(context.Context, ManagedReviewerWire) (*ManagedReviewerResultWire, error)

	AcquireAgentOwnership(context.Context, AcquireOwnershipWire) (*AgentOwnershipLeaseWire, error)
	GetAgentOwnership(context.Context, string, string) (*AgentOwnershipLeaseWire, error)
	ListAgentOwnership(context.Context, string, OwnershipFilterWire) ([]*AgentOwnershipLeaseWire, error)
	RenewAgentOwnership(context.Context, RenewOwnershipWire) (*AgentOwnershipLeaseWire, error)
	ReleaseAgentOwnership(context.Context, OwnershipProofWire) (*AgentOwnershipLeaseWire, error)
}

type Adapter struct {
	transport Transport
}

var (
	_ agents.AgentReader          = (*Adapter)(nil)
	_ agents.RoleReferenceReader  = (*Adapter)(nil)
	_ agents.RoleStore            = (*Adapter)(nil)
	_ agents.AgentIdentityStore   = (*Adapter)(nil)
	_ agents.DesiredStateStore    = (*Adapter)(nil)
	_ agents.OwnershipStore       = (*Adapter)(nil)
	_ agents.LifecycleStore       = (*Adapter)(nil)
	_ agents.ManagedReviewerStore = (*Adapter)(nil)
)

func New(transport Transport) (*Adapter, error) {
	if transport == nil {
		return nil, fmt.Errorf("agents fleetdb adapter: nil transport: %w", agents.ErrUnavailable)
	}
	return &Adapter{transport: transport}, nil
}

func (adapter *Adapter) ApplyLifecycle(
	ctx context.Context,
	mutation agents.ApplyLifecycleMutation,
) (*agents.LifecycleResult, error) {
	transport, ok := adapter.transport.(LifecycleTransport)
	if !ok || transport == nil {
		return nil, agents.ErrUnavailable
	}
	value, err := transport.ApplyAgentServiceLifecycle(ctx, LifecycleWire{
		WorkspaceKey: mutation.WorkspaceKey, ServiceID: mutation.AgentID,
		Action: string(mutation.Action), ExpectedUpdatedAt: mutation.ExpectedUpdatedAt,
		ExpectedGenerationID: mutation.ExpectedGenerationID,
		IdempotencyKey:       mutation.IdempotencyKey, ChangedBy: mutation.ChangedBy,
	})
	if err != nil {
		return nil, mapError("apply agent lifecycle", err)
	}
	if value == nil {
		return nil, nil
	}
	return &agents.LifecycleResult{
		WorkspaceKey: value.WorkspaceKey, AgentID: value.ServiceID,
		IdempotencyKey: value.IdempotencyKey, Action: agents.LifecycleAction(value.Action),
		Agent: agentFromWire(value.Agent), BindingIDs: append([]string(nil), value.BindingIDs...),
		GrantIDs: append([]string(nil), value.GrantIDs...), CommittedAt: value.CommittedAt,
	}, nil
}

func (adapter *Adapter) ConvergeManagedReviewer(
	ctx context.Context,
	mutation agents.ManagedReviewerMutation,
) (*agents.ManagedReviewerResult, error) {
	value, err := adapter.transport.ConvergeManagedReviewer(ctx, ManagedReviewerWire{
		WorkspaceKey: mutation.WorkspaceKey, AgentID: mutation.AgentID,
		DesiredState: string(mutation.DesiredState), ActorID: mutation.ActorID,
		Preset: ManagedReviewerPresetWire{
			PresetID: mutation.Preset.PresetID, Revision: mutation.Preset.Revision,
			Fingerprint: mutation.Fingerprint, Role: mutation.Preset.Role, Agent: mutation.Preset.Agent,
		},
	})
	if err != nil {
		return nil, mapError("converge managed reviewer", err)
	}
	if value == nil {
		return nil, nil
	}
	return &agents.ManagedReviewerResult{
		PresetID: value.PresetID, PresetRevision: value.PresetRevision,
		PresetFingerprint: value.PresetFingerprint, Role: roleFromWire(value.Role),
		Agent: agentFromWire(value.Agent), Changed: value.Changed,
	}, nil
}

func (adapter *Adapter) GetAgent(ctx context.Context, workspace, agentID string) (*agents.Agent, error) {
	value, err := adapter.transport.GetAgentService(ctx, workspace, agentID)
	return agentFromWire(value), mapError("get agent", err)
}

func (adapter *Adapter) ListAgents(ctx context.Context, workspace string, filter agents.AgentFilter) ([]*agents.Agent, error) {
	values, err := adapter.transport.ListAgentServices(ctx, workspace, AgentServiceFilterWire{
		Kind: string(filter.Kind), DesiredState: string(filter.DesiredState),
		RoleName: filter.RoleName, IncludeDeleted: filter.IncludeDeleted, Limit: filter.Limit,
	})
	if err != nil {
		return nil, mapError("list agents", err)
	}
	out := make([]*agents.Agent, 0, len(values))
	for _, value := range values {
		out = append(out, agentFromWire(value))
	}
	return out, nil
}

func (adapter *Adapter) GetRoleReference(ctx context.Context, workspace, roleName string) (*agents.RoleReference, error) {
	value, err := adapter.transport.GetRoleReference(ctx, workspace, roleName)
	if err != nil {
		return nil, mapError("get role reference", err)
	}
	if value == nil {
		return nil, nil
	}
	return &agents.RoleReference{WorkspaceKey: value.WorkspaceKey, RoleName: value.Name}, nil
}

func (adapter *Adapter) GetRole(ctx context.Context, workspace, roleName string) (*agents.Role, error) {
	value, err := adapter.transport.GetRole(ctx, workspace, roleName)
	return roleFromWire(value), mapError("get role", err)
}

func (adapter *Adapter) ListRoles(ctx context.Context, workspace string) ([]*agents.Role, error) {
	values, err := adapter.transport.ListRoles(ctx, workspace)
	if err != nil {
		return nil, mapError("list roles", err)
	}
	out := make([]*agents.Role, 0, len(values))
	for _, value := range values {
		out = append(out, roleFromWire(value))
	}
	return out, nil
}

func (adapter *Adapter) CreateRole(
	ctx context.Context,
	workspace string,
	role agents.RoleDefinition,
) (*agents.Role, error) {
	value, err := adapter.transport.CreateRole(ctx, workspace, cloneRoleDefinition(role))
	return roleFromWire(value), mapError("create role", err)
}

func (adapter *Adapter) UpdateRole(
	ctx context.Context,
	mutation agents.UpdateRoleMutation,
) (*agents.Role, error) {
	value, err := adapter.transport.UpdateRole(ctx, UpdateRoleWire{
		WorkspaceKey: mutation.WorkspaceKey, RoleName: mutation.RoleName,
		ExpectedUpdatedAt: mutation.ExpectedUpdatedAt,
		Patch:             rolePatchToWire(mutation.Patch),
		UpdatedBy:         mutation.UpdatedBy,
	})
	return roleFromWire(value), mapError("update role", err)
}

func (adapter *Adapter) DeleteRole(ctx context.Context, mutation agents.DeleteRoleMutation) error {
	return mapError("delete role", adapter.transport.DeleteRole(ctx, DeleteRoleWire{
		WorkspaceKey: mutation.WorkspaceKey, RoleName: mutation.RoleName,
		ExpectedUpdatedAt: mutation.ExpectedUpdatedAt, DeletedBy: mutation.DeletedBy,
	}))
}

func (adapter *Adapter) CreateAgent(ctx context.Context, mutation agents.CreateAgentMutation) (*agents.Agent, error) {
	value, err := adapter.transport.CreateAgentService(ctx, CreateAgentServiceWire{
		WorkspaceKey: mutation.WorkspaceKey, ServiceID: mutation.AgentID, Name: mutation.Name,
		Kind: string(mutation.Kind), DesiredState: string(mutation.DesiredState),
		RoleName: mutation.Behavior.RoleName, DriverID: mutation.Behavior.DriverID,
		DriverVersionID: mutation.Behavior.DriverVersionID,
		ProfileName:     mutation.ProfileName, ScheduleID: mutation.ScheduleID,
		EventSources:    append([]string(nil), mutation.EventSources...),
		TriggerRefs:     append([]string(nil), mutation.TriggerRefs...),
		PlacementPolicy: mutation.PlacementPolicy, MaxInstances: mutation.MaxInstances,
		LeaseID: mutation.LeaseID, RestartPolicy: mutation.RestartPolicy,
		Permissions:  append([]string(nil), mutation.Permissions...),
		BudgetPolicy: mutation.BudgetPolicy, StateRef: mutation.StateRef,
		Metadata: cloneMap(mutation.Metadata), CreatedBy: mutation.CreatedBy,
	})
	return agentFromWire(value), mapError("create agent", err)
}

func (adapter *Adapter) UpdateAgent(ctx context.Context, mutation agents.UpdateAgentMutation) (*agents.Agent, error) {
	value, err := adapter.transport.UpdateAgentService(ctx, UpdateAgentServiceWire{
		WorkspaceKey: mutation.WorkspaceKey, ServiceID: mutation.AgentID,
		ExpectedUpdatedAt: mutation.ExpectedUpdatedAt, Patch: patchToWire(mutation.Patch),
		UpdatedBy: mutation.UpdatedBy,
	})
	return agentFromWire(value), mapError("update agent", err)
}

func (adapter *Adapter) ArchiveAgent(ctx context.Context, mutation agents.ArchiveAgentMutation) (*agents.Agent, error) {
	value, err := adapter.transport.ArchiveAgentService(ctx, ArchiveAgentServiceWire{
		WorkspaceKey: mutation.WorkspaceKey, ServiceID: mutation.AgentID,
		ExpectedUpdatedAt: mutation.ExpectedUpdatedAt, ArchivedBy: mutation.ArchivedBy,
	})
	return agentFromWire(value), mapError("archive agent", err)
}

func (adapter *Adapter) SetDesiredState(ctx context.Context, mutation agents.DesiredStateMutation) (*agents.Agent, error) {
	value, err := adapter.transport.SetAgentServiceDesiredState(ctx, DesiredStateWire{
		WorkspaceKey: mutation.WorkspaceKey, ServiceID: mutation.AgentID,
		ExpectedState: string(mutation.ExpectedState), DesiredState: string(mutation.DesiredState),
		ExpectedUpdatedAt: mutation.ExpectedUpdatedAt, ChangedBy: mutation.ChangedBy,
	})
	return agentFromWire(value), mapError("set desired state", err)
}

func (adapter *Adapter) SetDesiredStateOwned(ctx context.Context, mutation agents.OwnedDesiredStateMutation) (*agents.Agent, error) {
	proof := ownershipProofToWire(mutation.Ownership)
	value, err := adapter.transport.SetAgentServiceDesiredStateOwned(ctx, OwnedDesiredStateWire{
		WorkspaceKey: proof.WorkspaceKey, ServiceID: proof.AgentID, LeaseID: proof.LeaseID,
		LeaseToken: proof.LeaseToken, OwnerID: proof.OwnerID,
		RuntimeProvider: proof.RuntimeProvider, NodeID: proof.NodeID, FencingToken: proof.FencingToken,
		ExpectedState: string(mutation.ExpectedState), DesiredState: string(mutation.DesiredState),
		ExpectedUpdatedAt: mutation.ExpectedUpdatedAt, IdempotencyKey: mutation.IdempotencyKey,
	})
	return agentFromWire(value), mapError("set owner-fenced desired state", err)
}

func (adapter *Adapter) AcquireOwnership(ctx context.Context, mutation agents.AcquireOwnershipMutation) (*agents.OwnershipGrant, error) {
	value, err := adapter.transport.AcquireAgentOwnership(ctx, AcquireOwnershipWire{
		WorkspaceKey: mutation.WorkspaceKey, AgentID: mutation.AgentID, LeaseID: mutation.LeaseID,
		OwnerID: mutation.OwnerID, RuntimeProvider: string(mutation.RuntimeProvider),
		NodeID: mutation.NodeID, TTLSeconds: ttlSeconds(mutation.TTL),
	})
	if err != nil {
		return nil, mapError("acquire agent ownership", err)
	}
	if value == nil {
		return nil, nil
	}
	return &agents.OwnershipGrant{Lease: leaseFromWire(value), LeaseToken: value.Token}, nil
}

func (adapter *Adapter) GetOwnership(ctx context.Context, workspace, agentID string) (*agents.AgentOwnershipLease, error) {
	value, err := adapter.transport.GetAgentOwnership(ctx, workspace, agentID)
	return leaseFromWire(value), mapError("get agent ownership", err)
}

func (adapter *Adapter) ListOwnership(ctx context.Context, workspace string, filter agents.OwnershipFilter) ([]*agents.AgentOwnershipLease, error) {
	values, err := adapter.transport.ListAgentOwnership(ctx, workspace, OwnershipFilterWire{
		OwnerID: filter.OwnerID, RuntimeProvider: string(filter.RuntimeProvider),
		NodeID: filter.NodeID, Status: string(filter.Status), Limit: filter.Limit,
	})
	if err != nil {
		return nil, mapError("list agent ownership", err)
	}
	out := make([]*agents.AgentOwnershipLease, 0, len(values))
	for _, value := range values {
		out = append(out, leaseFromWire(value))
	}
	return out, nil
}

func (adapter *Adapter) RenewOwnership(ctx context.Context, mutation agents.RenewOwnershipMutation) (*agents.AgentOwnershipLease, error) {
	value, err := adapter.transport.RenewAgentOwnership(ctx, RenewOwnershipWire{
		Ownership: ownershipProofToWire(mutation.Ownership), TTLSeconds: ttlSeconds(mutation.TTL),
	})
	return leaseFromWire(value), mapError("renew agent ownership", err)
}

func (adapter *Adapter) ReleaseOwnership(ctx context.Context, proof agents.OwnershipProof) (*agents.AgentOwnershipLease, error) {
	value, err := adapter.transport.ReleaseAgentOwnership(ctx, ownershipProofToWire(proof))
	return leaseFromWire(value), mapError("release agent ownership", err)
}

func agentFromWire(value *AgentServiceWire) *agents.Agent {
	if value == nil {
		return nil
	}
	return &agents.Agent{
		WorkspaceKey: value.WorkspaceKey, AgentID: value.ServiceID,
		GenerationID: value.GenerationID, Name: value.Name,
		Kind: agents.AgentKind(value.Kind),
		Behavior: agents.BehaviorReference{
			RoleName: value.RoleName, DriverID: value.DriverID, DriverVersionID: value.DriverVersionID,
		},
		DesiredState: agents.DesiredState(value.DesiredState),
		ProfileName:  value.ProfileName, ScheduleID: value.ScheduleID,
		EventSources:    append([]string(nil), value.EventSources...),
		TriggerRefs:     append([]string(nil), value.TriggerRefs...),
		PlacementPolicy: value.PlacementPolicy, MaxInstances: value.MaxInstances,
		LeaseID: value.LeaseID, RestartPolicy: value.RestartPolicy,
		Permissions:  append([]string(nil), value.Permissions...),
		BudgetPolicy: value.BudgetPolicy, StateRef: value.StateRef,
		Metadata: cloneMap(value.Metadata), CreatedBy: value.CreatedBy, DeletedAt: cloneTime(value.DeletedAt),
		CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}

func patchToWire(patch agents.AgentPatch) AgentServicePatchWire {
	out := AgentServicePatchWire{
		Name: cloneString(patch.Name), PlacementPolicy: cloneString(patch.PlacementPolicy),
		MaxInstances: cloneInt(patch.MaxInstances), RestartPolicy: cloneString(patch.RestartPolicy),
		BudgetPolicy: cloneString(patch.BudgetPolicy), ProfileName: cloneString(patch.ProfileName),
		ScheduleID: cloneString(patch.ScheduleID), EventSources: cloneStringSlicePointer(patch.EventSources),
		TriggerRefs: cloneStringSlicePointer(patch.TriggerRefs), LeaseID: cloneString(patch.LeaseID),
		Permissions: cloneStringSlicePointer(patch.Permissions), StateRef: cloneString(patch.StateRef),
	}
	if patch.Metadata != nil {
		value := cloneMap(*patch.Metadata)
		out.Metadata = &value
	}
	if patch.Kind != nil {
		out.Kind = stringPointer(string(*patch.Kind))
	}
	if patch.Behavior != nil {
		out.RoleName = stringPointer(patch.Behavior.RoleName)
		out.DriverID = stringPointer(patch.Behavior.DriverID)
		out.DriverVersionID = stringPointer(patch.Behavior.DriverVersionID)
	}
	return out
}

func rolePatchToWire(patch agents.RolePatch) RolePatchWire {
	return RolePatchWire{
		Kind:           cloneString(patch.Kind),
		Description:    cloneString(patch.Description),
		Prompt:         cloneString(patch.Prompt),
		PromptFile:     cloneString(patch.PromptFile),
		Model:          cloneString(patch.Model),
		TaskFilter:     cloneString(patch.TaskFilter),
		Backend:        cloneString(patch.Backend),
		Effort:         cloneString(patch.Effort),
		PathPatterns:   cloneStringSlicePointer(patch.PathPatterns),
		Skills:         cloneStringSlicePointer(patch.Skills),
		MaxPriority:    cloneOptionalIntPointer(patch.MaxPriority),
		MaxConcurrency: cloneOptionalIntPointer(patch.MaxConcurrency),
		ReadOnly:       cloneBool(patch.ReadOnly),
		AllowedTools:   cloneStringSlicePointer(patch.AllowedTools),
		DeniedTools:    cloneStringSlicePointer(patch.DeniedTools),
		MaxBudgetUSD:   cloneOptionalFloat64Pointer(patch.MaxBudgetUSD),
	}
}

func roleFromWire(value *RoleWire) *agents.Role {
	if value == nil {
		return nil
	}
	return &agents.Role{
		WorkspaceKey: value.WorkspaceKey, Name: value.Name, Kind: value.Kind,
		Description: value.Description, Prompt: value.Prompt, PromptFile: value.PromptFile,
		Model: value.Model, TaskFilter: value.TaskFilter, Backend: value.Backend, Effort: value.Effort,
		PathPatterns: append([]string(nil), value.PathPatterns...),
		Skills:       append([]string(nil), value.Skills...),
		MaxPriority:  cloneInt(value.MaxPriority), MaxConcurrency: cloneInt(value.MaxConcurrency),
		ReadOnly: value.ReadOnly, AllowedTools: append([]string(nil), value.AllowedTools...),
		DeniedTools:  append([]string(nil), value.DeniedTools...),
		MaxBudgetUSD: cloneFloat64(value.MaxBudgetUSD),
		CreatedAt:    value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}

func cloneRoleDefinition(value agents.RoleDefinition) agents.RoleDefinition {
	value.PathPatterns = append([]string(nil), value.PathPatterns...)
	value.Skills = append([]string(nil), value.Skills...)
	value.MaxPriority = cloneInt(value.MaxPriority)
	value.MaxConcurrency = cloneInt(value.MaxConcurrency)
	value.AllowedTools = append([]string(nil), value.AllowedTools...)
	value.DeniedTools = append([]string(nil), value.DeniedTools...)
	value.MaxBudgetUSD = cloneFloat64(value.MaxBudgetUSD)
	return value
}

func cloneMap(value map[string]string) map[string]string {
	if value == nil {
		return nil
	}
	out := make(map[string]string, len(value))
	for key, item := range value {
		out[key] = item
	}
	return out
}

func cloneFloat64(value *float64) *float64 {
	if value == nil {
		return nil
	}
	out := *value
	return &out
}

func leaseFromWire(value *AgentOwnershipLeaseWire) *agents.AgentOwnershipLease {
	if value == nil {
		return nil
	}
	return &agents.AgentOwnershipLease{
		WorkspaceKey: value.WorkspaceKey, AgentID: value.AgentID, LeaseID: value.LeaseID,
		OwnerID: value.OwnerID, RuntimeProvider: agents.RuntimeProvider(value.RuntimeProvider),
		NodeID: value.NodeID, FencingToken: value.FencingToken,
		Status: agents.OwnershipStatus(value.Status), ExpiresAt: value.ExpiresAt,
		LastHeartbeat: value.LastHeartbeat, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}

func ownershipProofToWire(proof agents.OwnershipProof) OwnershipProofWire {
	return OwnershipProofWire{
		WorkspaceKey: proof.WorkspaceKey, AgentID: proof.AgentID, LeaseID: proof.LeaseID,
		LeaseToken: proof.LeaseToken, OwnerID: proof.OwnerID,
		RuntimeProvider: string(proof.RuntimeProvider), NodeID: proof.NodeID,
		FencingToken: proof.FencingToken,
	}
}

func ttlSeconds(ttl time.Duration) int {
	if ttl <= 0 {
		return 0
	}
	seconds := ttl / time.Second
	if ttl%time.Second != 0 {
		seconds++
	}
	return int(seconds)
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	out := *value
	return &out
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	return stringPointer(*value)
}

func cloneStringSlicePointer(value *[]string) *[]string {
	if value == nil {
		return nil
	}
	out := append([]string(nil), (*value)...)
	return &out
}

func cloneBool(value *bool) *bool {
	if value == nil {
		return nil
	}
	out := *value
	return &out
}

func cloneOptionalIntPointer(value **int) **int {
	if value == nil {
		return nil
	}
	out := cloneInt(*value)
	return &out
}

func cloneOptionalFloat64Pointer(value **float64) **float64 {
	if value == nil {
		return nil
	}
	out := cloneFloat64(*value)
	return &out
}

func stringPointer(value string) *string {
	return &value
}

func cloneInt(value *int) *int {
	if value == nil {
		return nil
	}
	out := *value
	return &out
}

func mapError(operation string, err error) error {
	if err == nil {
		return nil
	}
	var mapped error
	switch {
	case errors.Is(err, ErrTransportNotFound):
		mapped = agents.ErrNotFound
	case errors.Is(err, ErrTransportAlreadyExists):
		mapped = agents.ErrAlreadyExists
	case errors.Is(err, ErrTransportConflict):
		mapped = agents.ErrConflict
	case errors.Is(err, ErrTransportNotOwner):
		mapped = agents.ErrNotOwner
	case errors.Is(err, ErrTransportInvalidTransition):
		mapped = agents.ErrInvalidTransition
	case errors.Is(err, ErrTransportInvalid):
		mapped = agents.ErrInvalid
	case errors.Is(err, ErrTransportUnavailable):
		mapped = agents.ErrUnavailable
	default:
		mapped = agents.ErrUnavailable
	}
	return fmt.Errorf("%s: %w", operation, errors.Join(mapped, err))
}

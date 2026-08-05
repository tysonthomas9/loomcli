package agents

import (
	"context"
	"time"
)

// AgentReader is the Agents-owned identity query port.
type AgentReader interface {
	GetAgent(ctx context.Context, workspace, agentID string) (*Agent, error)
	ListAgents(ctx context.Context, workspace string, filter AgentFilter) ([]*Agent, error)
}

// RoleReferenceReader resolves only the durable Role fact required by
// identity commands. The identity store remains responsible for validating
// the same reference atomically with its write; this preflight exists for a
// useful capability-level error and is not a correctness boundary by itself.
type RoleReferenceReader interface {
	GetRoleReference(ctx context.Context, workspace, roleName string) (*RoleReference, error)
}

// RoleStore is the Agents-owned durable Role port used by exact provisioning
// commands. CreateRole must enforce workspace/name uniqueness atomically.
type RoleStore interface {
	GetRole(ctx context.Context, workspace, roleName string) (*Role, error)
	ListRoles(ctx context.Context, workspace string) ([]*Role, error)
	CreateRole(ctx context.Context, workspace string, role RoleDefinition) (*Role, error)
	UpdateRole(ctx context.Context, mutation UpdateRoleMutation) (*Role, error)
	DeleteRole(ctx context.Context, mutation DeleteRoleMutation) error
}

// RolePromptRepairStore is the deliberately narrow persistence primitive used
// by startup compatibility repair. Implementations must atomically fill an
// empty PromptFile, accept an exact replay without writing, and reject a
// different non-empty value as ErrConflict.
type RolePromptRepairStore interface {
	SetPromptFileIfEmpty(
		ctx context.Context,
		workspace, roleName, promptFile string,
	) (*Role, bool, error)
}

// AgentIdentityStore owns identity create, replace, and archive. Its methods
// must enforce workspace scope, immutable AgentID, behavior-reference
// integrity, and optimistic revision checks in the durable write.
type AgentIdentityStore interface {
	CreateAgent(ctx context.Context, mutation CreateAgentMutation) (*Agent, error)
	UpdateAgent(ctx context.Context, mutation UpdateAgentMutation) (*Agent, error)
	ArchiveAgent(ctx context.Context, mutation ArchiveAgentMutation) (*Agent, error)
}

// DesiredStateStore is deliberately separate from generic identity updates.
// The owned method must atomically validate the complete OwnershipProof and
// change desired state in one durable command; a read-then-update sequence is
// not a valid implementation.
type DesiredStateStore interface {
	SetDesiredState(ctx context.Context, mutation DesiredStateMutation) (*Agent, error)
	SetDesiredStateOwned(ctx context.Context, mutation OwnedDesiredStateMutation) (*Agent, error)
}

// LifecycleStore is the one FleetDB atomic command port for cross-capability
// Agent lifecycle convergence. Implementations must not decompose it into
// sequential desired-state, binding, grant, or archive calls.
type LifecycleStore interface {
	ApplyLifecycle(context.Context, ApplyLifecycleMutation) (*LifecycleResult, error)
}

// OwnershipStore is the only persistence port for AgentOwnershipLease.
// Renew and Release must validate every field in OwnershipProof atomically.
type OwnershipStore interface {
	AcquireOwnership(ctx context.Context, mutation AcquireOwnershipMutation) (*OwnershipGrant, error)
	GetOwnership(ctx context.Context, workspace, agentID string) (*AgentOwnershipLease, error)
	ListOwnership(ctx context.Context, workspace string, filter OwnershipFilter) ([]*AgentOwnershipLease, error)
	RenewOwnership(ctx context.Context, mutation RenewOwnershipMutation) (*AgentOwnershipLease, error)
	ReleaseOwnership(ctx context.Context, proof OwnershipProof) (*AgentOwnershipLease, error)
}

type CreateAgentMutation struct {
	WorkspaceKey    string
	AgentID         string
	Name            string
	Kind            AgentKind
	Behavior        BehaviorReference
	DesiredState    DesiredState
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
	CreatedBy       string
}

type UpdateRoleMutation struct {
	WorkspaceKey      string
	RoleName          string
	ExpectedUpdatedAt time.Time
	Patch             RolePatch
	UpdatedBy         string
}

type DeleteRoleMutation struct {
	WorkspaceKey      string
	RoleName          string
	ExpectedUpdatedAt time.Time
	DeletedBy         string
}

type UpdateAgentMutation struct {
	WorkspaceKey      string
	AgentID           string
	ExpectedUpdatedAt time.Time
	Patch             AgentPatch
	UpdatedBy         string
}

type ArchiveAgentMutation struct {
	WorkspaceKey      string
	AgentID           string
	ExpectedUpdatedAt time.Time
	ArchivedBy        string
}

type DesiredStateMutation struct {
	WorkspaceKey      string
	AgentID           string
	ExpectedState     DesiredState
	DesiredState      DesiredState
	ExpectedUpdatedAt time.Time
	ChangedBy         string
}

type ApplyLifecycleMutation struct {
	WorkspaceKey         string
	AgentID              string
	Action               LifecycleAction
	ExpectedUpdatedAt    time.Time
	ExpectedGenerationID string
	IdempotencyKey       string
	ChangedBy            string
}

// OwnedDesiredStateMutation is replay-safe only for the same complete
// ownership generation, idempotency key, expected Agent revision/state, and
// target state. The revision prevents desired-state ABA during a live owner
// generation. A stale token/fence, changed owner, or conflicting key reuse
// must fail closed.
type OwnedDesiredStateMutation struct {
	Ownership         OwnershipProof
	ExpectedState     DesiredState
	DesiredState      DesiredState
	ExpectedUpdatedAt time.Time
	IdempotencyKey    string
}

type AcquireOwnershipMutation struct {
	WorkspaceKey    string
	AgentID         string
	LeaseID         string
	OwnerID         string
	RuntimeProvider RuntimeProvider
	NodeID          string
	TTL             time.Duration
}

type RenewOwnershipMutation struct {
	Ownership OwnershipProof
	TTL       time.Duration
}

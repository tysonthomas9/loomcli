package agents

import (
	"context"
	"time"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

const (
	ActionCreateAgent                 authority.Action = "agents.create-agent"
	ActionUpdateAgent                 authority.Action = "agents.update-agent"
	ActionArchiveAgent                authority.Action = "agents.archive-agent"
	ActionSetDesiredState             authority.Action = "agents.set-desired-state"
	ActionCreateRole                  authority.Action = "agents.create-role"
	ActionUpdateRole                  authority.Action = "agents.update-role"
	ActionDeleteRole                  authority.Action = "agents.delete-role"
	ActionEnsureManagedRole           authority.Action = "agents.ensure-managed-role"
	ActionEnsureManagedAgent          authority.Action = "agents.ensure-managed-agent"
	ActionConvergeManagedReviewer     authority.Action = "agents.converge-managed-reviewer"
	ActionRepairManagedRolePromptFile authority.Action = "agents.repair-managed-role-prompt-file"
	ActionAcquireOwnership            authority.Action = "agents.acquire-ownership"
	ActionRenewOwnership              authority.Action = "agents.renew-ownership"
	ActionReleaseOwnership            authority.Action = "agents.release-ownership"
	ActionSetDesiredStateOwned        authority.Action = "agents.set-desired-state-owned"
	ActionApplyLifecycle              authority.Action = "agents.apply-lifecycle"
	ActionReconcileDesiredState       authority.Action = "agents.reconcile-desired-state"
)

// OperationRules is the complete default-deny registry for Agents commands.
func OperationRules() []authority.OperationRule {
	return []authority.OperationRule{
		authority.OperatorOnly(ActionCreateAgent),
		authority.OperatorOnly(ActionUpdateAgent),
		authority.OperatorOnly(ActionArchiveAgent),
		authority.OperatorOnly(ActionSetDesiredState),
		authority.OperatorOnly(ActionCreateRole),
		authority.OperatorOnly(ActionUpdateRole),
		authority.OperatorOnly(ActionDeleteRole),
		authority.Allow(ActionEnsureManagedRole, authority.ClassSystem),
		authority.Allow(ActionEnsureManagedAgent, authority.ClassSystem),
		authority.Allow(ActionConvergeManagedReviewer, authority.ClassSystem),
		authority.Allow(ActionRepairManagedRolePromptFile, authority.ClassSystem),
		authority.Allow(ActionAcquireOwnership, authority.ClassSystem),
		authority.Allow(ActionRenewOwnership, authority.ClassSystem),
		authority.Allow(ActionReleaseOwnership, authority.ClassSystem),
		authority.Allow(ActionSetDesiredStateOwned, authority.ClassSystem),
		authority.OperatorOnly(ActionApplyLifecycle),
		authority.Allow(ActionReconcileDesiredState, authority.ClassSystem),
	}
}

// API is the Phase 5 public Agents capability root. It exposes durable
// identity, Role/Driver behavior references, desired intent, and ownership
// generations only; session, PTY, Git, trigger, and execution operations live
// behind their respective capabilities.
type API interface {
	ProvisioningCommands
	ManagedReviewerIdentityCommands
	ManagedRoleRepairCommands
	IdentityQueries
	IdentityCommands
	RoleQueries
	RoleCommands
	OwnershipQueries
	OwnershipCommands
	LifecycleCommands
	DesiredStateReconciliationCommands
}

// ManagedReviewerIdentityCommands is the single deep command used by PR
// Review. It atomically converges a versioned shared Role and one
// checkout-specific Agent, or archives only that Agent while preserving the
// Role. There is deliberately no sequential Role/Agent fallback.
type ManagedReviewerIdentityCommands interface {
	ConvergeManagedReviewer(
		context.Context,
		authority.SystemAuthority,
		ManagedReviewerCommand,
	) (*ManagedReviewerResult, error)
}

// LifecycleCommands atomically converge Agent desired state, every attached
// managed binding, and (for deletion) binding-scoped grants plus archival.
// The Fleet receipt makes an exact committed response restart-replayable.
type LifecycleCommands interface {
	ApplyLifecycle(context.Context, authority.OperatorAuthority, ApplyLifecycleCommand) (*LifecycleResult, error)
}

type DesiredStateReconciliationCommands interface {
	ReconcileDesiredState(context.Context, authority.SystemAuthority, ReconcileDesiredStateCommand) (ReconcileDesiredStateResult, error)
}

type ReconcileDesiredStateCommand struct {
	WorkspaceKey      string    `json:"workspace_key"`
	AgentID           string    `json:"agent_id"`
	ExpectedUpdatedAt time.Time `json:"expected_updated_at"`
	GenerationID      string    `json:"generation_id"`
}

type ReconcileDesiredStateResult struct {
	WorkspaceKey string `json:"workspace_key"`
	AgentID      string `json:"agent_id"`
	Converged    bool   `json:"converged"`
	Repaired     bool   `json:"repaired"`
}

type LifecycleAction string

const (
	LifecycleEnable    LifecycleAction = "enable"
	LifecycleDisable   LifecycleAction = "disable"
	LifecycleDelete    LifecycleAction = "delete"
	LifecycleReconcile LifecycleAction = "reconcile"
)

type ApplyLifecycleCommand struct {
	WorkspaceKey         string          `json:"workspace_key"`
	AgentID              string          `json:"agent_id"`
	Action               LifecycleAction `json:"action"`
	ExpectedUpdatedAt    time.Time       `json:"expected_updated_at"`
	ExpectedGenerationID string          `json:"expected_generation_id,omitempty"`
	IdempotencyKey       string          `json:"idempotency_key"`
}

type LifecycleResult struct {
	WorkspaceKey   string          `json:"workspace_key"`
	AgentID        string          `json:"agent_id"`
	IdempotencyKey string          `json:"idempotency_key"`
	Action         LifecycleAction `json:"action"`
	Agent          *Agent          `json:"agent"`
	BindingIDs     []string        `json:"binding_ids,omitempty"`
	GrantIDs       []string        `json:"grant_ids,omitempty"`
	CommittedAt    time.Time       `json:"committed_at"`
}

// ProvisioningCommands are replay-safe, exact-definition commands used only
// by the AgentProvisioning process manager. They use natural aggregate keys
// plus an immutable request ID: an exact existing aggregate is success, while
// a same-key divergent aggregate fails closed.
type ProvisioningCommands interface {
	EnsureManagedRole(ctx context.Context, auth authority.SystemAuthority, command EnsureRoleCommand) (*Role, error)
	EnsureManagedAgent(ctx context.Context, auth authority.SystemAuthority, command EnsureAgentCommand) (*Agent, error)
}

// ManagedRoleRepairCommands contains the one monotonic startup repair owned by
// Agents. The command uses the Role revision CAS; callers never receive a
// persistence interface or a generic Role update authority.
type ManagedRoleRepairCommands interface {
	RepairManagedRolePromptFile(context.Context, authority.SystemAuthority, RepairManagedRolePromptFileCommand) (*Role, bool, error)
}

type IdentityQueries interface {
	GetAgent(ctx context.Context, workspace, agentID string) (*Agent, error)
	ListAgents(ctx context.Context, workspace string, filter AgentFilter) ([]*Agent, error)
}

type IdentityCommands interface {
	CreateAgent(ctx context.Context, auth authority.OperatorAuthority, command CreateAgentCommand) (*Agent, error)
	UpdateAgent(ctx context.Context, auth authority.OperatorAuthority, command UpdateAgentCommand) (*Agent, error)
	ArchiveAgent(ctx context.Context, auth authority.OperatorAuthority, command ArchiveAgentCommand) (*Agent, error)
	SetDesiredState(ctx context.Context, auth authority.OperatorAuthority, command SetDesiredStateCommand) (*Agent, error)
	SetDesiredStateOwned(ctx context.Context, auth authority.SystemAuthority, ownership OwnershipProof, command SetDesiredStateOwnedCommand) (*Agent, error)
}

type RoleQueries interface {
	GetRole(ctx context.Context, workspace, roleName string) (*Role, error)
	ListRoles(ctx context.Context, workspace string) ([]*Role, error)
}

type RoleCommands interface {
	CreateRole(ctx context.Context, auth authority.OperatorAuthority, command CreateRoleCommand) (*Role, error)
	UpdateRole(ctx context.Context, auth authority.OperatorAuthority, command UpdateRoleCommand) (*Role, error)
	DeleteRole(ctx context.Context, auth authority.OperatorAuthority, command DeleteRoleCommand) error
}

type OwnershipQueries interface {
	GetOwnership(ctx context.Context, workspace, agentID string) (*AgentOwnershipLease, error)
	ListOwnership(ctx context.Context, workspace string, filter OwnershipFilter) ([]*AgentOwnershipLease, error)
}

type OwnershipCommands interface {
	AcquireOwnership(ctx context.Context, auth authority.SystemAuthority, command AcquireOwnershipCommand) (*OwnershipGrant, error)
	RenewOwnership(ctx context.Context, auth authority.SystemAuthority, ownership OwnershipProof, ttl time.Duration) (*AgentOwnershipLease, error)
	ReleaseOwnership(ctx context.Context, auth authority.SystemAuthority, ownership OwnershipProof) (*AgentOwnershipLease, error)
}

type CreateAgentCommand struct {
	WorkspaceKey    string            `json:"workspace_key"`
	AgentID         string            `json:"agent_id"`
	Name            string            `json:"name"`
	Kind            AgentKind         `json:"kind"`
	Behavior        BehaviorReference `json:"behavior"`
	DesiredState    DesiredState      `json:"desired_state"`
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
}

// EnsureRoleCommand is the complete immutable role definition required by one
// provisioning intent. RequestID is diagnostic/replay identity; the durable
// Role key remains {workspace,name}.
type EnsureRoleCommand struct {
	RequestID    string         `json:"request_id"`
	WorkspaceKey string         `json:"workspace_key"`
	Role         RoleDefinition `json:"role"`
}

type RepairManagedRolePromptFileCommand struct {
	RequestID    string `json:"request_id"`
	WorkspaceKey string `json:"workspace_key"`
	RoleName     string `json:"role_name"`
	PromptFile   string `json:"prompt_file"`
}

type CreateRoleCommand struct {
	WorkspaceKey string         `json:"workspace_key"`
	Role         RoleDefinition `json:"role"`
}

// RolePatch preserves legacy role clear semantics. A nil field means
// unchanged. Pointer-valued fields use a pointer-to-pointer so callers can
// distinguish unchanged from explicitly cleared.
type RolePatch struct {
	Kind           *string   `json:"kind,omitempty"`
	Description    *string   `json:"description,omitempty"`
	Prompt         *string   `json:"prompt,omitempty"`
	PromptFile     *string   `json:"prompt_file,omitempty"`
	Model          *string   `json:"model,omitempty"`
	TaskFilter     *string   `json:"task_filter,omitempty"`
	Backend        *string   `json:"backend,omitempty"`
	Effort         *string   `json:"effort,omitempty"`
	PathPatterns   *[]string `json:"path_patterns,omitempty"`
	Skills         *[]string `json:"skills,omitempty"`
	MaxPriority    **int     `json:"max_priority,omitempty"`
	MaxConcurrency **int     `json:"max_concurrency,omitempty"`
	ReadOnly       *bool     `json:"read_only,omitempty"`
	AllowedTools   *[]string `json:"allowed_tools,omitempty"`
	DeniedTools    *[]string `json:"denied_tools,omitempty"`
	MaxBudgetUSD   **float64 `json:"max_budget_usd,omitempty"`
}

type UpdateRoleCommand struct {
	WorkspaceKey      string    `json:"workspace_key"`
	RoleName          string    `json:"role_name"`
	ExpectedUpdatedAt time.Time `json:"expected_updated_at"`
	Patch             RolePatch `json:"patch"`
}

type DeleteRoleCommand struct {
	WorkspaceKey      string    `json:"workspace_key"`
	RoleName          string    `json:"role_name"`
	ExpectedUpdatedAt time.Time `json:"expected_updated_at"`
}

// EnsureAgentCommand carries the exact identity definition required by one
// provisioning intent. DesiredState is part of initial identity creation;
// later changes use the explicit desired-state commands.
type EnsureAgentCommand struct {
	RequestID string `json:"request_id"`
	CreateAgentCommand
}

// AgentPatch intentionally excludes desired state. Intent changes use the
// explicit desired-state commands and cannot hitchhike on identity updates.
type AgentPatch struct {
	Name            *string            `json:"name,omitempty"`
	Kind            *AgentKind         `json:"kind,omitempty"`
	Behavior        *BehaviorReference `json:"behavior,omitempty"`
	ProfileName     *string            `json:"profile_name,omitempty"`
	ScheduleID      *string            `json:"schedule_id,omitempty"`
	EventSources    *[]string          `json:"event_sources,omitempty"`
	TriggerRefs     *[]string          `json:"trigger_refs,omitempty"`
	PlacementPolicy *string            `json:"placement_policy,omitempty"`
	MaxInstances    *int               `json:"max_instances,omitempty"`
	LeaseID         *string            `json:"lease_id,omitempty"`
	RestartPolicy   *string            `json:"restart_policy,omitempty"`
	Permissions     *[]string          `json:"permissions,omitempty"`
	BudgetPolicy    *string            `json:"budget_policy,omitempty"`
	StateRef        *string            `json:"state_ref,omitempty"`
	Metadata        *map[string]string `json:"metadata,omitempty"`
}

type UpdateAgentCommand struct {
	WorkspaceKey      string     `json:"workspace_key"`
	AgentID           string     `json:"agent_id"`
	ExpectedUpdatedAt time.Time  `json:"expected_updated_at"`
	Patch             AgentPatch `json:"patch"`
}

type ArchiveAgentCommand struct {
	WorkspaceKey      string    `json:"workspace_key"`
	AgentID           string    `json:"agent_id"`
	ExpectedUpdatedAt time.Time `json:"expected_updated_at"`
}

type SetDesiredStateCommand struct {
	WorkspaceKey      string       `json:"workspace_key"`
	AgentID           string       `json:"agent_id"`
	ExpectedState     DesiredState `json:"expected_state"`
	DesiredState      DesiredState `json:"desired_state"`
	ExpectedUpdatedAt time.Time    `json:"expected_updated_at"`
}

type SetDesiredStateOwnedCommand struct {
	ExpectedState     DesiredState `json:"expected_state"`
	DesiredState      DesiredState `json:"desired_state"`
	ExpectedUpdatedAt time.Time    `json:"expected_updated_at"`
	IdempotencyKey    string       `json:"idempotency_key"`
}

type AgentFilter struct {
	Kind           AgentKind    `json:"kind,omitempty"`
	DesiredState   DesiredState `json:"desired_state,omitempty"`
	RoleName       string       `json:"role_name,omitempty"`
	IncludeDeleted bool         `json:"include_deleted,omitempty"`
	Limit          int          `json:"limit,omitempty"`
}

type AcquireOwnershipCommand struct {
	WorkspaceKey    string          `json:"workspace_key"`
	AgentID         string          `json:"agent_id"`
	LeaseID         string          `json:"lease_id"`
	RuntimeProvider RuntimeProvider `json:"runtime_provider"`
	NodeID          string          `json:"node_id"`
	TTL             time.Duration   `json:"-"`
}

type OwnershipFilter struct {
	OwnerID         string          `json:"owner_id,omitempty"`
	RuntimeProvider RuntimeProvider `json:"runtime_provider,omitempty"`
	NodeID          string          `json:"node_id,omitempty"`
	Status          OwnershipStatus `json:"status,omitempty"`
	Limit           int             `json:"limit,omitempty"`
}

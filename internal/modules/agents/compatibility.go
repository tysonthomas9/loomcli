package agents

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

const (
	ActionCreateSupervisedAssignment       authority.Action = "agents.create-supervised-assignment"
	ActionUpdateSupervisedAssignmentIntent authority.Action = "agents.update-supervised-assignment-intent"
	ActionRetireSupervisedAssignment       authority.Action = "agents.retire-supervised-assignment"
	ActionRetireManagedAssignment          authority.Action = "agents.retire-managed-supervised-assignment"
	ActionBindSupervisedAssignmentParent   authority.Action = "agents.bind-supervised-assignment-parent"
	ActionRepairManagedRolePromptFile      authority.Action = "agents.repair-managed-role-prompt-file"
)

// CompatibilityAPI is the bounded Phase 5 owner surface for the transitional
// supervised-assignment projection and exact startup repairs. It intentionally has no
// runtime-state mutation and no mixed PATCH operation.
type CompatibilityAPI interface {
	ProvisioningCommands
	CreateSupervisedAssignment(context.Context, authority.OperatorAuthority, CreateSupervisedAssignmentCommand) (*SupervisedAssignment, error)
	UpdateSupervisedAssignmentIntent(context.Context, authority.OperatorAuthority, UpdateSupervisedAssignmentIntentCommand) (*SupervisedAssignment, error)
	RetireSupervisedAssignment(context.Context, authority.OperatorAuthority, RetireSupervisedAssignmentCommand) error
	RetireManagedSupervisedAssignment(context.Context, authority.SystemAuthority, RetireSupervisedAssignmentCommand) error
	BindSupervisedAssignmentParent(context.Context, authority.SystemAuthority, BindSupervisedAssignmentParentCommand) (*SupervisedAssignment, error)
	RepairManagedRolePromptFile(context.Context, authority.SystemAuthority, RepairManagedRolePromptFileCommand) (*Role, bool, error)
}

// CompatibilityStore is implemented only by the owner-private compatstore
// adapter. Callers receive CompatibilityAPI, never this persistence port.
type CompatibilityStore interface {
	EnsureRole(context.Context, EnsureRoleCommand) (*Role, bool, error)
	EnsureAgent(context.Context, EnsureAgentCommand) (*Agent, bool, error)
	CreateSupervisedAssignment(context.Context, CreateSupervisedAssignmentCommand) (*SupervisedAssignment, error)
	UpdateSupervisedAssignmentIntent(context.Context, UpdateSupervisedAssignmentIntentCommand) (*SupervisedAssignment, error)
	RetireSupervisedAssignment(context.Context, RetireSupervisedAssignmentCommand) error
	BindSupervisedAssignmentParent(context.Context, BindSupervisedAssignmentParentCommand) (*SupervisedAssignment, error)
	RepairManagedRolePromptFile(context.Context, RepairManagedRolePromptFileCommand) (*Role, bool, error)
}

// SupervisedAssignmentMode is the Agents-owned execution shape of a
// transitional supervised assignment.
type SupervisedAssignmentMode string

const (
	SupervisedAssignmentModeEphemeral SupervisedAssignmentMode = "ephemeral"
	SupervisedAssignmentModeService   SupervisedAssignmentMode = "service"
)

// SupervisedAssignmentDesiredState is operator-owned intent. Runtime state is
// reported separately and cannot be changed through the intent patch.
type SupervisedAssignmentDesiredState string

const (
	SupervisedAssignmentDesiredStopped  SupervisedAssignmentDesiredState = "stopped"
	SupervisedAssignmentDesiredIdle     SupervisedAssignmentDesiredState = "idle"
	SupervisedAssignmentDesiredRunning  SupervisedAssignmentDesiredState = "running"
	SupervisedAssignmentDesiredDraining SupervisedAssignmentDesiredState = "draining"
)

// SupervisedAssignmentState is the coarse stored runtime projection.
type SupervisedAssignmentState string

const (
	SupervisedAssignmentStateIdle               SupervisedAssignmentState = "idle"
	SupervisedAssignmentStateActive             SupervisedAssignmentState = "active"
	SupervisedAssignmentStateStopped            SupervisedAssignmentState = "stopped"
	SupervisedAssignmentStateBackendUnavailable SupervisedAssignmentState = "backend_unavailable"
)

// SupervisedAssignmentLiveStatus is the read-only liveness projection returned
// by FleetDB.
type SupervisedAssignmentLiveStatus string

const (
	SupervisedAssignmentLiveWorking SupervisedAssignmentLiveStatus = "working"
	SupervisedAssignmentLiveIdle    SupervisedAssignmentLiveStatus = "idle"
)

// SupervisedAssignment is the Agents-owned compatibility projection exposed
// while legacy consumers migrate. It deliberately duplicates the wire shape
// instead of exporting internal/domain types through the capability boundary.
type SupervisedAssignment struct {
	WorkspaceKey     string                           `json:"workspace_key"`
	Name             string                           `json:"name"`
	RoleName         string                           `json:"role_name"`
	Auto             bool                             `json:"auto,omitempty"`
	Backend          string                           `json:"backend,omitempty"`
	FallbackBackends []string                         `json:"fallback_backends,omitempty"`
	Repos            []string                         `json:"repos,omitempty"`
	RepoGroups       []string                         `json:"repo_groups,omitempty"`
	CrossRepo        bool                             `json:"cross_repo,omitempty"`
	Parent           string                           `json:"parent,omitempty"`
	State            SupervisedAssignmentState        `json:"state,omitempty"`
	Mode             SupervisedAssignmentMode         `json:"mode,omitempty"`
	TaskFilter       string                           `json:"task_filter,omitempty"`
	MaxConcurrency   int                              `json:"max_concurrency,omitempty"`
	BudgetPolicy     string                           `json:"budget_policy,omitempty"`
	DesiredState     SupervisedAssignmentDesiredState `json:"desired_state,omitempty"`
	CreatedAt        time.Time                        `json:"created_at"`
	UpdatedAt        time.Time                        `json:"updated_at"`
	LiveStatus       SupervisedAssignmentLiveStatus   `json:"live_status,omitempty"`
	ActiveTaskID     string                           `json:"active_task_id,omitempty"`
	ActivePhase      string                           `json:"active_phase,omitempty"`
	LastErrorClass   string                           `json:"last_error_class,omitempty"`
}

// CreateSupervisedAssignmentCommand is the complete operator-controlled
// definition of the transitional assignment. Runtime state and parent
// orchestration linkage are deliberately absent.
type CreateSupervisedAssignmentCommand struct {
	WorkspaceKey     string
	AgentName        string
	RoleName         string
	Auto             bool
	Backend          string
	FallbackBackends []string
	Repos            []string
	RepoGroups       []string
	CrossRepo        bool
	Mode             SupervisedAssignmentMode
	TaskFilter       string
	MaxConcurrency   int
	BudgetPolicy     string
	DesiredState     SupervisedAssignmentDesiredState
}

// SupervisedAssignmentIntentPatch contains only operator-owned configuration
// and desired intent. State and Parent cannot hitchhike on this patch.
type SupervisedAssignmentIntentPatch struct {
	RoleName         *string
	Auto             *bool
	Backend          *string
	FallbackBackends *[]string
	Repos            *[]string
	RepoGroups       *[]string
	CrossRepo        *bool
	Mode             *SupervisedAssignmentMode
	TaskFilter       *string
	MaxConcurrency   *int
	BudgetPolicy     *string
	DesiredState     *SupervisedAssignmentDesiredState
}

type UpdateSupervisedAssignmentIntentCommand struct {
	WorkspaceKey string
	AgentName    string
	Patch        SupervisedAssignmentIntentPatch
}

// ParentBindingProof is the non-secret exact DriverRun generation already
// verified by the parent-binding application process. The process issues the
// system authority for this exact run only after Execution accepts the same
// owner/fence tuple.
type ParentBindingProof struct {
	DriverRunID  string
	NodeID       string
	LeaseID      string
	FencingToken int64
}

type BindSupervisedAssignmentParentCommand struct {
	WorkspaceKey   string
	AgentName      string
	ExpectedParent *string
	Parent         string
	Proof          ParentBindingProof
}

type RetireSupervisedAssignmentCommand struct {
	WorkspaceKey string
	AgentName    string
}

type RepairManagedRolePromptFileCommand struct {
	RequestID    string
	WorkspaceKey string
	RoleName     string
	PromptFile   string
}

// CompatibilityService owns admission and policy for the transitional
// persistence adapter. The adapter itself is authority-free and remains
// private to composition.
type CompatibilityService struct {
	store     CompatibilityStore
	admission *authority.Admission
}

func NewCompatibilityService(
	store CompatibilityStore,
	admission *authority.Admission,
) (*CompatibilityService, error) {
	if store == nil || admission == nil {
		return nil, fmt.Errorf("compose Agents compatibility service: %w", ErrUnavailable)
	}
	return &CompatibilityService{store: store, admission: admission}, nil
}

func (service *CompatibilityService) EnsureManagedRole(
	ctx context.Context,
	auth authority.SystemAuthority,
	command EnsureRoleCommand,
) (*Role, error) {
	if err := service.requireSystem(ActionEnsureManagedRole, command.WorkspaceKey, auth); err != nil {
		return nil, err
	}
	role, _, err := service.store.EnsureRole(ctx, command)
	return role, err
}

func (service *CompatibilityService) EnsureManagedAgent(
	ctx context.Context,
	auth authority.SystemAuthority,
	command EnsureAgentCommand,
) (*Agent, error) {
	if err := service.requireSystem(ActionEnsureManagedAgent, command.WorkspaceKey, auth); err != nil {
		return nil, err
	}
	agent, _, err := service.store.EnsureAgent(ctx, command)
	return agent, err
}

func (service *CompatibilityService) CreateSupervisedAssignment(
	ctx context.Context,
	auth authority.OperatorAuthority,
	command CreateSupervisedAssignmentCommand,
) (*SupervisedAssignment, error) {
	if err := service.requireOperator(ActionCreateSupervisedAssignment, command.WorkspaceKey, auth); err != nil {
		return nil, err
	}
	return service.store.CreateSupervisedAssignment(ctx, command)
}

func (service *CompatibilityService) UpdateSupervisedAssignmentIntent(
	ctx context.Context,
	auth authority.OperatorAuthority,
	command UpdateSupervisedAssignmentIntentCommand,
) (*SupervisedAssignment, error) {
	if err := service.requireOperator(ActionUpdateSupervisedAssignmentIntent, command.WorkspaceKey, auth); err != nil {
		return nil, err
	}
	return service.store.UpdateSupervisedAssignmentIntent(ctx, command)
}

func (service *CompatibilityService) RetireSupervisedAssignment(
	ctx context.Context,
	auth authority.OperatorAuthority,
	command RetireSupervisedAssignmentCommand,
) error {
	if err := service.requireOperator(ActionRetireSupervisedAssignment, command.WorkspaceKey, auth); err != nil {
		return err
	}
	return service.store.RetireSupervisedAssignment(ctx, command)
}

func (service *CompatibilityService) RetireManagedSupervisedAssignment(
	ctx context.Context,
	auth authority.SystemAuthority,
	command RetireSupervisedAssignmentCommand,
) error {
	if err := service.requireSystem(ActionRetireManagedAssignment, command.WorkspaceKey, auth); err != nil {
		return err
	}
	return service.store.RetireSupervisedAssignment(ctx, command)
}

func (service *CompatibilityService) BindSupervisedAssignmentParent(
	ctx context.Context,
	auth authority.SystemAuthority,
	command BindSupervisedAssignmentParentCommand,
) (*SupervisedAssignment, error) {
	if err := service.requireSystem(ActionBindSupervisedAssignmentParent, command.WorkspaceKey, auth); err != nil {
		return nil, err
	}
	if err := validateParentBindingProof(command.Proof); err != nil {
		return nil, err
	}
	if auth.Subject() != "driver-run:"+command.Proof.DriverRunID {
		return nil, fmt.Errorf("parent binding authority does not match verified DriverRun: %w", ErrNotOwner)
	}
	return service.store.BindSupervisedAssignmentParent(ctx, command)
}

func (service *CompatibilityService) RepairManagedRolePromptFile(
	ctx context.Context,
	auth authority.SystemAuthority,
	command RepairManagedRolePromptFileCommand,
) (*Role, bool, error) {
	if err := service.requireSystem(ActionRepairManagedRolePromptFile, command.WorkspaceKey, auth); err != nil {
		return nil, false, err
	}
	return service.store.RepairManagedRolePromptFile(ctx, command)
}

func (service *CompatibilityService) requireOperator(
	action authority.Action,
	workspace string,
	auth authority.OperatorAuthority,
) error {
	if service == nil || service.admission == nil {
		return authority.ErrAdmissionDenied
	}
	return service.admission.RequireOperator(action, workspace, auth)
}

func (service *CompatibilityService) requireSystem(
	action authority.Action,
	workspace string,
	auth authority.SystemAuthority,
) error {
	if service == nil || service.admission == nil {
		return authority.ErrAdmissionDenied
	}
	return service.admission.RequireSystem(action, workspace, auth)
}

func validateParentBindingProof(proof ParentBindingProof) error {
	for label, value := range map[string]string{
		"driver run id": proof.DriverRunID,
		"node id":       proof.NodeID,
		"lease id":      proof.LeaseID,
	} {
		if strings.TrimSpace(value) == "" || value != strings.TrimSpace(value) {
			return fmt.Errorf("%s must be canonical: %w", label, ErrInvalid)
		}
	}
	if proof.FencingToken <= 0 {
		return fmt.Errorf("parent binding fence must be positive: %w", ErrInvalid)
	}
	return nil
}

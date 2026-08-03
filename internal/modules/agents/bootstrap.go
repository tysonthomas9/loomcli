package agents

import (
	"context"
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

// BootstrapAPI is the Agents-owned system surface used by workspace creation
// and exact startup repair. It contains no legacy role-agent assignment API.
type BootstrapAPI interface {
	ProvisioningCommands
	RepairManagedRolePromptFile(context.Context, authority.SystemAuthority, RepairManagedRolePromptFileCommand) (*Role, bool, error)
}

// BootstrapStore persists only canonical Role and Agent identities. The
// adapter is private to composition and never receives a composite Store.
type BootstrapStore interface {
	EnsureRole(context.Context, EnsureRoleCommand) (*Role, bool, error)
	EnsureAgent(context.Context, EnsureAgentCommand) (*Agent, bool, error)
	RepairManagedRolePromptFile(context.Context, RepairManagedRolePromptFileCommand) (*Role, bool, error)
}

type RepairManagedRolePromptFileCommand struct {
	RequestID    string
	WorkspaceKey string
	RoleName     string
	PromptFile   string
}

type BootstrapService struct {
	store     BootstrapStore
	admission *authority.Admission
}

func NewBootstrapService(store BootstrapStore, admission *authority.Admission) (*BootstrapService, error) {
	if store == nil || admission == nil {
		return nil, fmt.Errorf("compose Agents bootstrap service: %w", ErrUnavailable)
	}
	return &BootstrapService{store: store, admission: admission}, nil
}

func (service *BootstrapService) EnsureManagedRole(
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

func (service *BootstrapService) EnsureManagedAgent(
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

func (service *BootstrapService) RepairManagedRolePromptFile(
	ctx context.Context,
	auth authority.SystemAuthority,
	command RepairManagedRolePromptFileCommand,
) (*Role, bool, error) {
	if err := service.requireSystem(ActionRepairManagedRolePromptFile, command.WorkspaceKey, auth); err != nil {
		return nil, false, err
	}
	return service.store.RepairManagedRolePromptFile(ctx, command)
}

func (service *BootstrapService) requireSystem(
	action authority.Action,
	workspace string,
	auth authority.SystemAuthority,
) error {
	if service == nil || service.admission == nil {
		return authority.ErrAdmissionDenied
	}
	return service.admission.RequireSystem(action, workspace, auth)
}

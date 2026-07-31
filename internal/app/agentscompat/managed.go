// Package agentscompat owns narrow application workflows over the public
// transitional Agents compatibility API. It never receives a process-wide
// store or concrete persistence adapter.
package agentscompat

import (
	"context"
	"fmt"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

const managedComponent = "agents-compat-managed-migration"

// NewAPI admits an already-composed owner persistence port behind the public
// compatibility API. Infrastructure construction remains in a composition
// root and cannot leak a process-wide store into this application workflow.
func NewAPI(
	persistence agents.CompatibilityStore,
	admission *authority.Admission,
) (agents.CompatibilityAPI, error) {
	service, err := agents.NewCompatibilityService(persistence, admission)
	if err != nil {
		return nil, err
	}
	return service, nil
}

// ManagedCommands is the exact system workflow needed by workspace bootstrap
// and serve-start repair. It deliberately has no generic authority factory.
type ManagedCommands interface {
	EnsureRole(context.Context, agents.EnsureRoleCommand) (*agents.Role, error)
	EnsureAgent(context.Context, agents.EnsureAgentCommand) (*agents.Agent, error)
	RepairRolePromptFile(context.Context, agents.RepairManagedRolePromptFileCommand) (*agents.Role, bool, error)
}

type managedCommands struct {
	api    agents.CompatibilityAPI
	issuer *authority.Issuer
	now    func() time.Time
}

// NewManagedCommands accepts only the public Agents API and its matching
// issuer. The returned workflow retains the issuer; consumers receive only
// ManagedCommands and cannot reach persistence composition.
func NewManagedCommands(
	api agents.CompatibilityAPI,
	issuer *authority.Issuer,
) (ManagedCommands, error) {
	if api == nil || issuer == nil {
		return nil, fmt.Errorf("compose managed Agents workflow: %w", agents.ErrUnavailable)
	}
	return &managedCommands{api: api, issuer: issuer, now: time.Now}, nil
}

// NewManagedCommandsWithIssuer preserves the Phase 5 composition call shape.
// New composition should call NewManagedCommands directly.
func NewManagedCommandsWithIssuer(
	api agents.CompatibilityAPI,
	issuer *authority.Issuer,
) (ManagedCommands, error) {
	return NewManagedCommands(api, issuer)
}

func (commands *managedCommands) EnsureRole(
	ctx context.Context,
	command agents.EnsureRoleCommand,
) (*agents.Role, error) {
	auth, err := commands.authority(command.WorkspaceKey, agents.ActionEnsureManagedRole, command.RequestID)
	if err != nil {
		return nil, err
	}
	return commands.api.EnsureManagedRole(ctx, auth, command)
}

func (commands *managedCommands) EnsureAgent(
	ctx context.Context,
	command agents.EnsureAgentCommand,
) (*agents.Agent, error) {
	auth, err := commands.authority(command.WorkspaceKey, agents.ActionEnsureManagedAgent, command.RequestID)
	if err != nil {
		return nil, err
	}
	return commands.api.EnsureManagedAgent(ctx, auth, command)
}

func (commands *managedCommands) RepairRolePromptFile(
	ctx context.Context,
	command agents.RepairManagedRolePromptFileCommand,
) (*agents.Role, bool, error) {
	auth, err := commands.authority(command.WorkspaceKey, agents.ActionRepairManagedRolePromptFile, command.RequestID)
	if err != nil {
		return nil, false, err
	}
	return commands.api.RepairManagedRolePromptFile(ctx, auth, command)
}

func (commands *managedCommands) authority(
	workspace string,
	action authority.Action,
	requestID string,
) (authority.SystemAuthority, error) {
	if commands == nil || commands.api == nil || commands.issuer == nil || commands.now == nil {
		return authority.SystemAuthority{}, agents.ErrUnavailable
	}
	if requestID == "" {
		return authority.SystemAuthority{}, fmt.Errorf("managed Agents request ID is required: %w", agents.ErrInvalid)
	}
	principal, err := commands.issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject:   managedComponent,
		Class:     authority.ClassSystem,
		Workspace: workspace,
		Actions:   []authority.Action{action},
		ExpiresAt: commands.now().Add(time.Minute),
	})
	if err != nil {
		return authority.SystemAuthority{}, err
	}
	return commands.issuer.IssueSystem(
		principal,
		workspace,
		action,
		managedComponent+" exact request "+requestID,
	)
}

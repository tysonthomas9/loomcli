// Package provisioningcomposition assembles the cross-capability
// AgentProvisioning process manager from exact owner ports.
package provisioningcomposition

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/app/agentprovisioning"
	provisioningagents "github.com/tysonthomas9/loomcli/internal/app/agentprovisioning/agents"
	provisioningautomation "github.com/tysonthomas9/loomcli/internal/app/agentprovisioning/automation"
	provisioningconnectors "github.com/tysonthomas9/loomcli/internal/app/agentprovisioning/connectors"
	provisioningfleetdb "github.com/tysonthomas9/loomcli/internal/app/agentprovisioning/fleetdb"
	infrafleetdb "github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	"github.com/tysonthomas9/loomcli/internal/modules/connectors"
	workflowcataloghttp "github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog/httpapi"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	platformruntime "github.com/tysonthomas9/loomcli/internal/platform/runtime"
)

// Capability is the composition-owned cross-aggregate
// process manager. It exposes only request commands, the request authority
// resolver, and inert runtime registrations; none of the three capability
// issuers or the shared Fleet transport cross this boundary.
type Capability struct {
	commands         agentprovisioning.Commands
	operatorResolver workflowcataloghttp.OperatorAuthorityResolver
	runtime          []platformruntime.Registration
}

func (capability *Capability) AgentProvisioningCommands() agentprovisioning.Commands {
	if capability == nil {
		return nil
	}
	return capability.commands
}

func (capability *Capability) OperatorAuthorityResolver() workflowcataloghttp.OperatorAuthorityResolver {
	if capability == nil {
		return nil
	}
	return capability.operatorResolver
}

func (capability *Capability) RuntimeRegistrations() []platformruntime.Registration {
	if capability == nil {
		return nil
	}
	return append([]platformruntime.Registration(nil), capability.runtime...)
}

type Config struct {
	WorkspaceKey    string
	WorkspaceLister agentprovisioning.WorkspaceLister
}

// Owners contains only the exact Automation and Connectors
// owner ports and authority seals required by the cross-aggregate manager.
// The Agents owner remains private on AgentsCapability.
type Owners struct {
	Bindings         automation.ProvisioningBindingCommands
	AutomationIssuer *authority.Issuer
	Grants           connectors.GrantCommands
	ConnectorsIssuer *authority.Issuer
}

// AgentsOwner is the exact Agents-owned command and authority surface needed
// by the process manager.
type AgentsOwner struct {
	Commands         agents.ProvisioningCommands
	Issuer           *authority.Issuer
	OperatorResolver workflowcataloghttp.OperatorAuthorityResolver
}

const ActionBeginProvisioning = agentprovisioning.ActionBeginProvisioning

// New composes the real production manager from
// the existing owner capabilities. Each step receives a fresh, fixed-action
// SystemAuthority from its owner's issuer, then uses a typed FleetDB owner
// command that validates the durable provisioning generation atomically with
// the target mutation. Progress and owner commands share one authenticated
// low-level FleetDB client without sharing a generic mutation surface.
func New(
	agentsOwner AgentsOwner,
	client *infrafleetdb.Client,
	config Config,
	owners Owners,
) (*Capability, error) {
	if agentsOwner.Commands == nil || agentsOwner.Issuer == nil ||
		agentsOwner.OperatorResolver == nil {
		return nil, fmt.Errorf("compose AgentProvisioning Agents dependency: %w", agentprovisioning.ErrUnavailable)
	}
	if owners.AutomationIssuer == nil || owners.Bindings == nil {
		return nil, fmt.Errorf("compose AgentProvisioning Automation dependency: %w", agentprovisioning.ErrUnavailable)
	}
	if owners.ConnectorsIssuer == nil || owners.Grants == nil {
		return nil, fmt.Errorf("compose AgentProvisioning Connectors dependency: %w", agentprovisioning.ErrUnavailable)
	}
	if client == nil {
		return nil, fmt.Errorf("compose AgentProvisioning progress dependency: %w", agentprovisioning.ErrUnavailable)
	}
	progress, err := provisioningfleetdb.New(client.AgentProvisioning())
	if err != nil {
		return nil, err
	}
	return composeAgentProvisioningCapability(agentProvisioningDependencies{
		progress: progress,
		agents:   agentsOwner.Commands, agentsIssuer: agentsOwner.Issuer,
		bindings: owners.Bindings, automationIssuer: owners.AutomationIssuer,
		grants: owners.Grants, connectorsIssuer: owners.ConnectorsIssuer,
		operatorResolver: agentsOwner.OperatorResolver,
		workspaceKey:     config.WorkspaceKey, workspaceLister: config.WorkspaceLister,
		now: time.Now,
	})
}

type agentProvisioningDependencies struct {
	progress agentprovisioning.ProgressStore
	agents   agents.ProvisioningCommands
	bindings automation.ProvisioningBindingCommands
	grants   connectors.GrantCommands

	agentsIssuer     *authority.Issuer
	automationIssuer *authority.Issuer
	connectorsIssuer *authority.Issuer
	operatorResolver workflowcataloghttp.OperatorAuthorityResolver
	workspaceKey     string
	workspaceLister  agentprovisioning.WorkspaceLister
	now              func() time.Time
}

//nolint:funlen // Cross-capability composition validates and seals every exact owner dependency in one boundary.
func composeAgentProvisioningCapability(
	dependencies agentProvisioningDependencies,
) (*Capability, error) {
	if dependencies.progress == nil || dependencies.agents == nil ||
		dependencies.bindings == nil || dependencies.grants == nil ||
		dependencies.agentsIssuer == nil || dependencies.automationIssuer == nil ||
		dependencies.connectorsIssuer == nil || dependencies.operatorResolver == nil ||
		dependencies.now == nil {
		return nil, fmt.Errorf("compose AgentProvisioning dependencies: %w", agentprovisioning.ErrUnavailable)
	}
	admission, err := dependencies.agentsIssuer.NewAdmission(agentprovisioning.OperationRules()...)
	if err != nil {
		return nil, fmt.Errorf("compose AgentProvisioning admission: %w", err)
	}
	authorities := &agentProvisioningAuthorityProvider{
		agentsIssuer: dependencies.agentsIssuer, automationIssuer: dependencies.automationIssuer,
		connectorsIssuer: dependencies.connectorsIssuer, now: dependencies.now,
	}
	agentSteps, ok := dependencies.progress.(interface {
		agentprovisioning.RoleOperations
		agentprovisioning.AgentOperations
	})
	if !ok {
		return nil, fmt.Errorf(
			"compose AgentProvisioning guarded Agents owner commands: %w",
			agentprovisioning.ErrUnavailable,
		)
	}
	bindingSteps, ok := dependencies.progress.(agentprovisioning.BindingOperations)
	if !ok {
		return nil, fmt.Errorf(
			"compose AgentProvisioning guarded Automation owner command: %w",
			agentprovisioning.ErrUnavailable,
		)
	}
	grantSteps, ok := dependencies.progress.(agentprovisioning.GrantOperations)
	if !ok {
		return nil, fmt.Errorf(
			"compose AgentProvisioning guarded Connectors owner command: %w",
			agentprovisioning.ErrUnavailable,
		)
	}
	agentsAdapter, err := provisioningagents.New(agentSteps, authorities)
	if err != nil {
		return nil, err
	}
	automationAdapter, err := provisioningautomation.New(bindingSteps, authorities)
	if err != nil {
		return nil, err
	}
	connectorsAdapter, err := provisioningconnectors.New(grantSteps, authorities)
	if err != nil {
		return nil, err
	}
	manager, err := agentprovisioning.New(
		dependencies.progress,
		agentsAdapter,
		agentsAdapter,
		automationAdapter,
		connectorsAdapter,
		admission,
		nil,
		dependencies.now,
	)
	if err != nil {
		return nil, err
	}
	registration, err := agentprovisioning.RuntimeRegistration(
		manager,
		agentprovisioning.RuntimeConfig{
			WorkspaceKey:    dependencies.workspaceKey,
			WorkspaceLister: dependencies.workspaceLister,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("compose AgentProvisioning recovery: %w", err)
	}
	return &Capability{
		commands: manager, operatorResolver: dependencies.operatorResolver,
		runtime: []platformruntime.Registration{registration},
	}, nil
}

// agentProvisioningAuthorityProvider implements four fixed-action methods.
// The application adapters cannot select another action or issuer.
type agentProvisioningAuthorityProvider struct {
	agentsIssuer     *authority.Issuer
	automationIssuer *authority.Issuer
	connectorsIssuer *authority.Issuer
	now              func() time.Time
}

func (provider *agentProvisioningAuthorityProvider) AuthorityForRole(
	ctx context.Context,
	workspace,
	reason string,
) (authority.SystemAuthority, error) {
	return provider.issue(ctx, provider.agentsIssuer, workspace, agents.ActionEnsureManagedRole, reason)
}

func (provider *agentProvisioningAuthorityProvider) AuthorityForAgent(
	ctx context.Context,
	workspace,
	reason string,
) (authority.SystemAuthority, error) {
	return provider.issue(ctx, provider.agentsIssuer, workspace, agents.ActionEnsureManagedAgent, reason)
}

func (provider *agentProvisioningAuthorityProvider) AuthorityForBinding(
	ctx context.Context,
	workspace,
	reason string,
) (authority.SystemAuthority, error) {
	return provider.issue(ctx, provider.automationIssuer, workspace, automation.ActionEnsureManagedBinding, reason)
}

func (provider *agentProvisioningAuthorityProvider) AuthorityForGrant(
	ctx context.Context,
	workspace,
	reason string,
) (authority.SystemAuthority, error) {
	return provider.issue(ctx, provider.connectorsIssuer, workspace, connectors.ActionEnsureGrant, reason)
}

func (provider *agentProvisioningAuthorityProvider) issue(
	ctx context.Context,
	issuer *authority.Issuer,
	workspace string,
	action authority.Action,
	reason string,
) (authority.SystemAuthority, error) {
	if provider == nil || issuer == nil || provider.now == nil {
		return authority.SystemAuthority{}, agentprovisioning.ErrUnavailable
	}
	if ctx == nil {
		return authority.SystemAuthority{}, fmt.Errorf("AgentProvisioning authority context is required: %w", authority.ErrInvalidScope)
	}
	if err := ctx.Err(); err != nil {
		return authority.SystemAuthority{}, err
	}
	workspace = strings.TrimSpace(workspace)
	reason = strings.TrimSpace(reason)
	if workspace == "" || reason == "" {
		return authority.SystemAuthority{}, fmt.Errorf("AgentProvisioning authority scope and reason are required: %w", authority.ErrInvalidScope)
	}
	subject := string(agentprovisioning.RecoveryComponentID)
	principal, err := issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject: subject, Class: authority.ClassSystem, Workspace: workspace,
		Actions: []authority.Action{action}, ExpiresAt: provider.now().Add(time.Minute),
	})
	if err != nil {
		return authority.SystemAuthority{}, err
	}
	return issuer.IssueSystem(principal, workspace, action, reason)
}

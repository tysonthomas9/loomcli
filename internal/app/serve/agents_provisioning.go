package serve

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/app/agentprovisioning"
	provisioningfleetdb "github.com/tysonthomas9/loomcli/internal/app/agentprovisioning/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/app/serve/agentcomposition/owneradapters"
	infrafleetdb "github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	"github.com/tysonthomas9/loomcli/internal/modules/connectors"
	workflowcataloghttp "github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog/httpapi"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	platformruntime "github.com/tysonthomas9/loomcli/internal/platform/runtime"
)

// AgentProvisioningCapability is the composition-owned cross-aggregate
// process manager. It exposes only request commands, the request authority
// resolver, and inert runtime registrations. Owner issuers and the shared
// FleetDB transport do not cross this boundary.
type AgentProvisioningCapability struct {
	commands         agentprovisioning.Commands
	operatorResolver workflowcataloghttp.OperatorAuthorityResolver
	runtime          []platformruntime.Registration
}

func (capability *AgentProvisioningCapability) AgentProvisioningCommands() agentprovisioning.Commands {
	if capability == nil {
		return nil
	}
	return capability.commands
}

func (capability *AgentProvisioningCapability) OperatorAuthorityResolver() workflowcataloghttp.OperatorAuthorityResolver {
	if capability == nil {
		return nil
	}
	return capability.operatorResolver
}

func (capability *AgentProvisioningCapability) RuntimeRegistrations() []platformruntime.Registration {
	if capability == nil {
		return nil
	}
	return append([]platformruntime.Registration(nil), capability.runtime...)
}

type AgentProvisioningConfig struct {
	WorkspaceKey    string
	WorkspaceLister agentprovisioning.WorkspaceLister
}

// AgentProvisioningOwners contains the authority seals required for the
// Automation and Connectors steps. The concrete mutations are deliberately
// absent: the durable FleetDB adapter below is the one canonical path because
// it validates the provisioning generation in the same transaction as every
// owner mutation.
type AgentProvisioningOwners struct {
	AutomationIssuer *authority.Issuer
	ConnectorsIssuer *authority.Issuer
}

const agentProvisioningActionBegin = agentprovisioning.ActionBeginProvisioning

func (capability *AgentsCapability) newAgentProvisioningCapabilityWithOwners(
	client *infrafleetdb.Client,
	config AgentProvisioningConfig,
	owners AgentProvisioningOwners,
) (*AgentProvisioningCapability, error) {
	var agentsIssuer *authority.Issuer
	var operatorResolver workflowcataloghttp.OperatorAuthorityResolver
	if capability != nil {
		agentsIssuer = capability.issuer
		operatorResolver = capability.operatorResolver
	}
	return newAgentProvisioningCapability(
		agentsIssuer,
		operatorResolver,
		client,
		config,
		owners,
	)
}

// newAgentProvisioningCapability composes the production manager. Each step
// derives a fresh, fixed-action SystemAuthority from its owner's issuer before
// invoking the generation-guarded FleetDB operation. Progress and mutations
// share one authenticated low-level client without publishing a generic write
// surface or creating a parallel module-command path.
func newAgentProvisioningCapability(
	agentsIssuer *authority.Issuer,
	operatorResolver workflowcataloghttp.OperatorAuthorityResolver,
	client *infrafleetdb.Client,
	config AgentProvisioningConfig,
	owners AgentProvisioningOwners,
) (*AgentProvisioningCapability, error) {
	if agentsIssuer == nil || operatorResolver == nil {
		return nil, fmt.Errorf("compose AgentProvisioning Agents dependency: %w", agentprovisioning.ErrUnavailable)
	}
	if owners.AutomationIssuer == nil {
		return nil, fmt.Errorf("compose AgentProvisioning Automation dependency: %w", agentprovisioning.ErrUnavailable)
	}
	if owners.ConnectorsIssuer == nil {
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
		progress:     progress,
		agentsIssuer: agentsIssuer, automationIssuer: owners.AutomationIssuer,
		connectorsIssuer: owners.ConnectorsIssuer, operatorResolver: operatorResolver,
		workspaceKey: config.WorkspaceKey, workspaceLister: config.WorkspaceLister,
		now: time.Now,
	})
}

type agentProvisioningDependencies struct {
	progress agentprovisioning.ProgressStore

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
) (*AgentProvisioningCapability, error) {
	if dependencies.progress == nil ||
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
	agentsAdapter, err := owneradapters.NewAgentsAdapter(agentSteps, authorities)
	if err != nil {
		return nil, err
	}
	automationAdapter, err := owneradapters.NewAutomationAdapter(bindingSteps, authorities)
	if err != nil {
		return nil, err
	}
	connectorsAdapter, err := owneradapters.NewConnectorsAdapter(grantSteps, authorities)
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
	return &AgentProvisioningCapability{
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

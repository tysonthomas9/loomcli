package serve

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/tysonthomas9/loomcli/internal/app/agentprovisioning"
	provisioningfleetdb "github.com/tysonthomas9/loomcli/internal/app/agentprovisioning/fleetdb"
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
	agentsAdapter, err := newAgentProvisioningAgentsAdapter(agentSteps, authorities)
	if err != nil {
		return nil, err
	}
	automationAdapter, err := newAgentProvisioningAutomationAdapter(bindingSteps, authorities)
	if err != nil {
		return nil, err
	}
	connectorsAdapter, err := newAgentProvisioningConnectorsAdapter(grantSteps, authorities)
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

type agentProvisioningAgentsOperations interface {
	EnsureRole(context.Context, agentprovisioning.EnsureRoleCommand) error
	EnsureAgent(context.Context, agentprovisioning.EnsureAgentCommand) error
}

// agentProvisioningAgentsAuthority exposes fixed actions so this composition
// adapter cannot turn a provisioning step into another Agents action.
type agentProvisioningAgentsAuthority interface {
	AuthorityForRole(context.Context, string, string) (authority.SystemAuthority, error)
	AuthorityForAgent(context.Context, string, string) (authority.SystemAuthority, error)
}

type agentProvisioningAgentsAdapter struct {
	operations  agentProvisioningAgentsOperations
	authorities agentProvisioningAgentsAuthority
}

var (
	_ agentprovisioning.RoleOperations  = (*agentProvisioningAgentsAdapter)(nil)
	_ agentprovisioning.AgentOperations = (*agentProvisioningAgentsAdapter)(nil)
)

func newAgentProvisioningAgentsAdapter(
	operations agentProvisioningAgentsOperations,
	authorities agentProvisioningAgentsAuthority,
) (*agentProvisioningAgentsAdapter, error) {
	if operations == nil || authorities == nil {
		return nil, fmt.Errorf("compose AgentProvisioning Agents adapter: %w", agentprovisioning.ErrUnavailable)
	}
	return &agentProvisioningAgentsAdapter{operations: operations, authorities: authorities}, nil
}

func (adapter *agentProvisioningAgentsAdapter) EnsureRole(
	ctx context.Context,
	command agentprovisioning.EnsureRoleCommand,
) error {
	_, err := adapter.authorities.AuthorityForRole(ctx, command.WorkspaceKey, "AgentProvisioning "+command.CommandID)
	if err != nil {
		return fmt.Errorf(
			"issue role authority through Agents guarded owner command: %w",
			errors.Join(agentprovisioning.ErrUnavailable, err),
		)
	}
	if err := adapter.operations.EnsureRole(ctx, command); err != nil {
		return fmt.Errorf("ensure role through Agents guarded owner command: %w", err)
	}
	return nil
}

func (adapter *agentProvisioningAgentsAdapter) EnsureAgent(
	ctx context.Context,
	command agentprovisioning.EnsureAgentCommand,
) error {
	_, err := adapter.authorities.AuthorityForAgent(ctx, command.WorkspaceKey, "AgentProvisioning "+command.CommandID)
	if err != nil {
		return fmt.Errorf(
			"issue agent authority through Agents guarded owner command: %w",
			errors.Join(agentprovisioning.ErrUnavailable, err),
		)
	}
	if err := adapter.operations.EnsureAgent(ctx, command); err != nil {
		return fmt.Errorf("ensure agent through Agents guarded owner command: %w", err)
	}
	return nil
}

type agentProvisioningAutomationOperations interface {
	EnsureBinding(context.Context, agentprovisioning.EnsureBindingCommand) error
}

type agentProvisioningAutomationAuthority interface {
	AuthorityForBinding(context.Context, string, string) (authority.SystemAuthority, error)
}

type agentProvisioningAutomationAdapter struct {
	operations  agentProvisioningAutomationOperations
	authorities agentProvisioningAutomationAuthority
}

var _ agentprovisioning.BindingOperations = (*agentProvisioningAutomationAdapter)(nil)

func newAgentProvisioningAutomationAdapter(
	operations agentProvisioningAutomationOperations,
	authorities agentProvisioningAutomationAuthority,
) (*agentProvisioningAutomationAdapter, error) {
	if operations == nil || authorities == nil {
		return nil, fmt.Errorf("compose AgentProvisioning Automation adapter: %w", agentprovisioning.ErrUnavailable)
	}
	return &agentProvisioningAutomationAdapter{operations: operations, authorities: authorities}, nil
}

func (adapter *agentProvisioningAutomationAdapter) EnsureBinding(
	ctx context.Context,
	command agentprovisioning.EnsureBindingCommand,
) error {
	_, err := adapter.authorities.AuthorityForBinding(ctx, command.WorkspaceKey, "AgentProvisioning "+command.CommandID)
	if err != nil {
		return fmt.Errorf(
			"issue binding authority through Automation guarded owner command: %w",
			errors.Join(agentprovisioning.ErrUnavailable, err),
		)
	}
	if err := adapter.operations.EnsureBinding(ctx, command); err != nil {
		return fmt.Errorf("ensure binding through Automation guarded owner command: %w", err)
	}
	return nil
}

// agentProvisioningConnectorsAuthority is action-specific: composition may
// issue only Connectors' ensure-grant action for the requested workspace.
type agentProvisioningConnectorsAuthority interface {
	AuthorityForGrant(context.Context, string, string) (authority.SystemAuthority, error)
}

type agentProvisioningConnectorsAdapter struct {
	grants    agentprovisioning.GrantOperations
	authority agentProvisioningConnectorsAuthority
}

var _ agentprovisioning.GrantOperations = (*agentProvisioningConnectorsAdapter)(nil)

func newAgentProvisioningConnectorsAdapter(
	grants agentprovisioning.GrantOperations,
	authorityProvider agentProvisioningConnectorsAuthority,
) (*agentProvisioningConnectorsAdapter, error) {
	if grants == nil || authorityProvider == nil {
		return nil, fmt.Errorf("compose AgentProvisioning Connector grants: dependencies are required: %w", agentprovisioning.ErrUnavailable)
	}
	return &agentProvisioningConnectorsAdapter{grants: grants, authority: authorityProvider}, nil
}

func (adapter *agentProvisioningConnectorsAdapter) EnsureGrant(
	ctx context.Context,
	command agentprovisioning.EnsureGrantCommand,
) error {
	if adapter == nil || adapter.grants == nil || adapter.authority == nil {
		return agentprovisioning.ErrUnavailable
	}
	if !canonicalAgentProvisioningAuditID(command.CommandID) {
		return fmt.Errorf("connector grant command id is not canonical: %w", agentprovisioning.ErrInvalid)
	}
	_, err := adapter.authority.AuthorityForGrant(ctx, command.WorkspaceKey, "AgentProvisioning "+command.CommandID)
	if err != nil {
		return fmt.Errorf(
			"issue Connector grant authority: %w",
			errors.Join(agentprovisioning.ErrUnavailable, err),
		)
	}
	if err := adapter.grants.EnsureGrant(ctx, command); err != nil {
		return fmt.Errorf("ensure Connector grant through Connectors guarded owner command: %w", err)
	}
	return nil
}

func canonicalAgentProvisioningAuditID(value string) bool {
	if value == "" || value != strings.TrimSpace(value) {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

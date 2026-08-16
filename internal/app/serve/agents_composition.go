// Agents composition assembles the Phase 5 capability from owner-scoped ports
// inside the serve application root.
package serve

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/automation"

	infrafleetdb "github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	agentsfleetdb "github.com/tysonthomas9/loomcli/internal/modules/agents/fleetdb"
	workflowcataloghttp "github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog/httpapi"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	platformruntime "github.com/tysonthomas9/loomcli/internal/platform/runtime"
)

// AgentsCapability is the composition-owned Phase 5 Agents handle. Consumers
// receive only the public API and request authority resolver; the issuer,
// Fleet transport, and persistence adapter remain private.
type AgentsCapability struct {
	api              agents.API
	issuer           *authority.Issuer
	operatorResolver workflowcataloghttp.OperatorAuthorityResolver
	runtime          []platformruntime.Registration
}

func (capability *AgentsCapability) RuntimeRegistrations() []platformruntime.Registration {
	if capability == nil {
		return nil
	}
	return append([]platformruntime.Registration(nil), capability.runtime...)
}

func (capability *AgentsCapability) AgentsAPI() agents.API {
	if capability == nil {
		return nil
	}
	return capability.api
}

// EnsureRole implements workspace management's consumer-owned bootstrap port.
// The application root derives exact system authority; workspace management
// never receives the Agents issuer or a persistence adapter.
func (capability *AgentsCapability) EnsureRole(
	ctx context.Context,
	command agents.EnsureRoleCommand,
) (*agents.Role, error) {
	auth, err := capability.workspaceBootstrapAuthority(
		ctx, command.WorkspaceKey, agents.ActionEnsureManagedRole, command.RequestID,
	)
	if err != nil {
		return nil, err
	}
	return capability.api.EnsureManagedRole(ctx, auth, command)
}

// GetRole implements workspace management's read-only bootstrap query without
// exposing the complete Agents capability.
func (capability *AgentsCapability) GetRole(
	ctx context.Context,
	workspace, roleName string,
) (*agents.Role, error) {
	if capability == nil || capability.api == nil {
		return nil, agents.ErrUnavailable
	}
	return capability.api.GetRole(ctx, workspace, roleName)
}

// RepairRolePromptFile implements workspace management's monotonic repair
// port without exposing generic Role update authority.
func (capability *AgentsCapability) RepairRolePromptFile(
	ctx context.Context,
	command agents.RepairManagedRolePromptFileCommand,
) (*agents.Role, bool, error) {
	auth, err := capability.workspaceBootstrapAuthority(
		ctx, command.WorkspaceKey, agents.ActionRepairManagedRolePromptFile, command.RequestID,
	)
	if err != nil {
		return nil, false, err
	}
	return capability.api.RepairManagedRolePromptFile(ctx, auth, command)
}

func (capability *AgentsCapability) workspaceBootstrapAuthority(
	ctx context.Context,
	workspace string,
	action authority.Action,
	requestID string,
) (authority.SystemAuthority, error) {
	if capability == nil || capability.api == nil || capability.issuer == nil {
		return authority.SystemAuthority{}, agents.ErrUnavailable
	}
	if ctx == nil {
		return authority.SystemAuthority{}, fmt.Errorf("workspace bootstrap authority context is required: %w", authority.ErrInvalidScope)
	}
	if err := ctx.Err(); err != nil {
		return authority.SystemAuthority{}, err
	}
	workspace = strings.TrimSpace(workspace)
	requestID = strings.TrimSpace(requestID)
	if workspace == "" || requestID == "" {
		return authority.SystemAuthority{}, fmt.Errorf("workspace bootstrap scope and request ID are required: %w", authority.ErrInvalidScope)
	}
	const subject = "workspace-bootstrap"
	principal, err := capability.issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject: subject, Class: authority.ClassSystem, Workspace: workspace,
		Actions: []authority.Action{action}, ExpiresAt: time.Now().Add(time.Minute),
	})
	if err != nil {
		return authority.SystemAuthority{}, err
	}
	return capability.issuer.IssueSystem(
		principal, workspace, action, subject+" exact request "+requestID,
	)
}

func (capability *AgentsCapability) OperatorAuthorityResolver() workflowcataloghttp.OperatorAuthorityResolver {
	if capability == nil {
		return nil
	}
	return capability.operatorResolver
}

// ConvergeReviewerIdentity is PR Review's purpose-scoped Agents port. It
// derives authority for exactly one managed-reviewer command and never exposes
// the issuer or generic Role/Agent mutation methods to the delivery adapter.
func (capability *AgentsCapability) ConvergeReviewerIdentity(
	ctx context.Context,
	command agents.ManagedReviewerCommand,
) (*agents.ManagedReviewerResult, error) {
	if capability == nil || capability.api == nil {
		return nil, agents.ErrUnavailable
	}
	auth, err := capability.reviewerAuthority(ctx, command.WorkspaceKey, command.AgentID)
	if err != nil {
		return nil, err
	}
	return capability.api.ConvergeManagedReviewer(ctx, auth, command)
}

type AgentsConfig struct {
	FleetDBClient                   *infrafleetdb.Client
	TriggerBindings                 automation.TriggerBindingStore
	WorkspaceKey                    string
	WorkspaceLister                 agents.RuntimeWorkspaceLister
	ExternalAuth                    bool
	ExternalOperatorResolverFactory ExternalOperatorResolverFactory
}

//nolint:funlen // Composition keeps the exact Agents ports, authority issuer, and compatibility adapters visibly co-located.
func NewAgentsCapability(config AgentsConfig) (*AgentsCapability, error) {
	if config.FleetDBClient == nil {
		return nil, fmt.Errorf("compose Agents FleetDB transport: %w", agents.ErrUnavailable)
	}
	issuer := authority.NewIssuer()
	admission, err := issuer.NewAdmission(agents.OperationRules()...)
	if err != nil {
		return nil, fmt.Errorf("compose Agents admission: %w", err)
	}
	adapter, err := agentsfleetdb.New(newAgentsFleetDBTransport(config.FleetDBClient))
	if err != nil {
		return nil, fmt.Errorf("compose Agents FleetDB adapter: %w", err)
	}
	service, err := agents.NewWithLifecycle(
		adapter, adapter, adapter, adapter, adapter, adapter, adapter, adapter,
		newAgentBindingStateSource(config.TriggerBindings), admission,
	)
	if err != nil {
		return nil, err
	}
	resolver, err := composeAgentsOperatorResolver(config, issuer)
	if err != nil {
		return nil, err
	}
	runtimeRegistration, err := agents.RuntimeRegistration(
		service,
		service,
		newAgentsRuntimeAuthorityProvider(issuer, time.Now),
		agents.RuntimeConfig{WorkspaceKey: config.WorkspaceKey, WorkspaceLister: config.WorkspaceLister},
	)
	if err != nil {
		return nil, fmt.Errorf("compose Agents desired-state reconciliation: %w", err)
	}
	return &AgentsCapability{
		api: service, issuer: issuer, operatorResolver: resolver,
		runtime: []platformruntime.Registration{runtimeRegistration},
	}, nil
}

func composeAgentsOperatorResolver(
	config AgentsConfig,
	issuer *authority.Issuer,
) (workflowcataloghttp.OperatorAuthorityResolver, error) {
	if config.ExternalAuth {
		if config.ExternalOperatorResolverFactory == nil {
			return nil, fmt.Errorf("compose Agents external authorization: operator resolver factory is required")
		}
		resolver := config.ExternalOperatorResolverFactory(issuer, workflowcataloghttp.ErrUnauthenticated)
		if resolver == nil {
			return nil, fmt.Errorf("compose Agents external authorization: operator resolver is unavailable")
		}
		return resolver, nil
	}
	resolver, err := NewLocalOpenOperatorResolver(issuer, agentsOperatorActions()...)
	if err != nil {
		return nil, fmt.Errorf("compose Agents local open authority: %w", err)
	}
	return resolver, nil
}

func agentsOperatorActions() []authority.Action {
	return []authority.Action{
		agents.ActionCreateAgent,
		agents.ActionUpdateAgent,
		agents.ActionArchiveAgent,
		agents.ActionSetDesiredState,
		agents.ActionApplyLifecycle,
		agents.ActionCreateRole,
		agents.ActionUpdateRole,
		agents.ActionDeleteRole,
		agentProvisioningActionBegin,
	}
}

type agentBindingStateSource struct {
	bindings automation.TriggerBindingStore
}

var _ agents.DesiredStateBindingSource = (*agentBindingStateSource)(nil)

func newAgentBindingStateSource(bindings automation.TriggerBindingStore) agents.DesiredStateBindingSource {
	if bindings == nil {
		return nil
	}
	return &agentBindingStateSource{bindings: bindings}
}

func (source *agentBindingStateSource) ListAgentBindingStates(
	ctx context.Context,
	workspace,
	agentID string,
) ([]bool, error) {
	if source == nil || source.bindings == nil {
		return nil, agents.ErrUnavailable
	}
	bindings, err := source.bindings.List(ctx, workspace, automation.TriggerBindingFilter{
		TargetAgentServiceID: agentID,
	})
	if err != nil {
		return nil, fmt.Errorf("list Automation bindings for Agent %q: %w", agentID, err)
	}
	states := make([]bool, 0, len(bindings))
	for _, binding := range bindings {
		if binding == nil || binding.WorkspaceKey != workspace || binding.TargetAgentServiceID != agentID {
			return nil, agents.ErrInvalidPersistedState
		}
		states = append(states, binding.Enabled)
	}
	return states, nil
}

type agentsRuntimeAuthorityProvider struct {
	issuer *authority.Issuer
	now    func() time.Time
}

var _ agents.RuntimeAuthorityProvider = (*agentsRuntimeAuthorityProvider)(nil)

func newAgentsRuntimeAuthorityProvider(issuer *authority.Issuer, now func() time.Time) agents.RuntimeAuthorityProvider {
	if issuer == nil || now == nil {
		return nil
	}
	return &agentsRuntimeAuthorityProvider{issuer: issuer, now: now}
}

func (provider *agentsRuntimeAuthorityProvider) AuthorityForAgentsRuntime(
	ctx context.Context,
	componentID platformruntime.ComponentID,
	workspace string,
	action authority.Action,
) (authority.SystemAuthority, error) {
	if provider == nil || provider.issuer == nil || provider.now == nil {
		return authority.SystemAuthority{}, agents.ErrUnavailable
	}
	if ctx == nil {
		return authority.SystemAuthority{}, fmt.Errorf("agents runtime authority context is required: %w", authority.ErrInvalidScope)
	}
	if err := ctx.Err(); err != nil {
		return authority.SystemAuthority{}, err
	}
	workspace = strings.TrimSpace(workspace)
	if workspace == "" || componentID != agents.DesiredStateReconciliationComponentID || action != agents.ActionReconcileDesiredState {
		return authority.SystemAuthority{}, fmt.Errorf(
			"unregistered Agents runtime authority request: component=%q action=%q: %w",
			componentID, action, authority.ErrActionNotAllowed,
		)
	}
	principal, err := provider.issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject: string(componentID), Class: authority.ClassSystem, Workspace: workspace,
		Actions: []authority.Action{action}, ExpiresAt: provider.now().Add(time.Minute),
	})
	if err != nil {
		return authority.SystemAuthority{}, err
	}
	return provider.issuer.IssueSystem(principal, workspace, action, "registered Agents desired-state reconciliation pass")
}

const prReviewerAuthoritySubject = "serve-pr-reviewer-convergence"

func (capability *AgentsCapability) reviewerAuthority(
	ctx context.Context,
	workspace,
	agentID string,
) (authority.SystemAuthority, error) {
	if capability == nil || capability.issuer == nil {
		return authority.SystemAuthority{}, agents.ErrUnavailable
	}
	if ctx == nil {
		return authority.SystemAuthority{}, fmt.Errorf(
			"pr reviewer authority context is required: %w",
			authority.ErrInvalidScope,
		)
	}
	if err := ctx.Err(); err != nil {
		return authority.SystemAuthority{}, err
	}
	workspace = strings.TrimSpace(workspace)
	agentID = strings.TrimSpace(agentID)
	if workspace == "" || agentID == "" {
		return authority.SystemAuthority{}, fmt.Errorf(
			"pr reviewer authority workspace and agent ID are required: %w",
			authority.ErrInvalidScope,
		)
	}
	principal, err := capability.issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject:   prReviewerAuthoritySubject,
		Class:     authority.ClassSystem,
		Workspace: workspace,
		Actions:   []authority.Action{agents.ActionConvergeManagedReviewer},
		ExpiresAt: time.Now().Add(time.Minute),
	})
	if err != nil {
		return authority.SystemAuthority{}, err
	}
	return capability.issuer.IssueSystem(
		principal,
		workspace,
		agents.ActionConvergeManagedReviewer,
		"converge managed PR reviewer identity "+agentID,
	)
}

const (
	// LocalOpenOperatorSubject is the durable audit subject used when endpoint
	// reachability is the deployment trust boundary.
	LocalOpenOperatorSubject = "local-open-operator"

	localOpenOperatorAuthorityTTL = time.Minute
)

type OperatorAuthorityResolver = workflowcataloghttp.OperatorAuthorityResolver

// ExternalOperatorResolverFactory keeps identity-middleware adaptation at the
// outer server boundary while a capability retains its authority issuer.
type ExternalOperatorResolverFactory func(
	*authority.Issuer,
	error,
) OperatorAuthorityResolver

// LocalOpenOperatorResolver derives one sealed, short-lived authority for one
// route-selected action. Request content cannot select or widen its scope.
type LocalOpenOperatorResolver struct {
	issuer  *authority.Issuer
	actions map[authority.Action]struct{}
}

func NewLocalOpenOperatorResolver(
	issuer *authority.Issuer,
	actions ...authority.Action,
) (*LocalOpenOperatorResolver, error) {
	if issuer == nil {
		return nil, authority.ErrInvalidIssuer
	}
	allowed := make(map[authority.Action]struct{}, len(actions))
	for _, action := range actions {
		if strings.TrimSpace(string(action)) == "" {
			return nil, authority.ErrActionNotAllowed
		}
		allowed[action] = struct{}{}
	}
	if len(allowed) == 0 {
		return nil, authority.ErrActionNotAllowed
	}
	return &LocalOpenOperatorResolver{issuer: issuer, actions: allowed}, nil
}

func (resolver *LocalOpenOperatorResolver) ResolveOperatorAuthority(
	request *http.Request,
	workspace string,
	action authority.Action,
) (authority.OperatorAuthority, error) {
	if request == nil {
		return authority.OperatorAuthority{}, workflowcataloghttp.ErrUnauthenticated
	}
	if resolver == nil || resolver.issuer == nil {
		return authority.OperatorAuthority{}, authority.ErrInvalidIssuer
	}
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return authority.OperatorAuthority{}, authority.ErrInvalidScope
	}
	if _, ok := resolver.actions[action]; !ok {
		return authority.OperatorAuthority{}, authority.ErrActionNotAllowed
	}
	principal, err := resolver.issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject:   LocalOpenOperatorSubject,
		Class:     authority.ClassOperator,
		Workspace: workspace,
		Actions:   []authority.Action{action},
		ExpiresAt: time.Now().Add(localOpenOperatorAuthorityTTL),
	})
	if err != nil {
		return authority.OperatorAuthority{}, err
	}
	return resolver.issuer.IssueOperator(principal, workspace, action)
}

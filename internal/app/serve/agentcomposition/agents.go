// Package agentcomposition assembles the Phase 5 Agents capability from
// owner-scoped ports. The parent serve package retains only the public facade
// that supplies cross-capability dependencies.
package agentcomposition

import (
	"fmt"
	"time"

	"github.com/tysonthomas9/loomcli/internal/app/prreviewer"
	"github.com/tysonthomas9/loomcli/internal/app/serve/agentcomposition/provisioningcomposition"
	"github.com/tysonthomas9/loomcli/internal/app/serve/operatorauth"
	infrafleetdb "github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	agentsfleetdb "github.com/tysonthomas9/loomcli/internal/modules/agents/fleetdb"
	workflowcataloghttp "github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog/httpapi"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	platformruntime "github.com/tysonthomas9/loomcli/internal/platform/runtime"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// AgentsCapability is the composition-owned Phase 5 Agents handle. Consumers
// receive only the public API and request authority resolver; the issuer,
// Fleet transport, and persistence adapter remain private.
type AgentsCapability struct {
	api              agents.API
	issuer           *authority.Issuer
	operatorResolver workflowcataloghttp.OperatorAuthorityResolver
	prReviewers      prreviewer.Commands
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

func (capability *AgentsCapability) OperatorAuthorityResolver() workflowcataloghttp.OperatorAuthorityResolver {
	if capability == nil {
		return nil
	}
	return capability.operatorResolver
}

func (capability *AgentsCapability) PRReviewerProvisioning() prreviewer.Commands {
	if capability == nil {
		return nil
	}
	return capability.prReviewers
}

type AgentsConfig struct {
	FleetDBClient                   *infrafleetdb.Client
	TriggerBindings                 store.TriggerBindingStore
	WorkspaceKey                    string
	WorkspaceLister                 agents.RuntimeWorkspaceLister
	ExternalAuth                    bool
	ExternalOperatorResolverFactory operatorauth.ExternalOperatorResolverFactory
}

type API = agents.API
type PRReviewerCommands = prreviewer.Commands
type OperatorAuthorityResolver = workflowcataloghttp.OperatorAuthorityResolver
type FleetDBClient = infrafleetdb.Client

type AgentProvisioningCapability = provisioningcomposition.Capability
type AgentProvisioningConfig = provisioningcomposition.Config
type AgentProvisioningOwners = provisioningcomposition.Owners

func (capability *AgentsCapability) NewAgentProvisioningCapability(
	client *infrafleetdb.Client,
	config AgentProvisioningConfig,
	owners AgentProvisioningOwners,
) (*AgentProvisioningCapability, error) {
	var owner provisioningcomposition.AgentsOwner
	if capability != nil {
		owner = provisioningcomposition.AgentsOwner{
			Commands: capability.api, Issuer: capability.issuer,
			OperatorResolver: capability.operatorResolver,
		}
	}
	return provisioningcomposition.New(owner, client, config, owners)
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
		adapter, adapter, adapter, adapter, adapter, adapter, adapter,
		newAgentBindingStateSource(config.TriggerBindings), admission,
	)
	if err != nil {
		return nil, err
	}
	resolver, err := composeAgentsOperatorResolver(config, issuer)
	if err != nil {
		return nil, err
	}
	reviewers, err := prreviewer.New(service, &prReviewerAuthorityProvider{
		issuer: issuer,
		now:    time.Now,
	})
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
		prReviewers: reviewers,
		runtime:     []platformruntime.Registration{runtimeRegistration},
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
	resolver, err := operatorauth.NewLocalOpenOperatorResolver(issuer, agentsOperatorActions()...)
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
		provisioningcomposition.ActionBeginProvisioning,
	}
}

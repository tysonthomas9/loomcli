// Package agentcomposition assembles the Phase 5 Agents capability from
// owner-scoped ports. The parent serve package retains only the public facade
// that supplies cross-capability dependencies.
package agentcomposition

import (
	"fmt"
	"time"

	"github.com/tysonthomas9/loomcli/internal/app/agentscompat"
	"github.com/tysonthomas9/loomcli/internal/app/prreviewer"
	"github.com/tysonthomas9/loomcli/internal/app/serve/agentcomposition/provisioningcomposition"
	"github.com/tysonthomas9/loomcli/internal/app/serve/operatorauth"
	"github.com/tysonthomas9/loomcli/internal/infra/agentscompatstore"
	infrafleetdb "github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	agentsfleetdb "github.com/tysonthomas9/loomcli/internal/modules/agents/fleetdb"
	workflowcataloghttp "github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog/httpapi"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// AgentsCapability is the composition-owned Phase 5 Agents handle. Consumers
// receive only the public API and request authority resolver; the issuer,
// Fleet transport, and persistence adapter remain private.
type AgentsCapability struct {
	api              agents.API
	compatibility    agents.CompatibilityAPI
	managed          agentscompat.ManagedCommands
	parentBindings   agentscompat.ParentBindingCommands
	retirements      agentscompat.ManagedRetirements
	issuer           *authority.Issuer
	operatorResolver workflowcataloghttp.OperatorAuthorityResolver
	prReviewers      prreviewer.Commands
}

func (capability *AgentsCapability) AgentsAPI() agents.API {
	if capability == nil {
		return nil
	}
	return capability.api
}

func (capability *AgentsCapability) CompatibilityAPI() agents.CompatibilityAPI {
	if capability == nil {
		return nil
	}
	return capability.compatibility
}

func (capability *AgentsCapability) ManagedCompatibility() agentscompat.ManagedCommands {
	if capability == nil {
		return nil
	}
	return capability.managed
}

func (capability *AgentsCapability) ParentBindingCommands() agentscompat.ParentBindingCommands {
	if capability == nil {
		return nil
	}
	return capability.parentBindings
}

func (capability *AgentsCapability) ManagedRetirements() agentscompat.ManagedRetirements {
	if capability == nil {
		return nil
	}
	return capability.retirements
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
	CompatibilityRoles              store.RoleStore
	CompatibilityAgentServices      store.AgentServiceStore
	CompatibilityAssignments        store.AgentStore
	ExternalAuth                    bool
	ExternalOperatorResolverFactory operatorauth.ExternalOperatorResolverFactory
}

type API = agents.API
type CompatibilityAPI = agents.CompatibilityAPI
type ManagedCommands = agentscompat.ManagedCommands
type ParentBindingCommands = agentscompat.ParentBindingCommands
type ManagedRetirements = agentscompat.ManagedRetirements
type PRReviewerCommands = prreviewer.Commands
type OperatorAuthorityResolver = workflowcataloghttp.OperatorAuthorityResolver
type FleetDBClient = infrafleetdb.Client
type RoleStore = store.RoleStore
type AgentServiceStore = store.AgentServiceStore
type AgentStore = store.AgentStore

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
		adapter, adapter, adapter, adapter, adapter, adapter, adapter, admission,
	)
	if err != nil {
		return nil, err
	}
	var compatibility agents.CompatibilityAPI
	var managed agentscompat.ManagedCommands
	var parentBindings agentscompat.ParentBindingCommands
	var retirements agentscompat.ManagedRetirements
	compatibilityConfigured := config.CompatibilityRoles != nil ||
		config.CompatibilityAgentServices != nil ||
		config.CompatibilityAssignments != nil
	if compatibilityConfigured {
		compatibilityPersistence, persistenceErr := agentscompatstore.New(
			config.CompatibilityRoles,
			config.CompatibilityAgentServices,
			config.CompatibilityAssignments,
		)
		if persistenceErr != nil {
			return nil, fmt.Errorf("compose Agents compatibility persistence: %w", persistenceErr)
		}
		compatibility, err = agentscompat.NewAPI(compatibilityPersistence, admission)
		if err != nil {
			return nil, fmt.Errorf("compose Agents compatibility API: %w", err)
		}
		managed, err = agentscompat.NewManagedCommandsWithIssuer(compatibility, issuer)
		if err != nil {
			return nil, err
		}
		parentBindings, err = agentscompat.NewParentBindingCommands(compatibility, issuer)
		if err != nil {
			return nil, err
		}
		retirements, err = agentscompat.NewManagedRetirements(compatibility, issuer)
		if err != nil {
			return nil, err
		}
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
	return &AgentsCapability{
		api: service, compatibility: compatibility, managed: managed, parentBindings: parentBindings,
		retirements: retirements, issuer: issuer, operatorResolver: resolver,
		prReviewers: reviewers,
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
		agents.ActionCreateSupervisedAssignment,
		agents.ActionUpdateSupervisedAssignmentIntent,
		agents.ActionRetireSupervisedAssignment,
		provisioningcomposition.ActionBeginProvisioning,
	}
}

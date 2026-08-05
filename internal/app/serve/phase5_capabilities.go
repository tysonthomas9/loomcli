package serve

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/app/serve/agentcomposition"
	"github.com/tysonthomas9/loomcli/internal/app/serve/interactioncomposition"
	"github.com/tysonthomas9/loomcli/internal/app/serve/sourcecontrolcomposition"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	platformruntime "github.com/tysonthomas9/loomcli/internal/platform/runtime"
)

// AgentsCapability is the composition-owned Phase 5 Agents handle. The
// implementation lives in a bounded child package; this facade retains the
// existing public serve API and supplies only explicit cross-capability owner
// dependencies.
type AgentsCapability struct {
	capability *agentcomposition.AgentsCapability
}

func (capability *AgentsCapability) AgentsAPI() agentcomposition.API {
	if capability == nil {
		return nil
	}
	return capability.capability.AgentsAPI()
}

func (capability *AgentsCapability) OperatorAuthorityResolver() agentcomposition.OperatorAuthorityResolver {
	if capability == nil {
		return nil
	}
	return capability.capability.OperatorAuthorityResolver()
}

func (capability *AgentsCapability) PRReviewerProvisioning() agentcomposition.PRReviewerCommands {
	if capability == nil {
		return nil
	}
	return capability.capability.PRReviewerProvisioning()
}

func (capability *AgentsCapability) RuntimeRegistrations() []platformruntime.Registration {
	if capability == nil {
		return nil
	}
	return capability.capability.RuntimeRegistrations()
}

type AgentsConfig = agentcomposition.AgentsConfig

func NewAgentsCapability(config AgentsConfig) (*AgentsCapability, error) {
	composed, err := agentcomposition.NewAgentsCapability(config)
	if err != nil {
		return nil, err
	}
	return &AgentsCapability{capability: composed}, nil
}

type AgentProvisioningCapability = agentcomposition.AgentProvisioningCapability
type AgentProvisioningConfig = agentcomposition.AgentProvisioningConfig

// SourceControlCapability preserves serve's public capability facade while
// keeping Git, Connectors, and FleetDB transport details in their bounded
// composition package.
type SourceControlCapability struct {
	capability *sourcecontrolcomposition.SourceControlCapability
	issuer     *authority.Issuer
	grants     sourcecontrolcomposition.GrantCommands
}

func (capability *SourceControlCapability) SourceControlAPI() sourcecontrolcomposition.API {
	if capability == nil {
		return nil
	}
	return capability.capability.SourceControlAPI()
}

func (capability *SourceControlCapability) SourceControlMaterializer() sourcecontrolcomposition.Materializer {
	if capability == nil {
		return nil
	}
	return capability.capability.SourceControlMaterializer()
}

func (capability *SourceControlCapability) RepositoryAdmissionMaterializer() sourcecontrolcomposition.RepositoryAdmissionMaterializer {
	if capability == nil {
		return nil
	}
	return capability.capability.RepositoryAdmissionMaterializer()
}

func (capability *SourceControlCapability) PrepareRepositoryAdmissionCheckout(
	ctx context.Context,
	command sourcecontrolcomposition.RepositoryAdmissionCheckoutCommand,
) (*sourcecontrolcomposition.PreparedRepositoryCheckout, error) {
	if capability == nil {
		return nil, sourcecontrolcomposition.ErrUnavailable
	}
	return capability.capability.PrepareRepositoryAdmissionCheckout(ctx, command)
}

func (capability *SourceControlCapability) PrepareTaskCheckout(
	ctx context.Context,
	command sourcecontrolcomposition.TaskCheckoutCommand,
) (*sourcecontrolcomposition.TaskCheckout, error) {
	if capability == nil {
		return nil, sourcecontrolcomposition.ErrUnavailable
	}
	return capability.capability.PrepareTaskCheckout(ctx, command)
}

func (capability *SourceControlCapability) PreparePullRequestCheckout(
	ctx context.Context,
	command sourcecontrolcomposition.PullRequestCheckoutCommand,
) (*sourcecontrolcomposition.PullRequestCheckout, error) {
	if capability == nil {
		return nil, sourcecontrolcomposition.ErrUnavailable
	}
	return capability.capability.PreparePullRequestCheckout(ctx, command)
}

func (catalog *WorkflowCatalogCapability) NewSourceControlCapability(
	localSettingsDir string,
	repositories sourcecontrolcomposition.RepositoryResolver,
) (*SourceControlCapability, error) {
	var issuer *authority.Issuer
	if catalog != nil {
		issuer = catalog.issuer
	}
	composed, err := sourcecontrolcomposition.NewSourceControlCapability(
		localSettingsDir,
		repositories,
		issuer,
	)
	if err != nil {
		return nil, err
	}
	return &SourceControlCapability{
		capability: composed, issuer: issuer,
		grants: composed.ProvisioningGrantCommands(),
	}, nil
}

func (catalog *WorkflowCatalogCapability) NewSourceControlCapabilityWithFleetDB(
	localSettingsDir string,
	repositories sourcecontrolcomposition.RepositoryResolver,
	client *sourcecontrolcomposition.FleetDBClient,
) (*SourceControlCapability, error) {
	var issuer *authority.Issuer
	if catalog != nil {
		issuer = catalog.issuer
	}
	composed, err := sourcecontrolcomposition.NewSourceControlCapabilityWithFleetDB(
		localSettingsDir,
		repositories,
		client,
		issuer,
	)
	if err != nil {
		return nil, err
	}
	return &SourceControlCapability{
		capability: composed, issuer: issuer,
		grants: composed.ProvisioningGrantCommands(),
	}, nil
}

func (capability *AgentsCapability) NewAgentProvisioningCapability(
	catalog *WorkflowCatalogCapability,
	sourceControl *SourceControlCapability,
	client *agentcomposition.FleetDBClient,
	config AgentProvisioningConfig,
) (*AgentProvisioningCapability, error) {
	var composed *agentcomposition.AgentsCapability
	if capability != nil {
		composed = capability.capability
	}
	owners := agentcomposition.AgentProvisioningOwners{}
	if catalog != nil {
		owners.AutomationIssuer = catalog.issuer
		if catalog.automation != nil {
			owners.Bindings = catalog.automation.ProvisioningBindingCommands()
		}
	}
	if sourceControl != nil {
		owners.ConnectorsIssuer = sourceControl.issuer
		owners.Grants = sourceControl.grants
	}
	return composed.NewAgentProvisioningCapability(client, config, owners)
}

type InteractionCapability = interactioncomposition.InteractionCapability
type InteractionDependencies = interactioncomposition.InteractionDependencies
type InteractionSessionAuthorityResolver = interactioncomposition.InteractionSessionAuthorityResolver

type InteractionConfig = interactioncomposition.InteractionConfig

func NewInteractionCapability(
	config InteractionConfig,
	dependencies InteractionDependencies,
) (*InteractionCapability, error) {
	return interactioncomposition.NewInteractionCapability(config, dependencies)
}

func (catalog *WorkflowCatalogCapability) NewInteractionCapability(
	config InteractionConfig,
	dependencies InteractionDependencies,
) (*InteractionCapability, error) {
	if catalog == nil {
		return interactioncomposition.NewInteractionCapabilityWithIssuer(config, dependencies, nil)
	}
	return interactioncomposition.NewInteractionCapabilityWithIssuer(
		config,
		dependencies,
		catalog.issuer,
	)
}

func NewInteractionCapabilityWithFleetDB(
	config InteractionConfig,
	client *interactioncomposition.FleetDBClient,
) (*InteractionCapability, error) {
	return interactioncomposition.NewInteractionCapabilityWithFleetDB(
		config,
		client,
	)
}

func (catalog *WorkflowCatalogCapability) NewInteractionCapabilityWithFleetDB(
	config InteractionConfig,
	client *interactioncomposition.FleetDBClient,
) (*InteractionCapability, error) {
	if catalog == nil {
		return interactioncomposition.NewInteractionCapabilityWithFleetDBIssuer(config, client, nil)
	}
	return interactioncomposition.NewInteractionCapabilityWithFleetDBIssuer(
		config,
		client,
		catalog.issuer,
	)
}

func (catalog *WorkflowCatalogCapability) NewInteractionSessionAuthorityResolver(
	client *interactioncomposition.FleetDBClient,
) (InteractionSessionAuthorityResolver, error) {
	if catalog == nil {
		return interactioncomposition.NewInteractionSessionAuthorityResolver(client, nil)
	}
	return interactioncomposition.NewInteractionSessionAuthorityResolver(client, catalog.issuer)
}

func ComposeInteractionChat(
	capability *InteractionCapability,
	runtime interactioncomposition.ChatRuntime,
) error {
	return interactioncomposition.ComposeInteractionChat(capability, runtime)
}

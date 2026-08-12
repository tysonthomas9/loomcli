package serveadapter

import (
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/app/agentprovisioning"
	appserve "github.com/tysonthomas9/loomcli/internal/app/serve"
	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/infra/interactionchat"
	leadcontrol "github.com/tysonthomas9/loomcli/internal/infra/interactionlead"
	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	workflowcataloghttp "github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog/httpapi"
	platformruntime "github.com/tysonthomas9/loomcli/internal/platform/runtime"
	"github.com/tysonthomas9/loomcli/internal/webui"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

type InteractionConfig struct {
	StoreHandle           *bootstrap.StoreHandle
	WorkflowCatalogModule *WorkflowCatalogModule
	AgentQueries          interactionchat.AgentQueries
	WorkspaceLister       agentprovisioning.WorkspaceLister
	Workspace             string
	ExternalAuth          bool
	WorkspaceRoleResolver middleware.WorkspaceRoleResolver
}

// InteractionCapability publishes only the web-facing command/query handle
// and runtime registrations. The issuer, one-use lease credentials, FleetDB
// transport, and persistence adapter stay inside app/serve composition.
type InteractionCapability struct {
	capability *appserve.InteractionCapability
}

var _ webui.InteractionCapability = (*InteractionCapability)(nil)

func (capability *InteractionCapability) InteractionAPI() interaction.API {
	if capability == nil || capability.capability == nil {
		return nil
	}
	return capability.capability.InteractionAPI()
}

func (capability *InteractionCapability) SessionQueries() interaction.SessionQueries {
	if capability == nil || capability.capability == nil {
		return nil
	}
	return capability.capability.SessionQueries()
}

func (capability *InteractionCapability) OperatorAuthorityResolver() workflowcataloghttp.OperatorAuthorityResolver {
	if capability == nil || capability.capability == nil {
		return nil
	}
	return capability.capability.OperatorAuthorityResolver()
}

func (capability *InteractionCapability) SessionAuthorityResolver() webui.InteractionSessionAuthorityResolver {
	if capability == nil || capability.capability == nil {
		return nil
	}
	return capability.capability.SessionAuthorityResolver()
}

func (capability *InteractionCapability) InboxEnqueuer() interaction.InboxEnqueuer {
	if capability == nil || capability.capability == nil {
		return nil
	}
	return capability.capability.InboxEnqueuer()
}

func (capability *InteractionCapability) ForceInterrupter() interaction.ForceInterrupter {
	if capability == nil || capability.capability == nil {
		return nil
	}
	return capability.capability.ForceInterrupter()
}

func (capability *InteractionCapability) ChatAPI() interaction.ChatAPI {
	if capability == nil || capability.capability == nil {
		return nil
	}
	return capability.capability.ChatAPI()
}

func (capability *InteractionCapability) ChatMessenger() interaction.ChatMessenger {
	if capability == nil || capability.capability == nil {
		return nil
	}
	return capability.capability.ChatMessenger()
}

func (capability *InteractionCapability) RuntimeRegistrations() []platformruntime.Registration {
	if capability == nil || capability.capability == nil {
		return nil
	}
	return capability.capability.RuntimeRegistrations()
}

func BuildInteractionCapability(config InteractionConfig) (*InteractionCapability, error) { //nolint:funlen // Composition validates and wires all fenced Interaction ports before runtime registration.
	if config.StoreHandle == nil || config.StoreHandle.Store == nil ||
		config.StoreHandle.FleetDBClient() == nil {
		return nil, fmt.Errorf("compose Interaction: FleetDB Store handle is required")
	}
	if config.AgentQueries == nil {
		return nil, fmt.Errorf(
			"compose Interaction: Agents queries are required: %w",
			interaction.ErrUnavailable,
		)
	}
	interactionConfig := appserve.InteractionConfig{
		WorkspaceKey:    config.Workspace,
		WorkspaceLister: config.WorkspaceLister,
		ExternalAuth:    config.ExternalAuth,
		ExternalOperatorResolverFactory: newExternalOperatorResolverFactory(
			config.WorkspaceRoleResolver,
		),
	}
	var (
		capability *appserve.InteractionCapability
		err        error
	)
	if config.WorkflowCatalogModule != nil &&
		config.WorkflowCatalogModule.CapabilityAvailable() {
		capability, err = config.WorkflowCatalogModule.
			NewInteractionCapabilityWithFleetDB(
				interactionConfig,
				config.StoreHandle.FleetDBClient(),
			)
	} else {
		capability, err = appserve.NewInteractionCapabilityWithFleetDB(
			interactionConfig,
			config.StoreHandle.FleetDBClient(),
		)
	}
	if err != nil {
		return nil, err
	}
	leadDependencies, err := leadcontrol.NewInteractionChatDependencies(leadcontrol.RuntimeDependencies{
		Sessions: config.StoreHandle.Store, InboxMessages: config.StoreHandle.Store.AgentInboxMessages(),
		AgentServices: config.StoreHandle.Store.AgentServices(), WorkerProfiles: config.StoreHandle.Store.WorkerProfiles(), Roles: config.StoreHandle.Store.Roles(),
	})
	if err != nil {
		return nil, err
	}
	chatRuntime, err := interactionchat.New(
		leadDependencies,
		capability.InboxEnqueuer(),
		config.AgentQueries,
	)
	if err != nil {
		return nil, err
	}
	if err := appserve.ComposeInteractionChat(
		capability,
		chatRuntime,
	); err != nil {
		return nil, err
	}
	return &InteractionCapability{
		capability: capability,
	}, nil
}

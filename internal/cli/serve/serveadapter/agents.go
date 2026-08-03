package serveadapter

import (
	"fmt"
	"os"

	"github.com/tysonthomas9/loomcli/internal/app/agentprovisioning"
	appserve "github.com/tysonthomas9/loomcli/internal/app/serve"
	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
	platformruntime "github.com/tysonthomas9/loomcli/internal/platform/runtime"
	"github.com/tysonthomas9/loomcli/internal/webui"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

type AgentsConfig struct {
	StoreHandle           *bootstrap.StoreHandle
	WorkflowCatalogModule *WorkflowCatalogModule
	LocalSettingsDir      string
	Workspace             string
	ExternalAuth          bool
	WorkspaceRoleResolver middleware.WorkspaceRoleResolver
	RepositoryAdmissions  sourcecontrol.RepositoryAdmissionLocalResolver
}

// AgentsCapability deliberately embeds only the web-facing public handle.
// CLI composition cannot recover the issuer, Fleet transport, or persistence
// adapter from this wrapper.
type AgentsCapability struct {
	webui.AgentsCapability
	capability    *appserve.AgentsCapability
	provisioning  *appserve.AgentProvisioningCapability
	sourceControl *appserve.SourceControlCapability
	workspaceList agentprovisioning.WorkspaceLister
}

func (capability *AgentsCapability) AgentProvisioningCommands() agentprovisioning.Commands {
	if capability == nil || capability.provisioning == nil {
		return nil
	}
	return capability.provisioning.AgentProvisioningCommands()
}

func (capability *AgentsCapability) RuntimeRegistrations() []platformruntime.Registration {
	if capability == nil {
		return nil
	}
	registrations := capability.capability.RuntimeRegistrations()
	if capability.provisioning != nil {
		registrations = append(registrations, capability.provisioning.RuntimeRegistrations()...)
	}
	return registrations
}

func (capability *AgentsCapability) SourceControlMaterializer() sourcecontrol.Materializer {
	if capability == nil || capability.sourceControl == nil {
		return nil
	}
	return capability.sourceControl.SourceControlMaterializer()
}

func (capability *AgentsCapability) RepositoryAdmissionMaterializer() sourcecontrol.RepositoryAdmissionMaterializer {
	if capability == nil || capability.sourceControl == nil {
		return nil
	}
	return capability.sourceControl.RepositoryAdmissionMaterializer()
}

func (capability *AgentsCapability) WorkspaceLister() agentprovisioning.WorkspaceLister {
	if capability == nil {
		return nil
	}
	return capability.workspaceList
}

func BuildAgentsCapability(config AgentsConfig) (*AgentsCapability, error) { //nolint:funlen // Composition validates and wires every Agents owner port before exposing the capability.
	if config.StoreHandle == nil || config.StoreHandle.Store == nil ||
		config.StoreHandle.FleetDBClient() == nil {
		return nil, fmt.Errorf("compose Agents: FleetDB Store handle is required")
	}
	capability, err := appserve.NewAgentsCapability(appserve.AgentsConfig{
		FleetDBClient:   config.StoreHandle.FleetDBClient(),
		TriggerBindings: config.StoreHandle.Store.TriggerBindings(),
		WorkspaceKey:    config.Workspace,
		WorkspaceLister: newAgentProvisioningWorkspaceLister(config.StoreHandle.Store.Workspaces()),
		ExternalAuth:    config.ExternalAuth,
		ExternalOperatorResolverFactory: newExternalOperatorResolverFactory(
			config.WorkspaceRoleResolver,
		),
	})
	if err != nil {
		return nil, err
	}
	workspaceLister := newAgentProvisioningWorkspaceLister(
		config.StoreHandle.Store.Workspaces(),
	)
	result := &AgentsCapability{
		AgentsCapability: capability,
		capability:       capability,
		workspaceList:    workspaceLister,
	}
	if config.WorkflowCatalogModule == nil ||
		!config.WorkflowCatalogModule.CapabilityAvailable() {
		return result, nil
	}
	repositories := newSourceControlRepositoryResolverWithAdmissions(
		config.StoreHandle.Store.Workspaces(),
		config.StoreHandle.Store.Repos(),
		config.StoreHandle.FleetDBClient().RepositoryAdmissions(),
		config.RepositoryAdmissions,
		bootstrap.WorkspaceDir,
		os.MkdirAll,
	)
	if repositories == nil {
		return nil, fmt.Errorf("compose AgentProvisioning Source Control repository resolver")
	}
	sourceControl, err := config.WorkflowCatalogModule.
		NewSourceControlCapabilityWithFleetDB(
			config.LocalSettingsDir,
			repositories,
			config.StoreHandle.FleetDBClient(),
		)
	if err != nil {
		return nil, err
	}
	result.sourceControl = sourceControl
	provisioning, err := config.WorkflowCatalogModule.NewAgentProvisioningCapability(
		capability,
		sourceControl,
		config.StoreHandle.FleetDBClient(),
		appserve.AgentProvisioningConfig{
			WorkspaceKey:    config.Workspace,
			WorkspaceLister: workspaceLister,
		},
	)
	if err != nil {
		return nil, err
	}
	result.provisioning = provisioning
	return result, nil
}

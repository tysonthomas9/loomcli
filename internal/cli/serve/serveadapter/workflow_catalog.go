package serveadapter

import (
	"context"

	appserve "github.com/tysonthomas9/loomcli/internal/app/serve"
	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli/serve/serveadapter/catalogcomposition"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

const WorkflowCatalogEnabledEnv = catalogcomposition.WorkflowCatalogEnabledEnv
const AutomationEnabledEnv = catalogcomposition.AutomationEnabledEnv

type WorkflowCatalogConfig = catalogcomposition.WorkflowCatalogConfig
type WorkflowCatalogModule = catalogcomposition.WorkflowCatalogModule
type AutomationCapability = catalogcomposition.AutomationCapability

func WorkflowCatalogEnabled(externalAuth, roleResolverAvailable bool) (bool, error) {
	return catalogcomposition.WorkflowCatalogEnabled(externalAuth, roleResolverAvailable)
}

func AutomationEnabled(externalAuth, roleResolverAvailable bool) (bool, error) {
	return catalogcomposition.AutomationEnabled(externalAuth, roleResolverAvailable)
}

func RequiredFleetDBCapabilities(externalAuth, roleResolverAvailable bool) ([]string, error) {
	return catalogcomposition.RequiredFleetDBCapabilities(externalAuth, roleResolverAvailable)
}

func RefreshBoundPromptAgentWorkflows(
	ctx context.Context,
	handle *bootstrap.StoreHandle,
	module *WorkflowCatalogModule,
) error {
	return catalogcomposition.RefreshBoundPromptAgentWorkflows(ctx, handle, module)
}

func BuildExecutionCapability(
	module *WorkflowCatalogModule,
	handle *bootstrap.StoreHandle,
	agentQueries catalogcomposition.AgentIdentityQueries,
) (*appserve.ExecutionCapability, error) {
	return catalogcomposition.BuildExecutionCapability(module, handle, agentQueries)
}

func BuildWorkflowCatalogModule(config WorkflowCatalogConfig) (*WorkflowCatalogModule, error) {
	return catalogcomposition.BuildWorkflowCatalogModule(config)
}

func newExternalOperatorResolverFactory(
	resolveRole middleware.WorkspaceRoleResolver,
) appserve.ExternalOperatorResolverFactory {
	return catalogcomposition.NewExternalOperatorResolverFactory(resolveRole)
}

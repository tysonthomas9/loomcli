package agentmodules

import (
	"context"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/app/webhookingestion"
	"github.com/tysonthomas9/loomcli/internal/app/workflowbinding"
	trigger "github.com/tysonthomas9/loomcli/internal/infra/automationruntime"
	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	workflowcataloghttp "github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog/httpapi"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/triggerbindings"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/webhooks"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// automationRouteModule is the route registration contract shared by the
// automation-facing composition group.
type automationRouteModule interface {
	Register(*http.ServeMux)
}

// Capabilities contains the Automation-owned application ports needed by the
// automation route adapter.
type automationRouteCapabilities struct {
	AutomationBindings automation.BindingOperations
	WorkflowBinding    *workflowbinding.Workflow
	AutomationAudit    automation.AuditQueries
	AutomationWebhook  *webhookingestion.Workflow
	AutomationOperator workflowcataloghttp.OperatorAuthorityResolver
}

// Deps contains the Automation-owned workflows and narrow legacy ports needed
// to compose webhook ingestion and trigger bindings.
type automationRouteDeps struct {
	Capabilities    automationRouteCapabilities
	Awaits          store.AwaitStore
	DriverRuns      store.DriverRunStore
	AwaitResolver   store.AtomicAwaitStore
	TriggerBindings store.TriggerBindingStore
	ConnectorGrants store.ConnectorGrantStore
	AgentServices   store.AgentServiceStore
}

// eventAwaitDispatcher is the shared post-admission await notification seam.
type eventAwaitDispatcher interface {
	Dispatch(context.Context, string, trigger.AwaitDispatchEvent) (*trigger.AwaitDispatchResult, error)
}

// bindingGrantCompatibility is the connector cleanup seam shared with agent
// deletion while Connectors remains a later migration phase.
type bindingGrantCompatibility interface {
	RevokeBindingGrants(context.Context, string, string) (int, error)
}

// Modules names each route group so the parent composition can preserve the
// long-standing registration order while this package owns its dependencies.
type automationRouteModules struct {
	Webhooks             automationRouteModule
	TriggerBindings      automationRouteModule
	EventAwaits          eventAwaitDispatcher
	BindingGrants        bindingGrantCompatibility
	WorkspaceFromContext func(context.Context) string
}

// New composes the Automation and binding-facing HTTP modules.
func newAutomationRouteModules(deps automationRouteDeps) automationRouteModules {
	var eventAwaits eventAwaitDispatcher
	if deps.Awaits != nil && deps.DriverRuns != nil && deps.AwaitResolver != nil {
		eventAwaits = trigger.NewAwaitMatcherWithResolver(deps.Awaits, deps.DriverRuns, deps.AwaitResolver)
	}
	connectorCompatibility := newStoreConnectorCompatibility(deps.TriggerBindings, deps.ConnectorGrants)
	agentIdentityCompatibility := newStoreAgentIdentityCompatibility(deps.AgentServices)

	return automationRouteModules{
		Webhooks: webhooks.New(webhooks.Config{
			Workflow:   deps.Capabilities.AutomationWebhook,
			Automation: deps.Capabilities.AutomationAudit,
			Awaits:     eventAwaits,
		}),
		TriggerBindings: triggerbindings.New(triggerbindings.Config{
			CreateWorkflow: deps.Capabilities.WorkflowBinding,
			Commands:       deps.Capabilities.AutomationBindings, Queries: deps.Capabilities.AutomationBindings,
			ManualDispatch:       deps.Capabilities.AutomationBindings,
			OperatorAuthority:    deps.Capabilities.AutomationOperator,
			WorkspaceFromContext: middleware.WorkspaceFromContext, Runs: deps.DriverRuns,
			Connectors: connectorCompatibility, AgentIdentities: agentIdentityCompatibility,
		}),
		EventAwaits:          eventAwaits,
		BindingGrants:        connectorCompatibility,
		WorkspaceFromContext: middleware.WorkspaceFromContext,
	}
}

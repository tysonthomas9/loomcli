package agentmodules

import (
	"context"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/app/webhookingestion"
	"github.com/tysonthomas9/loomcli/internal/app/workflowbinding"
	trigger "github.com/tysonthomas9/loomcli/internal/infra/automationruntime"
	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	connectorsmodule "github.com/tysonthomas9/loomcli/internal/modules/connectors"
	workflowcataloghttp "github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog/httpapi"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/triggerbindings"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/webhooks"
	"github.com/tysonthomas9/loomcli/internal/webui/readprojection"
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

// Deps contains the owner interfaces needed to compose webhook ingestion and
// trigger bindings.
type automationRouteDeps struct {
	Capabilities    automationRouteCapabilities
	Awaits          store.AwaitStore
	DriverRuns      store.DriverRunStore
	AwaitResolver   store.AtomicAwaitStore
	Connectors      connectorsmodule.BindingGrantLifecycle
	AgentIdentities agents.IdentityQueries
}

// eventAwaitDispatcher is the shared post-admission await notification seam.
type eventAwaitDispatcher interface {
	Dispatch(context.Context, string, trigger.AwaitDispatchEvent) (*trigger.AwaitDispatchResult, error)
}

type bindingGrantCleanup interface {
	RevokeBindingGrants(context.Context, string, string) (int, error)
}

type connectorBindingGrantCleanup struct {
	lifecycle connectorsmodule.BindingGrantLifecycle
}

func (cleanup connectorBindingGrantCleanup) RevokeBindingGrants(
	ctx context.Context,
	workspace, bindingID string,
) (int, error) {
	return cleanup.lifecycle.RevokeBindingGrants(ctx, connectorsmodule.BindingGrantCleanupCommand{
		WorkspaceKey: workspace,
		BindingID:    bindingID,
	})
}

// Modules names each route group so the parent composition can preserve the
// long-standing registration order while this package owns its dependencies.
type automationRouteModules struct {
	Webhooks             automationRouteModule
	TriggerBindings      automationRouteModule
	EventAwaits          eventAwaitDispatcher
	BindingGrants        bindingGrantCleanup
	BindingRuns          triggerbindings.RunQueries
	WorkspaceFromContext func(context.Context) string
}

// New composes the Automation and binding-facing HTTP modules.
func newAutomationRouteModules(deps automationRouteDeps) automationRouteModules {
	var eventAwaits eventAwaitDispatcher
	if deps.Awaits != nil && deps.DriverRuns != nil && deps.AwaitResolver != nil {
		eventAwaits = trigger.NewAwaitMatcherWithResolver(deps.Awaits, deps.DriverRuns, deps.AwaitResolver)
	}
	var grantCleanup bindingGrantCleanup
	if deps.Connectors != nil {
		grantCleanup = connectorBindingGrantCleanup{lifecycle: deps.Connectors}
	}
	var runQueries triggerbindings.RunQueries
	if deps.DriverRuns != nil {
		runQueries = readprojection.NewBindingRunReader(deps.DriverRuns)
	}
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
			WorkspaceFromContext: middleware.WorkspaceFromContext, Runs: runQueries,
			Connectors: deps.Connectors, AgentIdentities: deps.AgentIdentities,
		}),
		EventAwaits:          eventAwaits,
		BindingGrants:        grantCleanup,
		BindingRuns:          runQueries,
		WorkspaceFromContext: middleware.WorkspaceFromContext,
	}
}

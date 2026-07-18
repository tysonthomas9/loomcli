package automationroutes

import (
	"context"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/trigger"
	"github.com/tysonthomas9/loomcli/internal/webui/agentmodules/routecontracts"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/triggerbindings"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/webhooks"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// Module is the workspace route registration contract shared by web modules.
type Module interface {
	Register(*http.ServeMux)
}

// Deps contains the Automation-owned workflows and narrow legacy ports needed
// to compose webhook ingestion and trigger bindings.
type Deps struct {
	Capabilities    routecontracts.Deps
	Awaits          store.AwaitStore
	DriverRuns      store.DriverRunStore
	AwaitResolver   store.AtomicAwaitStore
	TriggerBindings store.TriggerBindingStore
	ConnectorGrants store.ConnectorGrantStore
}

// EventAwaitDispatcher is the shared post-admission await notification seam.
type EventAwaitDispatcher interface {
	Dispatch(context.Context, string, trigger.AwaitDispatchEvent) (*trigger.AwaitDispatchResult, error)
}

// BindingGrantCompatibility is the connector cleanup seam shared with agent
// deletion while Connectors remains a later migration phase.
type BindingGrantCompatibility interface {
	RevokeBindingGrants(context.Context, string, string) (int, error)
}

// Modules names each route group so the parent composition can preserve the
// long-standing registration order while this package owns its dependencies.
type Modules struct {
	Webhooks             Module
	TriggerBindings      Module
	EventAwaits          EventAwaitDispatcher
	BindingGrants        BindingGrantCompatibility
	WorkspaceFromContext func(context.Context) string
}

// New composes the Automation and binding-facing HTTP modules.
func New(deps Deps) Modules {
	var eventAwaits EventAwaitDispatcher
	if deps.Awaits != nil && deps.DriverRuns != nil && deps.AwaitResolver != nil {
		eventAwaits = trigger.NewAwaitMatcherWithResolver(deps.Awaits, deps.DriverRuns, deps.AwaitResolver)
	}
	connectorCompatibility := newStoreConnectorCompatibility(deps.TriggerBindings, deps.ConnectorGrants)

	return Modules{
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
			Connectors: connectorCompatibility,
		}),
		EventAwaits:          eventAwaits,
		BindingGrants:        connectorCompatibility,
		WorkspaceFromContext: middleware.WorkspaceFromContext,
	}
}

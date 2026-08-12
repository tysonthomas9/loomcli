package automationroutes

import (
	"context"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/app/webhookingestion"
	"github.com/tysonthomas9/loomcli/internal/app/workflowbinding"
	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	workflowcataloghttp "github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog/httpapi"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/trigger"
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
	AutomationBindings automation.BindingOperations
	WorkflowBinding    *workflowbinding.Workflow
	AutomationAudit    automation.AuditQueries
	AutomationWebhook  *webhookingestion.Workflow
	AutomationOperator workflowcataloghttp.OperatorAuthorityResolver
	Awaits             store.AwaitStore
	DriverRuns         store.DriverRunStore
	TriggerBindings    store.TriggerBindingStore
	ConnectorGrants    store.ConnectorGrantStore
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
	var eventAwaits *trigger.AwaitMatcher
	if deps.Awaits != nil && deps.DriverRuns != nil {
		eventAwaits = trigger.NewAwaitMatcher(deps.Awaits, deps.DriverRuns)
	}
	connectorCompatibility := newStoreConnectorCompatibility(deps.TriggerBindings, deps.ConnectorGrants)

	return Modules{
		Webhooks: webhooks.New(webhooks.Config{
			Workflow: deps.AutomationWebhook, Automation: deps.AutomationAudit, Awaits: eventAwaits,
		}),
		TriggerBindings: triggerbindings.New(triggerbindings.Config{
			CreateWorkflow: deps.WorkflowBinding,
			Commands:       deps.AutomationBindings, Queries: deps.AutomationBindings,
			ManualDispatch: deps.AutomationBindings, OperatorAuthority: deps.AutomationOperator,
			WorkspaceFromContext: middleware.WorkspaceFromContext, Runs: deps.DriverRuns,
			Connectors: connectorCompatibility,
		}),
		EventAwaits:          eventAwaits,
		BindingGrants:        connectorCompatibility,
		WorkspaceFromContext: middleware.WorkspaceFromContext,
	}
}

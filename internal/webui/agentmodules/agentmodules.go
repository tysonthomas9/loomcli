package agentmodules

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/app/webhookingestion"
	"github.com/tysonthomas9/loomcli/internal/app/workflowbinding"
	"github.com/tysonthomas9/loomcli/internal/app/workfloweventing"
	"github.com/tysonthomas9/loomcli/internal/connector"
	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	workflowcataloghttp "github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog/httpapi"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/agentmodules/automationroutes"
	"github.com/tysonthomas9/loomcli/internal/webui/agentmodules/supportroutes"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/agents"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/approvals"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/connectors"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/driverapi"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/roles"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/taskrunapi"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/workflows"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

type Deps struct {
	Store              store.Store
	AgentSvc           service.AgentService
	IssueSvc           service.IssueService
	Hub                *realtime.Hub
	FleetBaseURL       string
	DriverAPIBaseURL   string
	DriverAPIToken     string
	DriverRunTokenKey  []byte
	LocalSettingsDir   string
	Dispatcher         *connector.Dispatcher
	AutomationBindings automation.BindingOperations
	WorkflowBinding    *workflowbinding.Workflow
	AutomationAudit    automation.AuditQueries
	AutomationWebhook  *webhookingestion.Workflow
	AutomationEventing *workfloweventing.Workflow
	AutomationOperator workflowcataloghttp.OperatorAuthorityResolver
}

func New(deps Deps) []interface{ Register(*http.ServeMux) } {
	var awaitStore store.AwaitStore
	var driverRuns store.DriverRunStore
	var triggerBindings store.TriggerBindingStore
	var connectorGrants store.ConnectorGrantStore
	if deps.Store != nil {
		awaitStore = deps.Store.Awaits()
		driverRuns = deps.Store.DriverRuns()
		triggerBindings = deps.Store.TriggerBindings()
		connectorGrants = deps.Store.ConnectorGrants()
	}
	automationModules := automationroutes.New(automationroutes.Deps{
		AutomationBindings: deps.AutomationBindings, WorkflowBinding: deps.WorkflowBinding,
		AutomationAudit: deps.AutomationAudit, AutomationWebhook: deps.AutomationWebhook,
		AutomationOperator: deps.AutomationOperator,
		Awaits:             awaitStore, DriverRuns: driverRuns,
		TriggerBindings: triggerBindings, ConnectorGrants: connectorGrants,
	})
	onboardingModule := supportroutes.New(deps.IssueSvc, deps.AgentSvc)

	// Keep registration order stable. The child constructors retain shared
	// connector-compatibility and await-matcher instances across their users.
	return []interface{ Register(*http.ServeMux) }{
		agents.New(agents.Config{
			AgentService: deps.AgentSvc, Store: deps.Store, Hub: deps.Hub,
			Bindings: deps.AutomationBindings, OperatorAuthority: deps.AutomationOperator,
			WorkspaceFromContext: automationModules.WorkspaceFromContext,
			BindingGrants:        automationModules.BindingGrants,
		}),
		onboardingModule,
		workflows.NewModule(deps.Store),
		automationModules.Webhooks,
		roles.NewModule(deps.Store),
		automationModules.TriggerBindings,
		connectors.NewModule(deps.Store, deps.LocalSettingsDir),
		approvals.NewModule(deps.Store),
		taskrunapi.NewModule(taskrunapi.Config{
			Store: deps.Store, FleetBaseURL: deps.FleetBaseURL, LocalSettingsDir: deps.LocalSettingsDir,
		}),
		driverapi.NewModule(driverapi.Config{
			Store: deps.Store, FleetBaseURL: deps.FleetBaseURL, APIBaseURL: deps.DriverAPIBaseURL,
			APIToken: deps.DriverAPIToken, RunTokenKey: deps.DriverRunTokenKey,
			LocalSettingsDir: deps.LocalSettingsDir, Dispatcher: deps.Dispatcher,
			WorkflowEventing: deps.AutomationEventing, EventAwaits: automationModules.EventAwaits,
		}),
	}
}

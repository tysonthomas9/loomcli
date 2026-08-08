package agentmodules

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/driver"
)

func resolveAutomationDeps(deps Deps) automationRouteDeps {
	result := automationRouteDeps{
		Capabilities: automationRouteCapabilities{
			AutomationBindings: deps.AutomationBindings,
			WorkflowBinding:    deps.WorkflowBinding,
			AutomationAudit:    deps.AutomationAudit,
			AutomationWebhook:  deps.AutomationWebhook,
			AutomationOperator: deps.AutomationOperator,
		},
	}
	if deps.Store != nil {
		result.Awaits = deps.Store.Awaits()
		result.DriverRuns = deps.Store.DriverRuns()
		result.TriggerBindings = deps.Store.TriggerBindings()
		result.ConnectorGrants = deps.Store.ConnectorGrants()
		result.AgentServices = deps.Store.AgentServices()
	}
	if deps.ExecutionDriverRuns != nil && deps.ExecutionSystemAuthorities != nil {
		result.AwaitResolver = &driver.ExecutionAwaitResolver{
			API: deps.ExecutionDriverRuns, Authorities: deps.ExecutionSystemAuthorities,
			ComponentID: "serve-await-event-notifications",
		}
	}
	return result
}

func New(deps Deps) []interface{ Register(*http.ServeMux) } {
	automationModules := newAutomationRouteModules(resolveAutomationDeps(deps))
	return newWorkspaceModules(deps, automationModules)
}

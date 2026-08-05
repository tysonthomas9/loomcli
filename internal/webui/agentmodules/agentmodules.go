package agentmodules

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/webui/agentmodules/automationroutes"
	"github.com/tysonthomas9/loomcli/internal/webui/agentmodules/routecontracts"
	"github.com/tysonthomas9/loomcli/internal/webui/agentmodules/workspaceroutes"
)

type Deps = routecontracts.Deps

func resolveAutomationDeps(deps Deps) automationroutes.Deps {
	result := automationroutes.Deps{Capabilities: deps}
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
	automationModules := automationroutes.New(resolveAutomationDeps(deps))
	return workspaceroutes.New(deps, automationModules)
}

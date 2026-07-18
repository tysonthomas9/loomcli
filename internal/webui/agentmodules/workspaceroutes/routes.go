// Package workspaceroutes assembles the ordered workspace route modules from
// the public capability ports supplied by the web server composition root.
package workspaceroutes

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/agentmodules/automationroutes"
	"github.com/tysonthomas9/loomcli/internal/webui/agentmodules/routecontracts"
	"github.com/tysonthomas9/loomcli/internal/webui/agentmodules/supportroutes"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/agents"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/approvals"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/connectors"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/driverapi"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/executionmanagement"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/roles"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/taskrunapi"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/workflows"
)

// New preserves the established route registration order while grouping the
// concrete HTTP adapters behind one workspace composition seam.
func New(deps routecontracts.Deps, automationModules automationroutes.Modules) []interface{ Register(*http.ServeMux) } {
	onboardingModule := supportroutes.New(deps.IssueSvc, deps.AgentSvc)

	return []interface{ Register(*http.ServeMux) }{
		agents.New(agents.Config{
			AgentService: deps.AgentSvc, Store: deps.Store, Hub: deps.Hub,
			Bindings: deps.AutomationBindings, OperatorAuthority: deps.AutomationOperator,
			WorkspaceFromContext: automationModules.WorkspaceFromContext,
			BindingGrants:        automationModules.BindingGrants,
		}),
		onboardingModule,
		workflows.NewModule(workflows.Config{
			Store: deps.Store, Catalog: deps.WorkflowCatalog,
			Execution: deps.ExecutionDriverRuns, OperatorAuthority: deps.ExecutionOperator,
		}),
		executionmanagement.New(executionmanagement.Config{
			WorkerProfiles: deps.ExecutionWorkerProfiles, Authority: deps.ExecutionOperator,
		}),
		automationModules.Webhooks,
		roles.NewModule(deps.Store),
		automationModules.TriggerBindings,
		connectors.NewModule(deps.Store, deps.LocalSettingsDir),
		approvals.New(approvals.Config{Store: deps.Store, Awaits: automationModules.EventAwaits}),
		taskrunapi.NewModule(taskrunapi.Config{
			Store: deps.Store, FleetBaseURL: deps.FleetBaseURL, LocalSettingsDir: deps.LocalSettingsDir,
			Execution: deps.ExecutionTaskRuns, Authorities: deps.ExecutionTaskRunAuthorities, Artifacts: deps.Artifacts,
		}),
		driverapi.NewModule(driverapi.Config{
			Store: deps.Store, FleetBaseURL: deps.FleetBaseURL, APIBaseURL: deps.DriverAPIBaseURL,
			APIToken: deps.DriverAPIToken, RunTokenKey: deps.DriverRunTokenKey,
			LocalSettingsDir: deps.LocalSettingsDir, Dispatcher: deps.Dispatcher,
			WorkflowEventing: deps.AutomationEventing, EventAwaits: automationModules.EventAwaits,
			Execution: deps.ExecutionDriverRuns, ExecutionAuthorities: deps.ExecutionDriverRunAuthorities,
			TaskRunRequests: deps.ExecutionTaskRunRequests, TaskRunRecovery: deps.ExecutionTaskRunRecovery,
			TaskRuns: deps.ExecutionTaskRuns, TaskRunAuthorities: deps.ExecutionTaskRunAuthorities,
			WorkflowCatalog: deps.WorkflowCatalog, Artifacts: deps.Artifacts,
		}),
	}
}

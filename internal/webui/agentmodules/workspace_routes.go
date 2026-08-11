package agentmodules

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/handlers/agents"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/agentsmanagement"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/approvals"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/connectors"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/driverapi"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/executionmanagement"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/interactionmanagement"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/onboarding"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/roles"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/taskrunapi"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/workflows"
	"github.com/tysonthomas9/loomcli/internal/webui/readprojection"
	"github.com/tysonthomas9/loomcli/internal/webui/storeadapter"
)

// newWorkspaceModules preserves the established route registration order while
// grouping the concrete HTTP adapters behind one workspace composition seam.
//
//nolint:funlen // This ordered composition table keeps route precedence and one-pass dependency wiring auditable together.
func newWorkspaceModules(deps Deps, automationModules automationRouteModules) []interface{ Register(*http.ServeMux) } {
	onboardingModule := onboarding.NewModule(deps.WorkItems, deps.Agents, deps.AgentsOperator)
	taskWorkflowRuns := newTaskWorkflowRunReader(deps)
	var pendingAwaits approvals.PendingAwaitQueries
	if deps.Store != nil {
		pendingAwaits = deps.Store.Awaits()
	}

	return []interface{ Register(*http.ServeMux) }{
		agents.New(agents.Config{
			SessionTranscripts: deps.AgentSessionTranscripts,
			InteractiveRuntime: deps.InteractiveAgentRuntime,
			AgentRecords:       deps.Agents, AgentRecordAuthority: deps.AgentsOperator,
			Store: deps.Store, Hub: deps.Hub,
			Bindings: deps.AutomationBindings, OperatorAuthority: deps.AutomationOperator,
			BindingRuns:  automationModules.BindingRuns,
			Provisioning: deps.AgentProvisioning, ProvisioningAuthority: deps.AgentProvisioningOperator,
			PrepareWorkflowTarget: deps.WorkflowTargetPreparation,
			WorkspaceFromContext:  automationModules.WorkspaceFromContext,
			BindingGrants:         automationModules.BindingGrants,
		}),
		agentsmanagement.New(agentsmanagement.Config{
			Agents: deps.Agents, Authority: deps.AgentsOperator,
		}),
		interactionmanagement.New(interactionmanagement.Config{
			Interaction: deps.Interaction, Authority: deps.InteractionOperator,
			SessionAuthorities: deps.InteractionSessionAuthorities,
		}),
		onboardingModule,
		workflows.NewModule(workflows.Config{
			Store: deps.Store, Catalog: deps.WorkflowCatalog, Authoring: deps.WorkflowCatalogAuthoring,
			CatalogOperatorAuthority: deps.WorkflowCatalogOperator,
			PrepareWorkflowTarget:    deps.WorkflowTargetPreparation,
			Execution:                deps.ExecutionDriverRuns, OperatorAuthority: deps.ExecutionOperator,
			TaskWorkflowRuns: taskWorkflowRuns, BackendHealth: deps.WorkflowBackendHealth,
		}),
		executionmanagement.New(executionmanagement.Config{
			WorkerProfiles: deps.ExecutionWorkerProfiles, Authority: deps.ExecutionOperator,
		}),
		automationModules.Webhooks,
		roles.NewModule(roles.Config{
			Store: deps.Store, Roles: deps.Agents, Authority: deps.AgentsOperator,
		}),
		automationModules.TriggerBindings,
		connectors.NewModule(deps.Store, deps.LocalSettingsDir, deps.AutomationBindings, deps.AutomationOperator),
		approvals.New(approvals.Config{
			PendingAwaits: pendingAwaits, Awaits: automationModules.EventAwaits,
			Journal: deps.AutomationApprovalJournal, Authority: deps.AutomationApprovalAuthority,
		}),
		taskrunapi.NewModule(taskrunapi.Config{
			Store: deps.Store, FleetBaseURL: deps.FleetBaseURL,
			IssueBackends: deps.ExecutionIssueBackends,
			Execution:     deps.ExecutionTaskRuns, Authorities: deps.ExecutionTaskRunAuthorities, Artifacts: deps.Artifacts,
			DaytonaProvider: deps.DaytonaProvider,
		}),
		driverapi.NewModule(driverapi.Config{
			Store: deps.Store, APIBaseURL: deps.DriverAPIBaseURL,
			IssueBackends:    deps.ExecutionIssueBackends,
			RolePrompts:      roles.ReadPromptBody,
			RunTokenKey:      deps.DriverRunTokenKey,
			LocalSettingsDir: deps.LocalSettingsDir, LocalRepoPath: storeadapter.ResolveRepoPath,
			SourceControl:    deps.SourceControl,
			StackBindings:    deps.TaskStackBindings,
			TaskOutcomes:     deps.TaskOutcomes,
			Dispatcher:       deps.Dispatcher,
			WorkflowEventing: deps.AutomationEventing, EventAwaits: automationModules.EventAwaits,
			Execution: deps.ExecutionDriverRuns, ExecutionAuthorities: deps.ExecutionDriverRunAuthorities,
			AgentIdentities: deps.Agents,
			TaskRunRequests: deps.ExecutionTaskRunRequests, TaskRunRecovery: deps.ExecutionTaskRunRecovery,
			TaskRuns: deps.ExecutionTaskRuns, TaskRunAuthorities: deps.ExecutionTaskRunAuthorities,
			WorkflowCatalog: deps.WorkflowCatalog, Artifacts: deps.Artifacts,
			InteractionChat: deps.InteractionChat,
		}),
	}
}

type workflowBackendHealthAdapter struct {
	check BackendHealthCheck
}

// BackendHealthCheck is the composition input used to translate infrastructure
// health into the Workflows delivery adapter's consumer-owned model.
type BackendHealthCheck func(name string) (available, installed, apiKeySet bool, message string, ok bool)

// NewWorkflowBackendHealthQuery closes the composition seam without exposing
// the operations health model to Workflows.
func NewWorkflowBackendHealthQuery(check BackendHealthCheck) workflows.BackendHealthQuery {
	if check == nil {
		return nil
	}
	return workflowBackendHealthAdapter{check: check}
}

func (adapter workflowBackendHealthAdapter) BackendHealth(name string) (workflows.BackendHealth, bool) {
	available, installed, apiKeySet, message, ok := adapter.check(name)
	if !ok {
		return workflows.BackendHealth{}, false
	}
	return workflows.BackendHealth{
		Available: available, Installed: installed,
		APIKeySet: apiKeySet, Message: message,
	}, true
}

func newTaskWorkflowRunReader(deps Deps) readprojection.TaskWorkflowRunReader {
	if deps.Store == nil {
		return nil
	}
	return readprojection.NewTaskWorkflowRunReader(
		deps.Store.TaskRuns(),
		deps.Store.TriggerEvents(),
		deps.Store.TriggerDeliveries(),
		deps.Store.DriverRuns(),
	)
}

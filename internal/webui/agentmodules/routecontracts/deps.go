// Package routecontracts defines the dependency contract shared by the
// workspace route composition packages. It keeps capability ports explicit
// without making each route assembler import every capability implementation.
package routecontracts

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/app/agentprovisioning"
	"github.com/tysonthomas9/loomcli/internal/app/webhookingestion"
	"github.com/tysonthomas9/loomcli/internal/app/workflowbinding"
	"github.com/tysonthomas9/loomcli/internal/app/workfloweventing"
	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/modules/artifacts"
	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	connectorsmodule "github.com/tysonthomas9/loomcli/internal/modules/connectors"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
	workflowcataloghttp "github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog/httpapi"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// Deps contains the capability ports and remaining compatibility dependencies
// needed to assemble workspace-scoped agent and execution routes.
type Deps struct {
	Store                         store.Store
	InteractiveAgentRuntime       service.InteractiveAgentRuntime
	AgentSessionTranscripts       service.AgentSessionTranscriptService
	IssueSvc                      service.IssueService
	Hub                           *realtime.Hub
	FleetBaseURL                  string
	ExecutionIssueBackends        func(workspace, actor string) (backend.IssueBackend, error)
	DriverAPIBaseURL              string
	DriverAPIToken                string
	DriverRunTokenKey             []byte
	LocalSettingsDir              string
	SourceControl                 sourcecontrol.Materializer
	Dispatcher                    connectorsmodule.Dispatcher
	AutomationBindings            automation.BindingOperations
	WorkflowBinding               *workflowbinding.Workflow
	AutomationAudit               automation.AuditQueries
	AutomationWebhook             *webhookingestion.Workflow
	AutomationEventing            *workfloweventing.Workflow
	AutomationOperator            workflowcataloghttp.OperatorAuthorityResolver
	Agents                        agents.API
	AgentsOperator                workflowcataloghttp.OperatorAuthorityResolver
	AgentProvisioning             agentprovisioning.Commands
	AgentProvisioningOperator     workflowcataloghttp.OperatorAuthorityResolver
	Interaction                   interaction.API
	InteractionOperator           workflowcataloghttp.OperatorAuthorityResolver
	InteractionSessionAuthorities interaction.SessionAuthorityResolver
	InteractionChat               interaction.ChatMessenger
	WorkflowCatalog               workflowcatalog.API
	WorkflowCatalogAuthoring      workflowcatalog.VersionAuthoringAPI
	WorkflowCatalogOperator       workflowcataloghttp.OperatorAuthorityResolver
	WorkflowTargetPreparation     func(context.Context, string, string) (*workflowcatalog.Driver, error)
	Artifacts                     artifacts.API
	ExecutionTaskRuns             execution.TaskRunAPI
	DaytonaProvider               execution.DaytonaProviderBroker
	ExecutionTaskRunRequests      execution.TaskRunRequestAPI
	ExecutionTaskRunRecovery      execution.TaskRunRecoveryAPI
	ExecutionTaskRunAuthorities   execution.TaskRunAuthorityResolver
	ExecutionWorkerProfiles       execution.WorkerProfileAPI
	ExecutionDriverRuns           execution.DriverRunAPI
	ExecutionDriverRunAuthorities execution.DriverRunAuthorityResolver
	ExecutionSystemAuthorities    execution.SystemAuthorityResolver
	ExecutionOperator             workflowcataloghttp.OperatorAuthorityResolver
}

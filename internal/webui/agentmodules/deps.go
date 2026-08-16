package agentmodules

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/app/agentprovisioning"
	"github.com/tysonthomas9/loomcli/internal/app/query/sessionarchive"
	"github.com/tysonthomas9/loomcli/internal/app/webhookingestion"
	"github.com/tysonthomas9/loomcli/internal/app/workflowbinding"
	"github.com/tysonthomas9/loomcli/internal/app/workfloweventing"
	agentsowner "github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/modules/artifacts"
	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	connectorsmodule "github.com/tysonthomas9/loomcli/internal/modules/connectors"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
	workflowcataloghttp "github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog/httpapi"
	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
	"github.com/tysonthomas9/loomcli/internal/modules/workspace"
	"github.com/tysonthomas9/loomcli/internal/webui/agentcoord"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/workflows"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	"github.com/tysonthomas9/loomcli/internal/webui/storeadapter"
)

type connectorRecordSource interface {
	Connectors() connectorsmodule.ManagementStore
}

type sourceControlMaterializer interface {
	PrepareTaskCheckout(context.Context, sourcecontrol.TaskCheckoutCommand) (*sourcecontrol.TaskCheckout, error)
	PreparePullRequestCheckout(context.Context, sourcecontrol.PullRequestCheckoutCommand) (*sourcecontrol.PullRequestCheckout, error)
}

type sourceControlStackBindings interface {
	ResolveTaskStackBinding(context.Context, string, string, string) (sourcecontrol.TaskStackBinding, bool, error)
}

type sourceControlTaskOutcomes interface {
	RecordTaskOutcome(context.Context, sourcecontrol.TaskOutcomeCommand) (bool, error)
}

// Deps contains the capability ports used by the workspace route composition.
type Deps struct {
	WorkspaceTopology              storeadapter.WorkspaceTopologyReader
	ConnectorRecords               connectorRecordSource
	OrchestrationSessions          interaction.OrchestrationSessionStore
	AwaitRecords                   execution.AwaitStore
	DriverRunRecords               execution.DriverRunStore
	TaskRunRecords                 execution.TaskRunStore
	TriggerEventRecords            automation.TriggerEventStore
	InteractiveAgentRuntime        agentcoord.InteractiveAgentRuntime
	AgentSessionTranscripts        sessionarchive.AgentSessionTranscriptService
	WorkItems                      workitems.API
	Workspace                      workspace.API
	Hub                            *realtime.Hub
	FleetBaseURL                   string
	DriverAPIBaseURL               string
	DriverRunTokenKey              []byte
	LocalSettingsDir               string
	SourceControl                  sourceControlMaterializer
	TaskStackBindings              sourceControlStackBindings
	TaskOutcomes                   sourceControlTaskOutcomes
	Dispatcher                     connectorsmodule.Dispatcher
	ConnectorBindingGrantLifecycle connectorsmodule.BindingGrantLifecycle
	AutomationBindings             automation.BindingOperations
	WorkflowBinding                *workflowbinding.Workflow
	AutomationAudit                automation.AuditQueries
	AutomationWebhook              *webhookingestion.Workflow
	AutomationEventing             *workfloweventing.Workflow
	AutomationApprovalJournal      automation.ApprovalJournal
	AutomationApprovalAuthority    automation.ApprovalAuthorityProvider
	AutomationOperator             workflowcataloghttp.OperatorAuthorityResolver
	Agents                         agentsowner.API
	AgentsOperator                 workflowcataloghttp.OperatorAuthorityResolver
	AgentProvisioning              agentprovisioning.Commands
	AgentProvisioningOperator      workflowcataloghttp.OperatorAuthorityResolver
	Interaction                    interaction.API
	InteractionOperator            workflowcataloghttp.OperatorAuthorityResolver
	InteractionSessionAuthorities  interaction.SessionAuthorityResolver
	InteractionChat                interaction.ChatMessenger
	WorkflowCatalog                workflowcatalog.API
	WorkflowCatalogAuthoring       workflowcatalog.VersionAuthoringAPI
	WorkflowCatalogOperator        workflowcataloghttp.OperatorAuthorityResolver
	WorkflowTargetPreparation      func(context.Context, string, string) (*workflowcatalog.Driver, error)
	WorkflowBackendHealth          workflows.BackendHealthQuery
	Artifacts                      artifacts.API
	ExecutionTaskRuns              execution.TaskRunAPI
	ExecutionTaskRunQueries        execution.TaskRunQueries
	DaytonaProvider                execution.DaytonaProviderBroker
	ExecutionTaskRunRequests       execution.TaskRunRequestAPI
	ExecutionTaskRunRecovery       execution.TaskRunRecoveryAPI
	ExecutionTaskRunAuthorities    execution.TaskRunAuthorityResolver
	ExecutionWorkerProfiles        execution.WorkerProfileAPI
	ExecutionDriverRuns            execution.DriverRunAPI
	ExecutionDriverRunAuthorities  execution.DriverRunAuthorityResolver
	ExecutionSystemAuthorities     execution.SystemAuthorityResolver
	ExecutionOperator              workflowcataloghttp.OperatorAuthorityResolver
}

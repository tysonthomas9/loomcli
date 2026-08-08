package agentmodules

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
	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/agentcoord"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	"github.com/tysonthomas9/loomcli/internal/webui/sessioncoord"
)

// ProjectionStore contains the read models needed to assemble
// workspace-scoped agent and execution routes.
type ProjectionStore interface {
	store.OrchestrationSessionStore
	Workspaces() store.WorkspaceStore
	Repos() store.RepoStore
	Roles() store.RoleStore
	AgentServices() store.AgentServiceStore
	Awaits() store.AwaitStore
	DriverRuns() store.DriverRunStore
	DriverSteps() store.DriverStepStore
	Drivers() store.DriverStore
	DriverVersions() store.DriverVersionStore
	Nodes() store.NodeStore
	WorkerProfiles() store.WorkerProfileStore
	TaskRuns() store.TaskRunStore
	TaskRunEvents() store.TaskRunEventStore
	Artifacts() store.ArtifactStore
	TriggerBindings() store.TriggerBindingStore
	TriggerEvents() store.TriggerEventStore
	TriggerDeliveries() store.TriggerDeliveryStore
	Connectors() store.ConnectorStore
	ConnectorGrants() store.ConnectorGrantStore
	ConnectorCalls() store.ConnectorAuditStore
}

// Deps contains the capability ports used by the workspace route composition.
type Deps struct {
	Store                         ProjectionStore
	InteractiveAgentRuntime       agentcoord.InteractiveAgentRuntime
	AgentSessionTranscripts       sessioncoord.AgentSessionTranscriptService
	WorkItems                     workitems.API
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
	AutomationApprovalJournal     automation.ApprovalJournal
	AutomationApprovalAuthority   automation.ApprovalAuthorityProvider
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

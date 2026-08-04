// Package servercapabilities defines the request-facing capability contracts
// accepted by the Web UI server composition root.
//
// These contracts intentionally expose module APIs and exact-purpose authority
// resolvers only. Persistence transports, issuers, and process-wide mutation
// stores remain owned by the corresponding application composition roots.
package servercapabilities

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/app/agentprovisioning"
	"github.com/tysonthomas9/loomcli/internal/app/prreviewer"
	"github.com/tysonthomas9/loomcli/internal/app/webhookingestion"
	"github.com/tysonthomas9/loomcli/internal/app/workflowbinding"
	"github.com/tysonthomas9/loomcli/internal/app/workfloweventing"
	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/modules/artifacts"
	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
	workflowcataloghttp "github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog/httpapi"
	"github.com/tysonthomas9/loomcli/internal/modules/workspace"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

// Automation is the narrow composition handle consumed by the web
// application and the serve lifecycle root.
type Automation interface {
	BindingOperations() automation.BindingOperations
	AuditQueries() automation.AuditQueries
	WebhookWorkflow() *webhookingestion.Workflow
	WorkflowBinding() *workflowbinding.Workflow
	WorkflowEventing() *workfloweventing.Workflow
	OperatorAuthorityResolver() workflowcataloghttp.OperatorAuthorityResolver
}

// Agents is the narrow identity handle published by serve composition.
type Agents interface {
	AgentsAPI() agents.API
	OperatorAuthorityResolver() workflowcataloghttp.OperatorAuthorityResolver
	PRReviewerProvisioning() prreviewer.Commands
}

// AgentProvisioning is the narrow request-facing process-manager handle.
type AgentProvisioning interface {
	AgentProvisioningCommands() agentprovisioning.Commands
	OperatorAuthorityResolver() workflowcataloghttp.OperatorAuthorityResolver
}

// InteractionSessionAuthorityResolver derives one action-scoped authority
// after durable validation of the supplied child lease proof.
type InteractionSessionAuthorityResolver interface {
	ResolveSessionAuthority(
		context.Context,
		authority.Action,
		interaction.SessionAuthorityProof,
	) (authority.SessionAuthority, error)
}

// Interaction is the session, terminal, inbox, and combined activity handle.
type Interaction interface {
	InteractionAPI() interaction.API
	ChatAPI() interaction.ChatAPI
	ChatMessenger() interaction.ChatMessenger
	OperatorAuthorityResolver() workflowcataloghttp.OperatorAuthorityResolver
	SessionAuthorityResolver() InteractionSessionAuthorityResolver
	InboxEnqueuer() interaction.InboxEnqueuer
	ForceInterrupter() interaction.ForceInterrupter
}

// Execution is the active execution composition handle exposed to web
// adapters. It intentionally exposes no shared issuer or persistence adapter.
type Execution interface {
	TaskRunAPI() execution.TaskRunAPI
	TaskRunRequestAPI() execution.TaskRunRequestAPI
	TaskRunWorkerAPI() execution.TaskRunWorkerAPI
	TaskRunSchedulingAPI() execution.TaskRunSchedulingAPI
	WorkerProfileAPI() execution.WorkerProfileAPI
	TaskRunConvergenceAPI() execution.TaskRunConvergenceAPI
	TaskRunConvergenceSource() execution.TaskRunConvergenceSource
	TaskRunConvergenceCheckpoints() execution.TaskRunConvergenceCheckpointPort
	TaskRunRecoveryAPI() execution.TaskRunRecoveryAPI
	TaskRunRecoveryScopes() execution.TaskRunRecoveryScopePort
	TaskRunAuthorityResolver() execution.TaskRunAuthorityResolver
	DriverRunAPI() execution.DriverRunAPI
	AwaitEventNotificationAPI() execution.AwaitEventNotificationAPI
	DriverRunOutcomeAPI() execution.DriverRunOutcomeAPI
	TerminalDriverRunWorkRecoveryQueueAPI() execution.TerminalDriverRunWorkRecoveryQueueAPI
	OutboxDeliveryAPI() execution.OutboxDeliveryAPI
	DriverRunAuthorityResolver() execution.DriverRunAuthorityResolver
	SystemAuthorityResolver() execution.SystemAuthorityResolver
	OperatorAuthorityResolver() workflowcataloghttp.OperatorAuthorityResolver
}

// Artifacts is the owner-fenced artifact lifecycle handle.
type Artifacts interface {
	ArtifactsAPI() artifacts.API
}

// Exact module aliases keep ServerConfig's public field types source
// compatible without making the root webui package own these dependencies.
type (
	IssueBackend                             = backend.IssueBackend
	DaytonaProviderBroker                    = execution.DaytonaProviderBroker
	SourceControlMaterializer                = sourcecontrol.Materializer
	RepositoryAdmissionMaterializer          = sourcecontrol.RepositoryAdmissionMaterializer
	WorkflowCatalogAPI                       = workflowcatalog.API
	WorkflowCatalogVersionAuthoringAPI       = workflowcatalog.VersionAuthoringAPI
	WorkflowCatalogDriver                    = workflowcatalog.Driver
	WorkflowCatalogOperatorAuthorityResolver = workflowcataloghttp.OperatorAuthorityResolver
	WorkspaceAPI                             = workspace.API
)

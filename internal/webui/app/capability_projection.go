package app

import (
	"context"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	connectorsmodule "github.com/tysonthomas9/loomcli/internal/modules/connectors"
	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	workflowcataloghttp "github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog/httpapi"
	"github.com/tysonthomas9/loomcli/internal/modules/workspace"
	"github.com/tysonthomas9/loomcli/internal/webui"
	"github.com/tysonthomas9/loomcli/internal/webui/agentcoord"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/prreview"
)

type reviewerRuntimeProjection struct {
	runtime agentcoord.InteractiveAgentRuntime
}

func (projection reviewerRuntimeProjection) StopReviewerSession(
	ctx context.Context,
	workspace,
	agentID string,
) error {
	return projection.runtime.StopAgent(ctx, workspace, agentID)
}

// NewTerminalModules adds Interaction's request-bound authority resolver to an
// otherwise transport-neutral terminal delivery dependency set.
func NewTerminalModules(
	interactionCapability webui.InteractionCapability,
	deps TerminalModuleDeps,
) []interface{ Register(*http.ServeMux) } {
	if interactionCapability != nil {
		deps.Operator = interactionCapability.OperatorAuthorityResolver()
	}
	return newTerminalRouteModules(deps)
}

// PopulateUnifiedAgentCapabilityDeps projects every configured capability into
// the unified agent route module without exposing issuers or persistence
// transports to the app lifecycle root.
func PopulateUnifiedAgentCapabilityDeps(
	config webui.ServerConfig,
	deps *UnifiedAgentModuleDeps,
) {
	if capability := config.AutomationCapability; capability != nil {
		deps.AutomationBindings = capability.BindingOperations()
		deps.WorkflowBinding = capability.WorkflowBinding()
		deps.AutomationAudit = capability.AuditQueries()
		deps.AutomationWebhook = capability.WebhookWorkflow()
		deps.AutomationEventing = capability.WorkflowEventing()
		deps.AutomationApprovalJournal = capability.ApprovalJournal()
		deps.AutomationApprovalAuthority = capability.ApprovalAuthorityProvider()
		deps.AutomationOperator = capability.OperatorAuthorityResolver()
	}
	if capability := config.AgentsCapability; capability != nil {
		deps.Agents = capability.AgentsAPI()
		deps.AgentsOperator = capability.OperatorAuthorityResolver()
	}
	if capability := config.AgentProvisioning; capability != nil {
		deps.AgentProvisioning = capability.AgentProvisioningCommands()
		deps.AgentProvisioningOperator = capability.OperatorAuthorityResolver()
	}
	if capability := config.InteractionCapability; capability != nil {
		deps.Interaction = capability.InteractionAPI()
		deps.InteractionOperator = capability.OperatorAuthorityResolver()
		deps.InteractionSessionAuthorities = capability.SessionAuthorityResolver()
		deps.InteractionChat = capability.ChatMessenger()
	}
	if capability := config.ExecutionCapability; capability != nil {
		deps.ExecutionTaskRuns = capability.TaskRunAPI()
		deps.ExecutionTaskRunQueries = capability.TaskRunQueries()
		deps.ExecutionTaskRunRequests = capability.TaskRunRequestAPI()
		deps.ExecutionTaskRunRecovery = capability.TaskRunRecoveryAPI()
		deps.ExecutionTaskRunAuthorities = capability.TaskRunAuthorityResolver()
		deps.ExecutionWorkerProfiles = capability.WorkerProfileAPI()
		deps.ExecutionDriverRuns = capability.DriverRunAPI()
		deps.ExecutionDriverRunAuthorities = capability.DriverRunAuthorityResolver()
		deps.ExecutionSystemAuthorities = capability.SystemAuthorityResolver()
		deps.ExecutionOperator = capability.OperatorAuthorityResolver()
	}
}

// NewPRReviewModule projects Agents and Interaction capabilities into the
// connector-backed pull-request review module. Missing capabilities remain nil
// so the module retains its existing fail-closed behavior.
func NewPRReviewModule(
	config webui.ServerConfig,
	workspaceQueries workspace.API,
	connectorManagement connectorsmodule.Management,
	connectorSealer connectorsmodule.CredentialSealer,
	dispatcher connectorsmodule.Dispatcher,
	reviewerRuntime agentcoord.InteractiveAgentRuntime,
) PRReviewModule {
	var reviewerIdentities prreview.ReviewerIdentityCommands
	var reviewerAgents agents.IdentityQueries
	var reviewerChat interaction.ChatAPI
	var reviewerMessenger interaction.ChatMessenger
	var reviewerInteractionAuthority workflowcataloghttp.OperatorAuthorityResolver
	var reviewerRuntimeCommands prreview.ReviewerRuntimeCommands
	if capability := config.AgentsCapability; capability != nil {
		reviewerIdentities = capability
		reviewerAgents = capability.AgentsAPI()
	}
	if capability := config.InteractionCapability; capability != nil {
		reviewerChat = capability.ChatAPI()
		reviewerMessenger = capability.ChatMessenger()
		reviewerInteractionAuthority = capability.OperatorAuthorityResolver()
	}
	if reviewerRuntime != nil {
		reviewerRuntimeCommands = reviewerRuntimeProjection{runtime: reviewerRuntime}
	}
	return prreview.NewModule(prreview.Config{
		Workspace: workspaceQueries, ConnectorManagement: connectorManagement, ConnectorSealer: connectorSealer,
		Dispatcher: dispatcher, PullRequests: config.SourceControlCheckout,
		LocalSettingsDir:   config.LocalSettingsDir,
		ReviewerIdentities: reviewerIdentities, ReviewerAgents: reviewerAgents,
		ReviewerRuntime: reviewerRuntimeCommands,
		SourceControl:   config.SourceControl,
		InteractionChat: reviewerChat, InteractionMessenger: reviewerMessenger,
		InteractionAuthority: reviewerInteractionAuthority,
	})
}

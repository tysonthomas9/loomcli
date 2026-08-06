// Package capabilitycomposition projects process-owned capabilities into the
// narrow dependency sets consumed by Web UI services and route modules.
package capabilitycomposition

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/app/prreviewer"
	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	connectorsmodule "github.com/tysonthomas9/loomcli/internal/modules/connectors"
	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	workflowcataloghttp "github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog/httpapi"
	"github.com/tysonthomas9/loomcli/internal/webui"
	"github.com/tysonthomas9/loomcli/internal/webui/agentcoord"
	"github.com/tysonthomas9/loomcli/internal/webui/modbuilder"
	"github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

// NewTerminalModules adds the identity and Interaction capability projections
// to an otherwise transport-neutral terminal module dependency set.
func NewTerminalModules(
	agentsCapability webui.AgentsCapability,
	interactionCapability webui.InteractionCapability,
	deps modbuilder.TerminalModuleDeps,
) []interface{ Register(*http.ServeMux) } {
	if agentsCapability != nil {
		deps.Agents = agentsCapability.AgentsAPI()
	}
	if interactionCapability != nil {
		deps.Interaction = interactionCapability.InteractionAPI()
		deps.Operator = interactionCapability.OperatorAuthorityResolver()
		deps.SessionAuthorities = interactionCapability.SessionAuthorityResolver()
	}
	return modbuilder.NewTerminalModules(deps)
}

// PopulateUnifiedAgentCapabilityDeps projects every configured capability into
// the unified agent route module without exposing issuers or persistence
// transports to the app lifecycle root.
func PopulateUnifiedAgentCapabilityDeps(
	config webui.ServerConfig,
	deps *modbuilder.UnifiedAgentModuleDeps,
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
	dispatcher connectorsmodule.Dispatcher,
	agentService agentcoord.AgentService,
	terminalService terminal.TerminalService,
) modbuilder.PRReviewModule {
	var reviewerProvisioning prreviewer.Commands
	var reviewerAgents agents.IdentityQueries
	var reviewerChat interaction.ChatAPI
	var reviewerMessenger interaction.ChatMessenger
	var reviewerInteractionAuthority workflowcataloghttp.OperatorAuthorityResolver
	if capability := config.AgentsCapability; capability != nil {
		reviewerProvisioning = capability.PRReviewerProvisioning()
		reviewerAgents = capability.AgentsAPI()
	}
	if capability := config.InteractionCapability; capability != nil {
		reviewerChat = capability.ChatAPI()
		reviewerMessenger = capability.ChatMessenger()
		reviewerInteractionAuthority = capability.OperatorAuthorityResolver()
	}
	return modbuilder.NewPRReviewModule(
		config.Store,
		dispatcher,
		agentService,
		terminalService,
		config.LocalSettingsDir,
		reviewerProvisioning,
		reviewerAgents,
		config.SourceControl,
		reviewerChat,
		reviewerMessenger,
		reviewerInteractionAuthority,
	)
}

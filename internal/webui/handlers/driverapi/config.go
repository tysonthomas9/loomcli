package driverapi

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/app/workfloweventing"
	trigger "github.com/tysonthomas9/loomcli/internal/infra/automationruntime"
	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	connectorsmodule "github.com/tysonthomas9/loomcli/internal/modules/connectors"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
	"github.com/tysonthomas9/loomcli/internal/modules/workspace"
)

type RepositoryQueries interface {
	GetRepository(context.Context, workspace.GetRepositoryQuery) (*workspace.Repository, error)
	ListRepositories(context.Context, workspace.ListRepositoriesQuery) ([]workspace.Repository, error)
}

// OrchestrationSessionQueries is the exact Interaction read projection needed
// by a running driver to address an interactive agent session. The HTTP
// adapter receives only the resolved identity, never a session repository.
type OrchestrationSessionQueries interface {
	FindActiveOrchestrationSession(context.Context, string, string) (string, error)
}

// WorkItemOperations is the exact Work Items surface consumed by the Driver
// transport. It deliberately excludes aggregate creation, deletion, claim,
// and dependency mutation.
type WorkItemOperations interface {
	Get(context.Context, workitems.GetQuery) (*workitems.IssueDetail, error)
	List(context.Context, workitems.ListQuery) (*workitems.ListResult, error)
	Ready(context.Context, workitems.AvailabilityQuery) ([]workitems.IssueSummary, error)
	Blocked(context.Context, workitems.AvailabilityQuery) ([]workitems.IssueSummary, error)
	Patch(context.Context, workitems.PatchCommand) (*workitems.IssueDetail, error)
	AddComment(context.Context, workitems.AddCommentCommand) (*workitems.Comment, error)
	ListComments(context.Context, workitems.ListCommentsQuery) ([]*workitems.Comment, error)
	BlockRepositoryRequired(context.Context, workitems.BlockRepositoryRequiredCommand) (*workitems.RepositoryAdmissionResult, error)
}

// RolePromptReader materializes the prompt body for a persisted role. The
// composition root supplies the machine-local prompt-file adapter so this HTTP
// adapter does not depend on a sibling HTTP handler.
type RolePromptReader func(*agents.Role) string

// WorkflowEventAwaitDispatcher is the narrow post-admission AW7 seam.
type WorkflowEventAwaitDispatcher interface {
	Dispatch(context.Context, string, trigger.AwaitDispatchEvent) (*trigger.AwaitDispatchResult, error)
}

// Config wires the driver HTTP adapter through its narrow capability and
// read-projection contracts.
type Config struct {
	APIBaseURL            string
	RunTokenKey           []byte
	LocalRepoPath         func(workspaceKey, repoName string) string
	WorkItems             WorkItemOperations
	Repositories          RepositoryQueries
	OrchestrationSessions OrchestrationSessionQueries
	AutomationBindings    automation.BindingQueries
	AutomationEvents      automation.EventQueries
	AutomationDeliveries  automation.DeliveryQueries
	RolePrompts           RolePromptReader
	Dispatcher            connectorsmodule.Dispatcher
	WorkflowEventing      *workfloweventing.Workflow
	EventAwaits           WorkflowEventAwaitDispatcher

	Execution            execution.DriverRunAPI
	ExecutionAuthorities execution.DriverRunAuthorityResolver
	AgentIdentities      agents.IdentityQueries
	AgentRoles           agents.RoleQueries
	TaskRunRequests      execution.TaskRunRequestAPI
	TaskRunRecovery      execution.TaskRunRecoveryAPI
	TaskRuns             execution.TaskRunAPI
	TaskRunQueries       execution.TaskRunQueries
	TaskRunAuthorities   execution.TaskRunAuthorityResolver
	WorkflowCatalog      workflowcatalog.API
	InteractionChat      interaction.ChatMessenger
}

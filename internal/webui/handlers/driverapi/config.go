package driverapi

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/app/workfloweventing"
	"github.com/tysonthomas9/loomcli/internal/domain"
	trigger "github.com/tysonthomas9/loomcli/internal/infra/automationruntime"
	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	artifactsmodule "github.com/tysonthomas9/loomcli/internal/modules/artifacts"
	connectorsmodule "github.com/tysonthomas9/loomcli/internal/modules/connectors"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
)

type SourceControl = sourcecontrol.Materializer
type Artifacts = artifactsmodule.API

// RolePromptReader materializes the prompt body for a persisted role. The
// composition root supplies the machine-local prompt-file adapter so this HTTP
// adapter does not depend on a sibling HTTP handler.
type RolePromptReader func(*domain.Role) string

// WorkflowEventAwaitDispatcher is the narrow post-admission AW7 seam.
type WorkflowEventAwaitDispatcher interface {
	Dispatch(context.Context, string, trigger.AwaitDispatchEvent) (*trigger.AwaitDispatchResult, error)
}

// Config wires the driver HTTP adapter through its narrow capability and
// read-projection contracts.
type Config struct {
	Store            Store
	APIBaseURL       string
	APIToken         string //nolint:gosec // G117: driver API bearer token intentionally carried by handler config.
	RunTokenKey      []byte
	WorktreePath     string
	LocalSettingsDir string
	SourceControl    SourceControl
	LocalRepoPath    func(workspaceKey, repoName string) string
	IssueBackends    IssueBackendFactory
	RolePrompts      RolePromptReader
	Dispatcher       connectorsmodule.Dispatcher
	WorkflowEventing *workfloweventing.Workflow
	EventAwaits      WorkflowEventAwaitDispatcher

	Execution            execution.DriverRunAPI
	ExecutionAuthorities execution.DriverRunAuthorityResolver
	AgentIdentities      agents.IdentityQueries
	TaskRunRequests      execution.TaskRunRequestAPI
	TaskRunRecovery      execution.TaskRunRecoveryAPI
	TaskRuns             execution.TaskRunAPI
	TaskRunAuthorities   execution.TaskRunAuthorityResolver
	WorkflowCatalog      workflowcatalog.API
	Artifacts            Artifacts
	InteractionChat      interaction.ChatMessenger
}

package driverapi

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/app/workfloweventing"
	"github.com/tysonthomas9/loomcli/internal/backend"
	trigger "github.com/tysonthomas9/loomcli/internal/infra/automationruntime"
	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	artifactsmodule "github.com/tysonthomas9/loomcli/internal/modules/artifacts"
	connectorsmodule "github.com/tysonthomas9/loomcli/internal/modules/connectors"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type IssueBackendFactory func(workspace, actor string) (backend.IssueBackend, error)

type SourceControl = sourcecontrol.Materializer
type Artifacts = artifactsmodule.API

// Store is the legacy read-only projection boundary still needed by the
// driver HTTP adapter while mutations enter through capability APIs.
type Store interface {
	store.OrchestrationSessionStore
	Awaits() store.AwaitStore
	TriggerEvents() store.TriggerEventStore
	TriggerBindings() store.TriggerBindingStore
	TriggerDeliveries() store.TriggerDeliveryStore
	Roles() store.RoleStore
	Repos() store.RepoStore
	AgentServices() store.AgentServiceStore
	TaskRuns() store.TaskRunStore
	TaskRunEvents() store.TaskRunEventStore
	DriverRuns() store.DriverRunStore
	Drivers() store.DriverStore
	DriverVersions() store.DriverVersionStore
	Nodes() store.NodeStore
	WorkerProfiles() store.WorkerProfileStore
}

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

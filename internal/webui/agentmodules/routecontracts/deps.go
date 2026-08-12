// Package routecontracts defines the dependency contract shared by the
// workspace route composition packages. It keeps capability ports explicit
// without making each route assembler import every capability implementation.
package routecontracts

import (
	"github.com/tysonthomas9/loomcli/internal/app/webhookingestion"
	"github.com/tysonthomas9/loomcli/internal/app/workflowbinding"
	"github.com/tysonthomas9/loomcli/internal/app/workfloweventing"
	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/connector"
	"github.com/tysonthomas9/loomcli/internal/modules/artifacts"
	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
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
	AgentSvc                      service.AgentService
	AgentSessionTranscripts       service.AgentSessionTranscriptService
	IssueSvc                      service.IssueService
	Hub                           *realtime.Hub
	FleetBaseURL                  string
	ExecutionIssueBackends        func(workspace, actor string) (backend.IssueBackend, error)
	DriverAPIBaseURL              string
	DriverAPIToken                string
	DriverRunTokenKey             []byte
	LocalSettingsDir              string
	Dispatcher                    *connector.Dispatcher
	AutomationBindings            automation.BindingOperations
	WorkflowBinding               *workflowbinding.Workflow
	AutomationAudit               automation.AuditQueries
	AutomationWebhook             *webhookingestion.Workflow
	AutomationEventing            *workfloweventing.Workflow
	AutomationOperator            workflowcataloghttp.OperatorAuthorityResolver
	WorkflowCatalog               workflowcatalog.API
	Artifacts                     artifacts.API
	ExecutionTaskRuns             execution.TaskRunAPI
	ExecutionTaskRunRequests      execution.TaskRunRequestAPI
	ExecutionTaskRunRecovery      execution.TaskRunRecoveryAPI
	ExecutionTaskRunAuthorities   execution.TaskRunAuthorityResolver
	ExecutionWorkerProfiles       execution.WorkerProfileAPI
	ExecutionDriverRuns           execution.DriverRunAPI
	ExecutionDriverRunAuthorities execution.DriverRunAuthorityResolver
	ExecutionSystemAuthorities    execution.SystemAuthorityResolver
	ExecutionOperator             workflowcataloghttp.OperatorAuthorityResolver
}

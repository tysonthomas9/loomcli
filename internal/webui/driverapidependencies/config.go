// Package driverapidependencies owns the infrastructure/configuration boundary
// for the driver HTTP adapter.
package driverapidependencies

import (
	"context"
	"fmt"
	"os"

	"github.com/tysonthomas9/loomcli/internal/app/workfloweventing"
	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/backend/fleet"
	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	artifactsmodule "github.com/tysonthomas9/loomcli/internal/modules/artifacts"
	connectorsmodule "github.com/tysonthomas9/loomcli/internal/modules/connectors"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/trigger"
)

type IssueBackendFactory func(workspace, actor string) (backend.IssueBackend, error)

type SourceControl = sourcecontrol.Materializer
type Artifacts = artifactsmodule.API

// WorkflowEventAwaitDispatcher is the narrow post-admission AW7 seam.
type WorkflowEventAwaitDispatcher interface {
	Dispatch(context.Context, string, trigger.AwaitDispatchEvent) (*trigger.AwaitDispatchResult, error)
}

// Config wires the driver HTTP adapter without exposing its implementation
// dependencies to the handler package.
type Config struct {
	Store            store.Store
	FleetBaseURL     string
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

// DefaultIssueBackends builds the production issue-backend factory: a
// FleetDB client per workspace and actor against the configured base URL.
func DefaultIssueBackends(baseURL string) IssueBackendFactory {
	return func(workspace, actor string) (backend.IssueBackend, error) {
		issueBackend, err := fleet.New(fleet.Config{
			BaseURL:     baseURL,
			WorkspaceID: workspace,
			APIKey:      os.Getenv(bootstrap.EnvFleetDBAPIKey),
			Actor:       actor,
		})
		if err != nil {
			return nil, fmt.Errorf("create fleet-db issue backend: %w", err)
		}
		return issueBackend, nil
	}
}

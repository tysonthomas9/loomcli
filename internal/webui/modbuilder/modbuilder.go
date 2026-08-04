// Package modbuilder provides compatibility facades for workspace-scoped
// handler module composition.
package modbuilder

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/app/prreviewer"
	"github.com/tysonthomas9/loomcli/internal/app/workitemmove"
	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	connectorsmodule "github.com/tysonthomas9/loomcli/internal/modules/connectors"
	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
	workflowcataloghttp "github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog/httpapi"
	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/modbuilder/agentcomposition"
	"github.com/tysonthomas9/loomcli/internal/webui/modbuilder/reviewcomposition"
	"github.com/tysonthomas9/loomcli/internal/webui/modbuilder/sessioncomposition"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// PRReviewModule is the route module plus its credential-cache invalidation
// surface used by local settings wiring.
type PRReviewModule = reviewcomposition.PRReviewModule

// CredentialSeedInvalidator is the cross-module notification surface needed
// when a persisted GitHub runtime credential changes.
type CredentialSeedInvalidator = reviewcomposition.CredentialSeedInvalidator

// LocalSettingsHandlers contains the non-workspace local settings routes.
type LocalSettingsHandlers = reviewcomposition.LocalSettingsHandlers

// NewIssueModules creates the issue and session modules.
func NewIssueModules(workItems workitems.API, mover workitemmove.Commands, sessSvc service.SessionService) []interface{ Register(*http.ServeMux) } {
	return sessioncomposition.NewIssueModules(workItems, mover, sessSvc)
}

// TerminalModuleDeps holds dependencies for the (now tmux-free) terminal
// modules. PTYMgr drives the main terminal WS; AgentTmuxMgr is kept only for
// the live agent-view WS, which still reads auto-mode tmux sessions.
type TerminalModuleDeps = sessioncomposition.TerminalModuleDeps

// NewTerminalModules creates the terminal tab and main terminal modules.
func NewTerminalModules(deps TerminalModuleDeps) []interface{ Register(*http.ServeMux) } {
	return sessioncomposition.NewTerminalModules(deps)
}

// NewIssueTabModule creates the issue tab module.
func NewIssueTabModule(issueTabs interaction.IssueTabStateAPI, hub *realtime.Hub) interface{ Register(*http.ServeMux) } {
	return sessioncomposition.NewIssueTabModule(issueTabs, hub)
}

// NewDiffModule creates the git diff module.
func NewDiffModule(agentSvc service.AgentService, diffSvc service.DiffService) interface{ Register(*http.ServeMux) } {
	return reviewcomposition.NewDiffModule(agentSvc, diffSvc)
}

// NewFileModule creates the file operations module.
func NewFileModule(fileSvc service.FileService, accessCfg ...middleware.FileAccessConfig) interface{ Register(*http.ServeMux) } {
	return reviewcomposition.NewFileModule(fileSvc, accessCfg...)
}

// NewPRReviewModule creates the connector-backed pull request review module.
// terminalSvc may be nil (no PTY manager); reviewer backend migration then
// skips killing live reviewer terminals. localSettingsDir supplies the shared
// GitHub credential and connector vault key location. Interaction owns all
// reviewer conversation reads and message delivery.
func NewPRReviewModule(
	st store.Store,
	dispatcher connectorsmodule.Dispatcher,
	agentSvc service.AgentService,
	terminalSvc service.TerminalService,
	localSettingsDir string,
	reviewerProvisioning prreviewer.Commands,
	reviewerAgents agents.IdentityQueries,
	sourceControl sourcecontrol.Materializer,
	interactionChat interaction.ChatAPI,
	interactionMessenger interaction.ChatMessenger,
	interactionAuthority workflowcataloghttp.OperatorAuthorityResolver,
) PRReviewModule {
	return reviewcomposition.NewPRReviewModule(
		st,
		dispatcher,
		agentSvc,
		terminalSvc,
		localSettingsDir,
		reviewerProvisioning,
		reviewerAgents,
		sourceControl,
		interactionChat,
		interactionMessenger,
		interactionAuthority,
	)
}

// NewLocalSettingsHandlers wires GitHub credential changes to the PR-review
// seed cache without coupling either handler package to the other.
func NewLocalSettingsHandlers(dataDir string, invalidator CredentialSeedInvalidator) LocalSettingsHandlers {
	return reviewcomposition.NewLocalSettingsHandlers(dataDir, invalidator)
}

// NewTaskRunAPIModule creates the task-runner HTTP API module
// (POST /api/workspaces/{ws}/task-run/{op}, lease-token auth) so task runner
// processes talk to serve instead of holding fleet-db credentials.
func NewTaskRunAPIModule(st store.Store, fleetBaseURL string, localSettingsDir string) interface{ Register(*http.ServeMux) } {
	return agentcomposition.NewTaskRunAPIModule(st, fleetBaseURL, localSettingsDir)
}

type UnifiedAgentModuleDeps = agentcomposition.UnifiedAgentModuleDeps

func NewUnifiedAgentModules(deps UnifiedAgentModuleDeps) []interface{ Register(*http.ServeMux) } {
	return agentcomposition.NewUnifiedAgentModules(deps)
}

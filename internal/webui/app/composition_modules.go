package app

import (
	"context"
	"net/http"
	"time"

	"github.com/tysonthomas9/loomcli/internal/app/prreviewer"
	"github.com/tysonthomas9/loomcli/internal/app/workitemmove"
	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	connectorsmodule "github.com/tysonthomas9/loomcli/internal/modules/connectors"
	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
	workflowcataloghttp "github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog/httpapi"
	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/agentcoord"
	"github.com/tysonthomas9/loomcli/internal/webui/agentmodules"
	"github.com/tysonthomas9/loomcli/internal/webui/filecoord"
	githandlers "github.com/tysonthomas9/loomcli/internal/webui/handlers/git"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/issues"
	locsettings "github.com/tysonthomas9/loomcli/internal/webui/handlers/localsettings"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/misc"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/prreview"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/taskrunapi"
	hterminal "github.com/tysonthomas9/loomcli/internal/webui/handlers/terminal"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	"github.com/tysonthomas9/loomcli/internal/webui/sessioncoord"
	"github.com/tysonthomas9/loomcli/internal/webui/sourcecontrolcoord"
	"github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

// PRReviewModule is the route module plus its credential-cache invalidation
// surface used by local settings wiring.
type PRReviewModule interface {
	Register(*http.ServeMux)
	InvalidateCredentialSeeds()
}

// LocalSettingsHandlers contains the non-workspace local settings routes.
type LocalSettingsHandlers struct {
	Get                        http.HandlerFunc
	Patch                      http.HandlerFunc
	RuntimeCredentialPreflight http.HandlerFunc
}

// NewIssueModules creates the issue and session modules.
func NewIssueModules(workItems workitems.API, mover workitemmove.Commands, sessSvc sessioncoord.SessionService) []interface{ Register(*http.ServeMux) } {
	return []interface{ Register(*http.ServeMux) }{
		issues.NewIssueModule(workItems, mover),
		issues.NewSessionModule(sessSvc, issues.SessionModuleOpts{
			ListTaskSessions:     misc.HandleListTaskSessions(sessSvc),
			GetSession:           misc.HandleGetSession(sessSvc),
			GetSessionTranscript: misc.HandleGetSessionTranscript(sessSvc),
			GetSessionDiff:       misc.HandleGetSessionDiff(sessSvc),
		}),
	}
}

// TerminalModuleDeps holds dependencies for the (now tmux-free) terminal
// modules. PTYMgr drives the main terminal WS; AgentTmuxMgr is kept only for
// the live agent-view WS, which still reads auto-mode tmux sessions.
type TerminalModuleDeps struct {
	TermSvc            terminal.TerminalService
	AgentSvc           agentcoord.AgentService
	PTYMgr             terminal.PTYSource
	AgentTmuxMgr       *terminal.AgentTmuxManager // may be nil when tmux is missing
	TermAuth           *realtime.TerminalAuth
	CORSOrigins        []string
	SelfURL            string
	Store              store.Store
	TabMetaStore       terminal.TabMetadataStore
	Hub                *realtime.Hub
	ServerStartedAt    time.Time
	Agents             agents.IdentityQueries
	Interaction        interaction.API
	Operator           workflowcataloghttp.OperatorAuthorityResolver
	SessionAuthorities interface {
		ResolveSessionAuthority(
			context.Context,
			authority.Action,
			interaction.SessionAuthorityProof,
		) (authority.SessionAuthority, error)
	}
}

// NewTerminalModules creates the terminal tab and main terminal modules.
func newTerminalRouteModules(deps TerminalModuleDeps) []interface{ Register(*http.ServeMux) } {
	return []interface{ Register(*http.ServeMux) }{
		hterminal.NewTabModule(deps.TermSvc),
		hterminal.NewModule(
			deps.TermSvc, deps.AgentSvc, deps.PTYMgr, deps.AgentTmuxMgr,
			deps.TermAuth, deps.CORSOrigins,
			deps.SelfURL, deps.Store,
			deps.TabMetaStore, deps.Hub, deps.ServerStartedAt,
			hterminal.InteractionDependencies{
				API: deps.Interaction, Operator: deps.Operator,
				SessionAuthorities: deps.SessionAuthorities,
				TerminalIdentities: deps.TermSvc,
			},
			deps.Agents),
	}
}

// NewIssueTabModule creates the issue tab module.
func NewIssueTabModule(issueTabs interaction.IssueTabStateAPI, hub *realtime.Hub) interface{ Register(*http.ServeMux) } {
	return issues.NewIssueTabModule(issueTabs, hub)
}

// NewDiffModule creates the git diff module.
func NewDiffModule(agentSvc agentcoord.AgentService, diffSvc sourcecontrolcoord.DiffService) interface{ Register(*http.ServeMux) } {
	return githandlers.NewModule(agentSvc, diffSvc)
}

// NewFileModule creates the file operations module.
func NewFileModule(fileSvc filecoord.FileService, accessCfg ...middleware.FileAccessConfig) interface{ Register(*http.ServeMux) } {
	return misc.NewModule(fileSvc, accessCfg...)
}

// NewPRReviewModule creates the connector-backed pull request review module.
// terminalSvc may be nil (no PTY manager); reviewer backend migration then
// skips killing live reviewer terminals. localSettingsDir supplies the shared
// GitHub credential and connector vault key location. Interaction owns all
// reviewer conversation reads and message delivery.
func newPRReviewRouteModule(
	st store.Store,
	dispatcher connectorsmodule.Dispatcher,
	agentSvc agentcoord.AgentService,
	terminalSvc terminal.TerminalService,
	localSettingsDir string,
	reviewerProvisioning prreviewer.Commands,
	reviewerAgents agents.IdentityQueries,
	sourceControl sourcecontrol.Materializer,
	interactionChat interaction.ChatAPI,
	interactionMessenger interaction.ChatMessenger,
	interactionAuthority workflowcataloghttp.OperatorAuthorityResolver,
) PRReviewModule {
	return prreview.NewModule(
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
func NewLocalSettingsHandlers(dataDir string, invalidator credentialSeedInvalidator) LocalSettingsHandlers {
	options := locsettings.PatchOptions{}
	if invalidator != nil {
		options.OnGitHubRuntimeCredentialChanged = invalidator.InvalidateCredentialSeeds
	}
	return LocalSettingsHandlers{
		Get:                        locsettings.HandleGet(dataDir),
		Patch:                      locsettings.HandlePatch(dataDir, options),
		RuntimeCredentialPreflight: locsettings.HandleRuntimeCredentialPreflight(dataDir),
	}
}

// NewTaskRunAPIModule creates the task-runner HTTP API module
// (POST /api/workspaces/{ws}/task-run/{op}, lease-token auth) so task runner
// processes talk to serve instead of holding fleet-db credentials.
func NewTaskRunAPIModule(st store.Store, fleetBaseURL string, localSettingsDir string) interface{ Register(*http.ServeMux) } {
	_ = localSettingsDir // compatibility input only; task runners never receive Local Settings.
	return taskrunapi.NewModule(taskrunapi.Config{Store: st, FleetBaseURL: fleetBaseURL})
}

// UnifiedAgentModuleDeps contains the dependencies for unified agent modules.
type UnifiedAgentModuleDeps = agentmodules.Deps

// NewUnifiedAgentModules creates the unified agent route modules.
func NewUnifiedAgentModules(deps UnifiedAgentModuleDeps) []interface{ Register(*http.ServeMux) } {
	return agentmodules.New(deps)
}

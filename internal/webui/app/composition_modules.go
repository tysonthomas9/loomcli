package app

import (
	"context"
	"net/http"
	"time"

	"github.com/tysonthomas9/loomcli/internal/app/query/sessionarchive"
	"github.com/tysonthomas9/loomcli/internal/app/workitemmove"
	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
	workflowcataloghttp "github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog/httpapi"
	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
	"github.com/tysonthomas9/loomcli/internal/modules/workspace"
	"github.com/tysonthomas9/loomcli/internal/webui/agentcoord"
	"github.com/tysonthomas9/loomcli/internal/webui/agentmodules"
	githandlers "github.com/tysonthomas9/loomcli/internal/webui/handlers/git"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/issues"
	locsettings "github.com/tysonthomas9/loomcli/internal/webui/handlers/localsettings"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/misc"
	hterminal "github.com/tysonthomas9/loomcli/internal/webui/handlers/terminal"
	"github.com/tysonthomas9/loomcli/internal/webui/readprojection"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
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
func NewIssueModules(workItems workitems.API, mover workitemmove.Commands, sessSvc sessionarchive.SessionService) []interface{ Register(*http.ServeMux) } {
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

// TerminalModuleDeps holds the Interaction terminal owner plus delivery-only
// HTTP, WebSocket, presentation, and token dependencies.
type TerminalModuleDeps struct {
	TermSvc           interaction.TerminalTabs
	AgentSvc          agentcoord.AgentService
	TermAuth          *realtime.TerminalAuth
	CORSOrigins       []string
	SelfURL           string
	PresentationState hterminal.PresentationState
	Hub               *realtime.Hub
	ServerStartedAt   time.Time
	Workspace         workspace.API
	Operator          workflowcataloghttp.OperatorAuthorityResolver
}

// NewTerminalModules creates the terminal tab and main terminal modules.
func newTerminalRouteModules(deps TerminalModuleDeps) []interface{ Register(*http.ServeMux) } {
	state := terminalStateQueryAdapter{workspace: deps.Workspace}
	terminalTabs := hterminal.WithTerminalNotifications(deps.TermSvc, deps.Hub)
	return []interface{ Register(*http.ServeMux) }{
		hterminal.NewTabModule(terminalTabs, deps.PresentationState),
		hterminal.NewModule(
			terminalTabs, deps.AgentSvc,
			deps.TermAuth, deps.CORSOrigins,
			deps.SelfURL, state,
			deps.Hub, deps.ServerStartedAt,
			hterminal.InteractionDependencies{
				Operator: deps.Operator,
			}),
	}
}

type terminalStateQueryAdapter struct {
	workspace workspace.API
}

func (adapter terminalStateQueryAdapter) ResolveWorkspaceName(ctx context.Context, reference string) (string, error) {
	if adapter.workspace == nil {
		return "", nil
	}
	resolved, err := adapter.workspace.Resolve(ctx, workspace.ResolveQuery{Reference: reference})
	if err != nil || resolved == nil {
		return "", err
	}
	return resolved.Name, nil
}

// NewIssueTabModule creates the issue tab module.
func NewIssueTabModule(issueTabs interaction.IssueTabStateAPI, hub *realtime.Hub) interface{ Register(*http.ServeMux) } {
	return issues.NewIssueTabModule(issueTabs, hub)
}

// NewDiffModule creates the git diff module.
func NewDiffModule(
	checkout sourcecontrol.Checkout,
	browse sourcecontrol.Browse,
	issueDiff readprojection.IssueDiffProjection,
) interface{ Register(*http.ServeMux) } {
	return githandlers.NewModule(checkout, browse, issueDiff)
}

// NewFileModule creates the file operations module.
func NewFileModule(browse sourcecontrol.Browse, mutate sourcecontrol.Mutate, checkout sourcecontrol.Checkout, accessCfg ...middleware.FileAccessConfig) interface{ Register(*http.ServeMux) } {
	return misc.NewModule(browse, mutate, checkout, accessCfg...)
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

// UnifiedAgentModuleDeps contains the dependencies for unified agent modules.
type UnifiedAgentModuleDeps = agentmodules.Deps

// NewUnifiedAgentModules creates the unified agent route modules.
func NewUnifiedAgentModules(deps UnifiedAgentModuleDeps) []interface{ Register(*http.ServeMux) } {
	return agentmodules.New(deps)
}

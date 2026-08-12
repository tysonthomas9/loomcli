package app

import (
	"context"
	"net/http"
	"time"

	"github.com/tysonthomas9/loomcli/internal/app/workitemmove"
	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	workflowcataloghttp "github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog/httpapi"
	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
	"github.com/tysonthomas9/loomcli/internal/modules/workspace"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/agentcoord"
	"github.com/tysonthomas9/loomcli/internal/webui/agentmodules"
	"github.com/tysonthomas9/loomcli/internal/webui/filecoord"
	githandlers "github.com/tysonthomas9/loomcli/internal/webui/handlers/git"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/issues"
	locsettings "github.com/tysonthomas9/loomcli/internal/webui/handlers/localsettings"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/misc"
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
	TabMetaStore       terminal.TabMetadataStore
	Hub                *realtime.Hub
	ServerStartedAt    time.Time
	Agents             agents.IdentityQueries
	Roles              agents.RoleQueries
	Workspace          workspace.API
	Orchestration      store.OrchestrationSessionStore
	WorkspacePath      func(context.Context, string) string
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
	state := terminalStateQueryAdapter{
		roles: deps.Roles, workspace: deps.Workspace,
		orchestration: deps.Orchestration, workspacePath: deps.WorkspacePath,
	}
	return []interface{ Register(*http.ServeMux) }{
		hterminal.NewTabModule(deps.TermSvc),
		hterminal.NewModule(
			deps.TermSvc, deps.AgentSvc, deps.PTYMgr, deps.AgentTmuxMgr,
			deps.TermAuth, deps.CORSOrigins,
			deps.SelfURL, state,
			deps.TabMetaStore, deps.Hub, deps.ServerStartedAt,
			hterminal.InteractionDependencies{
				API: deps.Interaction, Operator: deps.Operator,
				SessionAuthorities: deps.SessionAuthorities,
				TerminalIdentities: deps.TermSvc,
			},
			deps.Agents),
	}
}

type terminalStateQueryAdapter struct {
	roles         agents.RoleQueries
	workspace     workspace.API
	orchestration store.OrchestrationSessionStore
	workspacePath func(context.Context, string) string
}

func (adapter terminalStateQueryAdapter) GetRole(ctx context.Context, workspaceKey, roleName string) (*agents.Role, error) {
	if adapter.roles == nil {
		return nil, agents.ErrNotFound
	}
	return adapter.roles.GetRole(ctx, workspaceKey, roleName)
}

func (adapter terminalStateQueryAdapter) FindActiveOrchestrationSession(ctx context.Context, workspaceKey, agentID string) (string, error) {
	if adapter.orchestration == nil {
		return "", nil
	}
	return store.OrchestrationSessionIDFor(ctx, adapter.orchestration, workspaceKey, agentID)
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

func (adapter terminalStateQueryAdapter) ResolveWorkspacePath(ctx context.Context, workspaceKey string) string {
	if adapter.workspacePath == nil {
		return ""
	}
	return adapter.workspacePath(ctx, workspaceKey)
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

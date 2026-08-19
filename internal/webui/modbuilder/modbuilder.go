// Package modbuilder constructs workspace-scoped handler modules for the
// webui/app composition root. It imports all handler sub-packages so that
// the app package does not need to import them directly.
package modbuilder

import (
	"net/http"
	"time"

	"github.com/tysonthomas9/loomcli/internal/connector"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/approvals"
	githandlers "github.com/tysonthomas9/loomcli/internal/webui/handlers/git"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/issues"
	locsettings "github.com/tysonthomas9/loomcli/internal/webui/handlers/localsettings"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/misc"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/prreview"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/stacks"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/taskrunapi"
	hterminal "github.com/tysonthomas9/loomcli/internal/webui/handlers/terminal"
	"github.com/tysonthomas9/loomcli/internal/webui/issuetabs"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
	"github.com/tysonthomas9/loomcli/internal/webui/svcimpl/stacksvc"
	"github.com/tysonthomas9/loomcli/internal/webui/tabmeta"
	"github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

// PRReviewModule is the route module plus its credential-cache invalidation
// surface used by local settings wiring.
type PRReviewModule interface {
	Register(*http.ServeMux)
	InvalidateCredentialSeeds()
}

// CredentialSeedInvalidator is the cross-module notification surface needed
// when a persisted GitHub runtime credential changes.
type CredentialSeedInvalidator interface {
	InvalidateCredentialSeeds()
}

// LocalSettingsHandlers contains the non-workspace local settings routes.
type LocalSettingsHandlers struct {
	Get   http.HandlerFunc
	Patch http.HandlerFunc
}

// NewIssueModules creates the issue and session modules.
func NewIssueModules(issueSvc service.IssueService, sessSvc service.SessionService, st store.Store) []interface{ Register(*http.ServeMux) } {
	return []interface{ Register(*http.ServeMux) }{
		issues.NewIssueModule(issueSvc, st),
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
	TermSvc         service.TerminalService
	AgentSvc        service.AgentService
	PTYMgr          terminal.PTYSource
	AgentTmuxMgr    *terminal.AgentTmuxManager // may be nil when tmux is missing
	TermAuth        *realtime.TerminalAuth
	CORSOrigins     []string
	SelfURL         string
	Store           store.Store
	TabMetaStore    *tabmeta.Store
	Hub             *realtime.Hub
	ServerStartedAt time.Time
}

// NewTerminalModules creates the terminal tab and main terminal modules.
func NewTerminalModules(deps TerminalModuleDeps) []interface{ Register(*http.ServeMux) } {
	return []interface{ Register(*http.ServeMux) }{
		hterminal.NewTabModule(deps.TermSvc),
		hterminal.NewModule(
			deps.TermSvc, deps.AgentSvc, deps.PTYMgr, deps.AgentTmuxMgr,
			deps.TermAuth, deps.CORSOrigins,
			deps.SelfURL, deps.Store,
			deps.TabMetaStore, deps.Hub, deps.ServerStartedAt),
	}
}

// NewIssueTabModule creates the issue tab module.
func NewIssueTabModule(issueTabStore *issuetabs.Store, hub *realtime.Hub) interface{ Register(*http.ServeMux) } {
	return issues.NewIssueTabModule(issueTabStore, hub)
}

// NewDiffModule creates the git diff module.
func NewDiffModule(agentSvc service.AgentService, diffSvc service.DiffService) interface{ Register(*http.ServeMux) } {
	return githandlers.NewModule(agentSvc, diffSvc)
}

// NewFileModule creates the file operations module.
func NewFileModule(fileSvc service.FileService, accessCfg ...middleware.FileAccessConfig) interface{ Register(*http.ServeMux) } {
	return misc.NewModule(fileSvc, accessCfg...)
}

// NewStackModule creates the read-only stack lineage module.
func NewStackModule(stackSvc service.StackService) interface{ Register(*http.ServeMux) } {
	if stackSvc == nil {
		stackSvc = stacksvc.New()
	}
	return stacks.NewModule(stackSvc)
}

// NewApprovalsModule creates the await approval-resolution module
// (POST /api/workspaces/{ws}/approvals; the actor is always the verified
// session identity, never request data).
func NewApprovalsModule(st store.Store) interface{ Register(*http.ServeMux) } {
	return approvals.NewModule(st)
}

// NewPRReviewModule creates the connector-backed pull request review module.
// terminalSvc may be nil (no PTY manager); reviewer backend migration then
// skips killing live reviewer terminals. localSettingsDir supplies the shared
// GitHub credential and connector vault key location.
func NewPRReviewModule(st store.Store, dispatcher *connector.Dispatcher, agentSvc service.AgentService, terminalSvc service.TerminalService, localSettingsDir string) PRReviewModule {
	return prreview.NewModule(st, dispatcher, agentSvc, terminalSvc, localSettingsDir)
}

// NewLocalSettingsHandlers wires GitHub credential changes to the PR-review
// seed cache without coupling either handler package to the other.
func NewLocalSettingsHandlers(dataDir string, invalidator CredentialSeedInvalidator) LocalSettingsHandlers {
	options := locsettings.PatchOptions{}
	if invalidator != nil {
		options.OnGitHubRuntimeCredentialChanged = invalidator.InvalidateCredentialSeeds
	}
	return LocalSettingsHandlers{
		Get:   locsettings.HandleGet(dataDir),
		Patch: locsettings.HandlePatch(dataDir, options),
	}
}

// NewTaskRunAPIModule creates the task-runner HTTP API module
// (POST /api/workspaces/{ws}/task-run/{op}, lease-token auth) so task runner
// processes talk to serve instead of holding fleet-db credentials.
func NewTaskRunAPIModule(st store.Store, fleetBaseURL string, localSettingsDir string) interface{ Register(*http.ServeMux) } {
	return taskrunapi.NewModule(taskrunapi.Config{Store: st, FleetBaseURL: fleetBaseURL, LocalSettingsDir: localSettingsDir})
}

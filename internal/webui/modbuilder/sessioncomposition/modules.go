// Package sessioncomposition constructs the issue, session, and terminal
// modules used by the web UI composition root.
package sessioncomposition

import (
	"context"
	"net/http"
	"time"

	"github.com/tysonthomas9/loomcli/internal/app/workitemmove"
	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	workflowcataloghttp "github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog/httpapi"
	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/issues"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/misc"
	hterminal "github.com/tysonthomas9/loomcli/internal/webui/handlers/terminal"
	"github.com/tysonthomas9/loomcli/internal/webui/issuetabs"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
	"github.com/tysonthomas9/loomcli/internal/webui/tabmeta"
	"github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

// NewIssueModules creates the issue and session modules.
func NewIssueModules(workItems workitems.API, mover workitemmove.Commands, sessSvc service.SessionService) []interface{ Register(*http.ServeMux) } {
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
	TermSvc            service.TerminalService
	AgentSvc           service.AgentService
	PTYMgr             terminal.PTYSource
	AgentTmuxMgr       *terminal.AgentTmuxManager // may be nil when tmux is missing
	TermAuth           *realtime.TerminalAuth
	CORSOrigins        []string
	SelfURL            string
	Store              store.Store
	TabMetaStore       *tabmeta.Store
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
func NewTerminalModules(deps TerminalModuleDeps) []interface{ Register(*http.ServeMux) } {
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
			},
			deps.Agents),
	}
}

// NewIssueTabModule creates the issue tab module.
func NewIssueTabModule(issueTabStore *issuetabs.Store, hub *realtime.Hub) interface{ Register(*http.ServeMux) } {
	return issues.NewIssueTabModule(issueTabStore, hub)
}

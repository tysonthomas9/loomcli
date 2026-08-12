package terminal

import (
	"context"
	"net/http"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	workflowcataloghttp "github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog/httpapi"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/webui/agentcoord"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	webuterminal "github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

type InteractionSessionAuthorityResolver interface {
	ResolveSessionAuthority(
		context.Context,
		authority.Action,
		interaction.SessionAuthorityProof,
	) (authority.SessionAuthority, error)
}

type InteractionDependencies struct {
	API                interaction.API
	Operator           workflowcataloghttp.OperatorAuthorityResolver
	SessionAuthorities InteractionSessionAuthorityResolver
	TerminalIdentities webuterminal.InteractionTerminalIdentityWriter
}

// Module registers the surviving workspace-scoped terminal routes: the
// PTY-backed terminal WebSocket, a one-time auth token endpoint, and the
// tmux-backed agent terminal viewer (which still uses tmux because
// auto-mode CLI hosts agents there).
//
// All tmux-specific session lifecycle routes (spawn, kill, restart, seed,
// lead-session, export, scrollback, close-all, list-sessions,
// session-status) are gone — each WebSocket now owns a fresh PTY with
// wterm-style wire, so there are no persistent sessions to manage.
type Module struct {
	termSvc         webuterminal.TerminalService
	agentSvc        agentcoord.AgentService // may be nil — agent routes skipped
	ptyMgr          webuterminal.PTYSource
	agentTmuxMgr    *webuterminal.AgentTmuxManager // may be nil — tmux missing
	termAuth        *realtime.TerminalAuth         // may be nil — token routes skipped
	allowedOrigins  []string
	loomServerURL   string
	state           StateQueries
	tabMetaStore    webuterminal.TabMetadataReader
	hub             *realtime.Hub
	serverStartedAt time.Time
	agentIdentity   terminalAgentIdentity
	interaction     InteractionDependencies
}

// NewModule returns a Module. Any of agentSvc, agentTmuxMgr, and termAuth
// may be nil — routes that depend on them will simply not be registered.
// ptyMgr must be non-nil when terminal routes should be served; pass nil
// (the interface, not a typed-nil pointer) to skip registration.
func NewModule(
	termSvc webuterminal.TerminalService,
	agentSvc agentcoord.AgentService,
	ptyMgr webuterminal.PTYSource,
	agentTmuxMgr *webuterminal.AgentTmuxManager,
	termAuth *realtime.TerminalAuth,
	allowedOrigins []string,
	loomServerURL string,
	state StateQueries,
	tabMetaStore webuterminal.TabMetadataReader,
	hub *realtime.Hub,
	serverStartedAt time.Time,
	interactionDeps InteractionDependencies,
	identities ...terminalAgentIdentity,
) *Module {
	return &Module{
		termSvc:         termSvc,
		agentSvc:        agentSvc,
		ptyMgr:          ptyMgr,
		agentTmuxMgr:    agentTmuxMgr,
		termAuth:        termAuth,
		allowedOrigins:  allowedOrigins,
		loomServerURL:   loomServerURL,
		state:           state,
		tabMetaStore:    tabMetaStore,
		hub:             hub,
		serverStartedAt: serverStartedAt,
		agentIdentity:   firstTerminalAgentIdentity(identities),
		interaction:     interactionDeps,
	}
}

// Register registers the surviving terminal routes on mux.
func (m *Module) Register(mux *http.ServeMux) {
	// Agent terminal (tmux-backed, for live view of auto-mode agent sessions).
	if m.agentSvc != nil {
		mux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/terminal/info", HandleGetAgentTerminalInfo(m.agentSvc))
		if m.termAuth != nil {
			mux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/terminal/token", HandleGetAgentTerminalToken(m.agentSvc))
		}
	}
	if m.termSvc != nil && m.state != nil {
		mux.HandleFunc(
			"POST /api/workspaces/{ws}/agents/{name}/terminal/session",
			HandleEnsureAgentTerminalSession(m.termSvc, m.state, m.agentIdentity),
		)
	}
	if m.agentTmuxMgr != nil {
		mux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/terminal/ws", HandleAgentTerminalWS(m.agentTmuxMgr, m.termAuth, m.allowedOrigins))
	}

	// Main web terminal (PTY-backed, wterm wire format).
	if m.termAuth != nil {
		mux.HandleFunc("GET /api/workspaces/{ws}/terminal/token", HandleTerminalToken(m.termSvc))
	}
	if m.ptyMgr != nil {
		mux.HandleFunc(
			"GET /api/workspaces/{ws}/terminal/ws",
			HandleTerminalWSWithInteraction(
				m.ptyMgr,
				m.termAuth,
				m.allowedOrigins,
				m.loomServerURL,
				m.state,
				m.tabMetaStore,
				m.hub,
				m.serverStartedAt,
				m.interaction,
				m.agentIdentity,
			),
		)
	}
}

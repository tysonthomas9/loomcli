package terminal

import (
	"net/http"
	"time"

	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
	"github.com/tysonthomas9/loomcli/internal/webui/tabmeta"
	webuterminal "github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

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
	termSvc               service.TerminalService
	agentSvc              service.AgentService // may be nil — agent routes skipped
	ptyMgr                *webuterminal.PTYManager
	agentTmuxMgr          *webuterminal.AgentTmuxManager // may be nil — tmux missing
	termAuth              *realtime.TerminalAuth         // may be nil — token routes skipped
	allowedOrigins        []string
	loomServerURL         string
	workspaceConfigByIDFn func(string) (*ops.WorkspaceData, error)
	tabMetaStore          *tabmeta.Store
	hub                   *realtime.Hub
	serverStartedAt       time.Time
}

// NewModule returns a Module. Any of agentSvc, agentTmuxMgr, and termAuth
// may be nil — routes that depend on them will simply not be registered.
func NewModule(
	termSvc service.TerminalService,
	agentSvc service.AgentService,
	ptyMgr *webuterminal.PTYManager,
	agentTmuxMgr *webuterminal.AgentTmuxManager,
	termAuth *realtime.TerminalAuth,
	allowedOrigins []string,
	loomServerURL string,
	workspaceConfigByIDFn func(string) (*ops.WorkspaceData, error),
	tabMetaStore *tabmeta.Store,
	hub *realtime.Hub,
	serverStartedAt time.Time,
) *Module {
	return &Module{
		termSvc:               termSvc,
		agentSvc:              agentSvc,
		ptyMgr:                ptyMgr,
		agentTmuxMgr:          agentTmuxMgr,
		termAuth:              termAuth,
		allowedOrigins:        allowedOrigins,
		loomServerURL:         loomServerURL,
		workspaceConfigByIDFn: workspaceConfigByIDFn,
		tabMetaStore:          tabMetaStore,
		hub:                   hub,
		serverStartedAt:       serverStartedAt,
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
	if m.agentTmuxMgr != nil {
		mux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/terminal/ws", HandleAgentTerminalWS(m.agentTmuxMgr, m.termAuth, m.allowedOrigins))
	}

	// Main web terminal (PTY-backed, wterm wire format).
	if m.termAuth != nil {
		mux.HandleFunc("GET /api/workspaces/{ws}/terminal/token", HandleTerminalToken(m.termSvc))
	}
	if m.ptyMgr != nil {
		mux.HandleFunc("GET /api/workspaces/{ws}/terminal/ws", HandleTerminalWS(m.ptyMgr, m.termAuth, m.allowedOrigins, m.loomServerURL, m.workspaceConfigByIDFn, m.tabMetaStore, m.hub, m.serverStartedAt))
	}
}

package terminal

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
	"github.com/tysonthomas9/loomcli/internal/webui/tabmeta"
	webuterminal "github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

// Module registers the workspace-scoped terminal management routes
// on a [*http.ServeMux]. This covers agent terminal endpoints, core session
// management, and scrollback/export.
//
// The module is only constructed when termSvc is non-nil. Agent terminal
// routes are conditional on agentSvc; token routes are conditional on
// termAuth.
type Module struct {
	termSvc               service.TerminalService
	agentSvc              service.AgentService // may be nil — agent routes skipped
	termMgr               *webuterminal.TerminalManager
	termAuth              *realtime.TerminalAuth // may be nil — token routes skipped
	allowedOrigins        []string
	loomServerURL         string
	workspaceConfigByIDFn func(string) (*ops.WorkspaceData, error)
	tabMetaStore          *tabmeta.Store
	hub                   *realtime.Hub
}

// NewModule returns a Module. agentSvc and termAuth may be
// nil — their conditional routes will simply not be registered.
func NewModule(
	termSvc service.TerminalService,
	agentSvc service.AgentService,
	termMgr *webuterminal.TerminalManager,
	termAuth *realtime.TerminalAuth,
	allowedOrigins []string,
	loomServerURL string,
	workspaceConfigByIDFn func(string) (*ops.WorkspaceData, error),
	tabMetaStore *tabmeta.Store,
	hub *realtime.Hub,
) *Module {
	return &Module{
		termSvc:               termSvc,
		agentSvc:              agentSvc,
		termMgr:               termMgr,
		termAuth:              termAuth,
		allowedOrigins:        allowedOrigins,
		loomServerURL:         loomServerURL,
		workspaceConfigByIDFn: workspaceConfigByIDFn,
		tabMetaStore:          tabMetaStore,
		hub:                   hub,
	}
}

// Register implements [Module] by registering 12–16 terminal management routes.
// Agent info/token routes are conditional on agentSvc != nil. The general
// terminal token route is conditional on termAuth != nil.
func (m *Module) Register(mux *http.ServeMux) {
	// Agent terminal endpoints — conditional on agentSvc
	if m.agentSvc != nil {
		mux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/terminal/info", HandleGetAgentTerminalInfo(m.agentSvc))
		if m.termAuth != nil {
			mux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/terminal/token", HandleGetAgentTerminalToken(m.agentSvc))
		}
	}
	mux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/terminal/ws", HandleAgentTerminalWS(m.termMgr, m.termAuth, m.allowedOrigins))

	// Core terminal session management
	mux.HandleFunc("GET /api/workspaces/{ws}/terminal/sessions", HandleListTerminalSessions(m.termSvc))
	if m.termAuth != nil {
		mux.HandleFunc("GET /api/workspaces/{ws}/terminal/token", HandleTerminalToken(m.termSvc))
	}
	mux.HandleFunc("GET /api/workspaces/{ws}/terminal/ws", HandleTerminalWS(m.termMgr, m.termAuth, m.allowedOrigins, m.loomServerURL, m.workspaceConfigByIDFn, m.tabMetaStore, m.hub))
	mux.HandleFunc("POST /api/workspaces/{ws}/terminal/restart", HandleTerminalRestart(m.termSvc, m.termAuth))
	mux.HandleFunc("POST /api/workspaces/{ws}/terminal/kill", HandleTerminalKill(m.termSvc, m.termAuth))
	mux.HandleFunc("GET /api/workspaces/{ws}/terminal/session-status", HandleTerminalSessionStatus(m.termSvc, m.termAuth))
	mux.HandleFunc("POST /api/workspaces/{ws}/terminal/spawn", HandleTerminalSpawn(m.termSvc))
	mux.HandleFunc("POST /api/workspaces/{ws}/terminal/sessions/{name}/seed", HandleSeedTerminalSession(m.termSvc))
	mux.HandleFunc("POST /api/workspaces/{ws}/terminal/sessions/{session}/kill", HandleScheduleSessionKill(m.termSvc))
	mux.HandleFunc("POST /api/workspaces/{ws}/terminal/sessions/close-all", HandleCloseAllSessions(m.termSvc))

	// Scrollback, export, and scrollback-info
	mux.HandleFunc("GET /api/workspaces/{ws}/terminal/sessions/{session}/scrollback", HandleGetScrollback(m.termSvc))
	mux.HandleFunc("GET /api/workspaces/{ws}/terminal/sessions/{session}/export", HandleExportSession(m.termSvc))
	mux.HandleFunc("GET /api/workspaces/{ws}/terminal/sessions/{session}/scrollback-info", HandleScrollbackInfo(m.termSvc))
}

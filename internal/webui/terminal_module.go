package webui

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	"github.com/tysonthomas9/loomcli/internal/webui/tabmeta"
)

// TerminalModule registers the workspace-scoped terminal management routes
// on a [*http.ServeMux]. This covers agent terminal endpoints, core session
// management, and scrollback/export.
//
// The module is only constructed when termSvc is non-nil. Agent terminal
// routes are conditional on agentSvc; token routes are conditional on
// termAuth.
type TerminalModule struct {
	termSvc               TerminalService
	agentSvc              AgentService // may be nil — agent routes skipped
	termMgr               *TerminalManager
	termAuth              *realtime.TerminalAuth // may be nil — token routes skipped
	allowedOrigins        []string
	loomServerURL         string
	workspaceConfigByIDFn func(string) (*ops.WorkspaceData, error)
	tabMetaStore          *tabmeta.Store
	hub                   *realtime.Hub
}

// NewTerminalModule returns a TerminalModule. agentSvc and termAuth may be
// nil — their conditional routes will simply not be registered.
func NewTerminalModule(
	termSvc TerminalService,
	agentSvc AgentService,
	termMgr *TerminalManager,
	termAuth *realtime.TerminalAuth,
	allowedOrigins []string,
	loomServerURL string,
	workspaceConfigByIDFn func(string) (*ops.WorkspaceData, error),
	tabMetaStore *tabmeta.Store,
	hub *realtime.Hub,
) *TerminalModule {
	return &TerminalModule{
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
func (m *TerminalModule) Register(mux *http.ServeMux) {
	// Agent terminal endpoints — conditional on agentSvc
	if m.agentSvc != nil {
		mux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/terminal/info", handleGetAgentTerminalInfo(m.agentSvc))
		if m.termAuth != nil {
			mux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/terminal/token", handleGetAgentTerminalToken(m.agentSvc))
		}
	}
	mux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/terminal/ws", handleAgentTerminalWS(m.termMgr, m.termAuth, m.allowedOrigins))

	// Core terminal session management
	mux.HandleFunc("GET /api/workspaces/{ws}/terminal/sessions", handleListTerminalSessions(m.termSvc))
	if m.termAuth != nil {
		mux.HandleFunc("GET /api/workspaces/{ws}/terminal/token", handleTerminalToken(m.termSvc))
	}
	mux.HandleFunc("GET /api/workspaces/{ws}/terminal/ws", handleTerminalWS(m.termMgr, m.termAuth, m.allowedOrigins, m.loomServerURL, m.workspaceConfigByIDFn, m.tabMetaStore, m.hub))
	mux.HandleFunc("POST /api/workspaces/{ws}/terminal/restart", handleTerminalRestart(m.termSvc, m.termAuth))
	mux.HandleFunc("POST /api/workspaces/{ws}/terminal/kill", handleTerminalKill(m.termSvc, m.termAuth))
	mux.HandleFunc("GET /api/workspaces/{ws}/terminal/session-status", handleTerminalSessionStatus(m.termSvc, m.termAuth))
	mux.HandleFunc("POST /api/workspaces/{ws}/terminal/spawn", handleTerminalSpawn(m.termSvc))
	mux.HandleFunc("POST /api/workspaces/{ws}/terminal/sessions/{name}/seed", handleSeedTerminalSession(m.termSvc))
	mux.HandleFunc("POST /api/workspaces/{ws}/terminal/sessions/{session}/kill", handleScheduleSessionKill(m.termSvc))
	mux.HandleFunc("POST /api/workspaces/{ws}/terminal/sessions/close-all", handleCloseAllSessions(m.termSvc))

	// Scrollback, export, and scrollback-info
	mux.HandleFunc("GET /api/workspaces/{ws}/terminal/sessions/{session}/scrollback", handleGetScrollback(m.termSvc))
	mux.HandleFunc("GET /api/workspaces/{ws}/terminal/sessions/{session}/export", handleExportSession(m.termSvc))
	mux.HandleFunc("GET /api/workspaces/{ws}/terminal/sessions/{session}/scrollback-info", handleScrollbackInfo(m.termSvc))
}

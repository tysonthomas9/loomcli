package terminal

import (
	"net/http"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	workflowcataloghttp "github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog/httpapi"
	"github.com/tysonthomas9/loomcli/internal/webui/agentcoord"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

type InteractionDependencies struct {
	Operator workflowcataloghttp.OperatorAuthorityResolver
}

// Module registers delivery-only terminal routes. Interaction owns terminal
// placement, launch, reconnect, replay, PTY, and tmux policy; this module owns
// HTTP mapping, WebSocket upgrade, framing, and disconnect translation.
type Module struct {
	termSvc         interaction.TerminalTabs
	agentSvc        agentcoord.AgentService // may be nil — agent routes skipped
	termAuth        *realtime.TerminalAuth  // may be nil — token routes skipped
	allowedOrigins  []string
	loomServerURL   string
	state           StateQueries
	hub             *realtime.Hub
	serverStartedAt time.Time
	interaction     InteractionDependencies
}

// NewModule returns a Module. Either agentSvc or termAuth
// may be nil — routes that depend on them will simply not be registered.
func NewModule(
	termSvc interaction.TerminalTabs,
	agentSvc agentcoord.AgentService,
	termAuth *realtime.TerminalAuth,
	allowedOrigins []string,
	loomServerURL string,
	state StateQueries,
	hub *realtime.Hub,
	serverStartedAt time.Time,
	interactionDeps InteractionDependencies,
) *Module {
	return &Module{
		termSvc:         termSvc,
		agentSvc:        agentSvc,
		termAuth:        termAuth,
		allowedOrigins:  allowedOrigins,
		loomServerURL:   loomServerURL,
		state:           state,
		hub:             hub,
		serverStartedAt: serverStartedAt,
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
	if m.termSvc != nil {
		mux.HandleFunc(
			"POST /api/workspaces/{ws}/agents/{name}/terminal/session",
			HandleEnsureAgentTerminalSession(m.termSvc),
		)
	}
	if m.termSvc != nil {
		mux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/terminal/ws", HandleAgentTerminalWS(m.termSvc, m.termAuth, m.allowedOrigins))
	}

	// Main web terminal (PTY-backed, wterm wire format).
	if m.termAuth != nil {
		mux.HandleFunc("GET /api/workspaces/{ws}/terminal/token", HandleTerminalToken(m.termAuth))
	}
	if m.termSvc != nil {
		mux.HandleFunc(
			"GET /api/workspaces/{ws}/terminal/ws",
			HandleTerminalWS(
				m.termSvc,
				m.termAuth,
				m.allowedOrigins,
				m.loomServerURL,
				m.state,
				m.hub,
				m.serverStartedAt,
				m.interaction,
			),
		)
	}
}

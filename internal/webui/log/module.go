package log

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/agentcoord"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/misc"
)

// Module registers the workspace-scoped log streaming routes on a
// [*http.ServeMux].
//
// The agent log route is conditional on agentSvc being non-nil. Task log
// routes are always registered (their handler constructors take no parameters).
type Module struct {
	agentSvc agentcoord.AgentService // may be nil — agent log route skipped
}

// NewModule returns a Module. agentSvc may be nil — the agent log
// route will simply not be registered.
func NewModule(agentSvc agentcoord.AgentService) *Module {
	return &Module{agentSvc: agentSvc}
}

// Register implements [Module] by registering 2–3 log streaming routes.
func (m *Module) Register(mux *http.ServeMux) {
	// Agent log — conditional on agentSvc availability
	if m.agentSvc != nil {
		mux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/logs", misc.HandleGetAgentLog(m.agentSvc))
	}

	// Task log routes — always registered (zero-parameter constructors)
	mux.HandleFunc("GET /api/workspaces/{ws}/tasks/{id}/logs", misc.HandleListTaskPhases())
	mux.HandleFunc("GET /api/workspaces/{ws}/tasks/{id}/logs/{phase}", misc.HandleGetTaskLog())
}

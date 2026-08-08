package app

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/agentcoord"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/misc"
)

// LogModule registers the workspace-scoped log streaming routes on a
// [*http.ServeMux].
//
// The agent log route is conditional on agentSvc being non-nil. Task log
// routes are always registered (their handler constructors take no parameters).
type LogModule struct {
	agentSvc agentcoord.AgentService // may be nil — agent log route skipped
}

// NewLogModule returns a LogModule. agentSvc may be nil — the agent log
// route will simply not be registered.
func NewLogModule(agentSvc agentcoord.AgentService) *LogModule {
	return &LogModule{agentSvc: agentSvc}
}

// Register implements wsModule by registering 2–3 log streaming routes.
func (m *LogModule) Register(mux *http.ServeMux) {
	// Agent log — conditional on agentSvc availability
	if m.agentSvc != nil {
		mux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/logs", misc.HandleGetAgentLog(m.agentSvc))
	}

	// Task log routes — always registered (zero-parameter constructors)
	mux.HandleFunc("GET /api/workspaces/{ws}/tasks/{id}/logs", misc.HandleListTaskPhases())
	mux.HandleFunc("GET /api/workspaces/{ws}/tasks/{id}/logs/{phase}", misc.HandleGetTaskLog())
}

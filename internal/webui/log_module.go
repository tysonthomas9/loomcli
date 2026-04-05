package webui

import "net/http"

// LogModule registers the workspace-scoped log streaming routes on a
// [*http.ServeMux].
//
// The agent log route is conditional on agentSvc being non-nil. Task log
// routes are always registered (their handler constructors take no parameters).
type LogModule struct {
	agentSvc AgentService // may be nil — agent log route skipped
}

// NewLogModule returns a LogModule. agentSvc may be nil — the agent log
// route will simply not be registered.
func NewLogModule(agentSvc AgentService) *LogModule {
	return &LogModule{agentSvc: agentSvc}
}

// Register implements [Module] by registering 2–3 log streaming routes.
func (m *LogModule) Register(mux *http.ServeMux) {
	// Agent log — conditional on agentSvc availability
	if m.agentSvc != nil {
		mux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/logs", handleGetAgentLog(m.agentSvc))
	}

	// Task log routes — always registered (zero-parameter constructors)
	mux.HandleFunc("GET /api/workspaces/{ws}/tasks/{id}/logs", handleListTaskPhases())
	mux.HandleFunc("GET /api/workspaces/{ws}/tasks/{id}/logs/{phase}", handleGetTaskLog())
}

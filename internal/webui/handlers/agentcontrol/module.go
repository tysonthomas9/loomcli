package agentcontrol

import "net/http"

// Module registers the 5 workspace-scoped agent lifecycle control routes.
// Constructed only when AgentControlFn is non-nil (local daemon mode).
type Module struct {
	controlFn AgentControlFn
}

// NewModule returns a Module that proxies agent lifecycle commands to the
// daemon control socket via the given callback.
func NewModule(controlFn AgentControlFn) *Module {
	return &Module{controlFn: controlFn}
}

// Register implements webui.Module by registering the agent lifecycle
// control routes (start/stop/restart/yield). FE list-of-agents lookups go
// through /api/monitor/agents, not a per-workspace /agents list.
func (m *Module) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/stop", handleAgentStop(m.controlFn))
	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/start", handleAgentStart(m.controlFn))
	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/restart", handleAgentRestart(m.controlFn))
	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/yield", handleAgentYield(m.controlFn))
}

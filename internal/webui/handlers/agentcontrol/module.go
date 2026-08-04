// Package agentcontrol serves the workspace-scoped agent lifecycle HTTP routes
// (start/stop/restart/yield, plus a bare agent list) by forwarding each one to
// the local daemon's control socket through an injected callback. Wired only in
// local-daemon serve mode, by internal/cli/serve/daemonwire and
// internal/webui; the fleet-db-backed equivalents of the same routes live in
// internal/webui/handlers/agents.
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
// control routes (start/stop/restart/yield) plus the bare list. The FE
// reads /api/monitor/agents for its agent panel, but the bare list is
// the daemon-socket projection consumed by the `loom data agents list`
// CLI subcommand (internal/cli/data/agents.go).
func (m *Module) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/stop", handleAgentStop(m.controlFn))
	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/start", handleAgentStart(m.controlFn))
	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/restart", handleAgentRestart(m.controlFn))
	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/yield", handleAgentYield(m.controlFn))
	mux.HandleFunc("GET /api/workspaces/{ws}/agents", handleAgentList(m.controlFn))
}

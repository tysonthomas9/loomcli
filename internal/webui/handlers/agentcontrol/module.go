package agentcontrol

import "net/http"

// Module registers the 5 workspace-scoped agent lifecycle control routes.
// Constructed only when AgentControlFn is non-nil (local daemon mode).
type Module struct {
	controlFn AgentControlFn
	inputFn   AgentInputFn
	holdFn    ClaimHoldFn
}

// NewModule returns a Module that proxies agent lifecycle commands to the
// daemon control socket via the given callback. inputFn and holdFn may each be
// nil, which leaves the pending-input and claim-hold routes respectively
// unregistered (older daemon, remote mode).
func NewModule(controlFn AgentControlFn, inputFn AgentInputFn, holdFn ClaimHoldFn) *Module {
	return &Module{controlFn: controlFn, inputFn: inputFn, holdFn: holdFn}
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
	if m.inputFn != nil {
		// Answering shares this module — and therefore the same authz
		// boundary — with stop/start on purpose: resolving an agent's prompt
		// is exactly as consequential as controlling its lifecycle.
		mux.HandleFunc("GET /api/workspaces/{ws}/pending-inputs", handlePendingInputList(m.inputFn))
		mux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/input", handlePendingInputGet(m.inputFn))
		mux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/answer", handleAgentAnswer(m.inputFn))
	}
	if m.holdFn != nil {
		// The claim hold shares this module — and therefore the same authz
		// boundary — with stop/start: quiescing the whole workspace is at
		// least as consequential as stopping one agent.
		mux.HandleFunc("GET /api/workspaces/{ws}/claims/hold", handleClaimHoldGet(m.holdFn))
		mux.HandleFunc("POST /api/workspaces/{ws}/claims/hold", handleClaimHoldSet(m.holdFn))
		mux.HandleFunc("DELETE /api/workspaces/{ws}/claims/hold", handleClaimHoldRelease(m.holdFn))
	}
}

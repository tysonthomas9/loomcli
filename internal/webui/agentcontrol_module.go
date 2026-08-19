package webui

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/handlers/agentcontrol"
)

// NewAgentControlModule creates an agent control module that can be registered
// on a workspace-scoped mux. This wrapper exists so that the app package can
// create the module without importing handlers/agentcontrol directly.
func NewAgentControlModule(controlFn agentcontrol.AgentControlFn, inputFn agentcontrol.AgentInputFn, holdFn agentcontrol.ClaimHoldFn) interface{ Register(*http.ServeMux) } {
	return agentcontrol.NewModule(controlFn, inputFn, holdFn)
}

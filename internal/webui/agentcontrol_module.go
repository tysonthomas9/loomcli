package webui

import (
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/agentcontrol"
	"github.com/tysonthomas9/loomcli/internal/webui/route"
)

// NewAgentControlModule creates an agent control module that can be registered
// on a workspace-scoped mux. This wrapper exists so that the app package can
// create the module without importing handlers/agentcontrol directly.
func NewAgentControlModule(controlFn agentcontrol.AgentControlFn, inputFn agentcontrol.AgentInputFn, holdFn agentcontrol.ClaimHoldFn) interface{ Register(route.Router) } {
	return agentcontrol.NewModule(controlFn, inputFn, holdFn)
}

// NewClaimHoldModule creates the claim-hold-only daemon socket module used by
// store-backed servers whose agent lifecycle routes are provided separately.
func NewClaimHoldModule(holdFn agentcontrol.ClaimHoldFn) interface{ Register(route.Router) } {
	return agentcontrol.NewClaimHoldModule(holdFn)
}

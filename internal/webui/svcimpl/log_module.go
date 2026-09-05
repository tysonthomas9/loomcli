package svcimpl

import (
	webuilog "github.com/tysonthomas9/loomcli/internal/webui/log"
	"github.com/tysonthomas9/loomcli/internal/webui/route"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// LogModule wraps the webuilog.Module to avoid importing webui/log in the app package.
type LogModule = webuilog.Module

// NewLogModule creates a new agent log module.
func NewLogModule(agentSvc service.AgentService) interface{ Register(route.Router) } {
	return webuilog.NewModule(agentSvc)
}

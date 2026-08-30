package svcimpl

import (
	"net/http"

	webuilog "github.com/tysonthomas9/loomcli/internal/webui/log"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// LogModule wraps the webuilog.Module to avoid importing webui/log in the app package.
type LogModule = webuilog.Module

// NewLogModule creates a new agent log module.
func NewLogModule(agentSvc service.AgentService, sseTokens *realtime.TokenStore) interface{ Register(*http.ServeMux) } {
	return webuilog.NewModule(agentSvc, sseTokens)
}

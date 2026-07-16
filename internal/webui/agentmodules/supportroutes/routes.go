package supportroutes

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/handlers/onboarding"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// Module is the workspace route registration contract shared by web modules.
type Module interface {
	Register(*http.ServeMux)
}

// New composes service-only support routes without importing persistence.
func New(issueSvc service.IssueService, agentSvc service.AgentService) Module {
	return onboarding.NewModule(issueSvc, agentSvc)
}

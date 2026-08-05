package supportroutes

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	workflowcataloghttp "github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog/httpapi"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/onboarding"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// Module is the workspace route registration contract shared by web modules.
type Module interface {
	Register(*http.ServeMux)
}

// New composes service-only support routes without importing persistence.
func New(issueSvc service.IssueService, agentAPI agents.API, authority workflowcataloghttp.OperatorAuthorityResolver) Module {
	return onboarding.NewModule(issueSvc, agentAPI, authority)
}

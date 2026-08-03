package onboarding

import (
	"net/http"

	workflowcataloghttp "github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog/httpapi"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// Module registers onboarding orchestration routes.
type Module struct {
	issueSvc  service.IssueService
	agents    AgentLifecycleAPI
	authority workflowcataloghttp.OperatorAuthorityResolver
}

func NewModule(issueSvc service.IssueService, agents AgentLifecycleAPI, authority workflowcataloghttp.OperatorAuthorityResolver) *Module {
	return &Module{issueSvc: issueSvc, agents: agents, authority: authority}
}

func (m *Module) Register(mux *http.ServeMux) {
	if m.issueSvc == nil || m.agents == nil || m.authority == nil {
		return
	}
	mux.HandleFunc("POST /api/workspaces/{ws}/onboarding/first-task", HandleRunFirstTask(m.issueSvc, m.agents, m.authority))
}

package onboarding

import (
	"net/http"

	workflowcataloghttp "github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog/httpapi"
	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
)

// Module registers onboarding orchestration routes.
type Module struct {
	workItems workitems.API
	agents    AgentLifecycleAPI
	authority workflowcataloghttp.OperatorAuthorityResolver
}

func NewModule(workItems workitems.API, agents AgentLifecycleAPI, authority workflowcataloghttp.OperatorAuthorityResolver) *Module {
	return &Module{workItems: workItems, agents: agents, authority: authority}
}

func (m *Module) Register(mux *http.ServeMux) {
	if m.workItems == nil || m.agents == nil || m.authority == nil {
		return
	}
	mux.HandleFunc("POST /api/workspaces/{ws}/onboarding/first-task", HandleRunFirstTask(m.workItems, m.agents, m.authority))
}

package onboarding

import (
	"github.com/tysonthomas9/loomcli/internal/webui/route"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// Module registers onboarding orchestration routes.
type Module struct {
	issueSvc service.IssueService
	agentSvc service.AgentService
}

func NewModule(issueSvc service.IssueService, agentSvc service.AgentService) *Module {
	return &Module{issueSvc: issueSvc, agentSvc: agentSvc}
}

func (m *Module) Register(mux route.Router) {
	if m.issueSvc == nil || m.agentSvc == nil {
		return
	}
	mux.HandleFunc("POST /api/workspaces/{ws}/onboarding/first-task", HandleRunFirstTask(m.issueSvc, m.agentSvc))
}

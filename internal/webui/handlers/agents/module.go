package agents

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// Module registers fleet-db-backed agent assignment routes.
type Module struct {
	agentSvc service.AgentService
}

func NewModule(agentSvc service.AgentService) *Module {
	return &Module{agentSvc: agentSvc}
}

func (m *Module) Register(mux *http.ServeMux) {
	if m.agentSvc == nil {
		return
	}
	mux.HandleFunc("GET /api/workspaces/{ws}/agents", HandleList(m.agentSvc))
	mux.HandleFunc("POST /api/workspaces/{ws}/agents", HandleCreate(m.agentSvc))
	mux.HandleFunc("PATCH /api/workspaces/{ws}/agents/{name}", HandleUpdate(m.agentSvc))
	mux.HandleFunc("DELETE /api/workspaces/{ws}/agents/{name}", HandleDelete(m.agentSvc))
	mux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/queue", HandleQueueUnsupported)

	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/stop", HandleStop(m.agentSvc))
	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/start", HandleStart(m.agentSvc))
	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/restart", HandleRestart(m.agentSvc))
	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/yield", HandleYield(m.agentSvc))
}

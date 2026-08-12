package agents

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// Module registers fleet-db-backed agent assignment routes.
type Module struct {
	agentSvc service.AgentService
	store    store.Store
	hub      *realtime.Hub
}

func NewModule(agentSvc service.AgentService, st store.Store, hub *realtime.Hub) *Module {
	return &Module{agentSvc: agentSvc, store: st, hub: hub}
}

func (m *Module) Register(mux *http.ServeMux) {
	if m.agentSvc == nil && m.store == nil {
		return
	}
	mux.HandleFunc("GET /api/workspaces/{ws}/interactive-prompts", HandleInteractivePrompts())
	mux.HandleFunc("GET /api/workspaces/{ws}/agents", m.listAgents)
	mux.HandleFunc("POST /api/workspaces/{ws}/agents", m.createAgent)
	mux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}", m.getAgent)
	mux.HandleFunc("PATCH /api/workspaces/{ws}/agents/{name}", m.patchAgent)
	mux.HandleFunc("DELETE /api/workspaces/{ws}/agents/{name}", m.deleteAgent)
	mux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/queue", HandleQueueUnsupported)

	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/stop", HandleStop(m.agentSvc, m.hub))
	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/start", HandleStart(m.agentSvc, m.hub))
	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/restart", HandleRestart(m.agentSvc, m.hub))
	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/yield", HandleYield(m.agentSvc, m.hub))
	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{id}/enable", m.setRecordEnabled(true))
	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{id}/disable", m.setRecordEnabled(false))
	mux.HandleFunc("GET /api/workspaces/{ws}/agents/{id}/runs", m.listAgentRuns)
}

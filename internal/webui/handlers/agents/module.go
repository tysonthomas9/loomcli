// Package agents serves the workspace-scoped agent-assignment HTTP routes —
// list/create/update/delete plus the start/stop/restart/yield lifecycle
// commands — over service.AgentService (fleet-db), broadcasting each mutation
// to the SSE hub. Registered by internal/webui/app; the daemon-socket variant
// of the same lifecycle routes is internal/webui/handlers/agentcontrol.
package agents

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// Module registers fleet-db-backed agent assignment routes.
type Module struct {
	agentSvc service.AgentService
	hub      *realtime.Hub
}

func NewModule(agentSvc service.AgentService, hub *realtime.Hub) *Module {
	return &Module{agentSvc: agentSvc, hub: hub}
}

func (m *Module) Register(mux *http.ServeMux) {
	if m.agentSvc == nil {
		return
	}
	mux.HandleFunc("GET /api/workspaces/{ws}/interactive-prompts", HandleInteractivePrompts())
	mux.HandleFunc("GET /api/workspaces/{ws}/agents", HandleList(m.agentSvc))
	mux.HandleFunc("POST /api/workspaces/{ws}/agents", HandleCreate(m.agentSvc, m.hub))
	mux.HandleFunc("PATCH /api/workspaces/{ws}/agents/{name}", HandleUpdate(m.agentSvc, m.hub))
	mux.HandleFunc("DELETE /api/workspaces/{ws}/agents/{name}", HandleDelete(m.agentSvc, m.hub))
	mux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/queue", HandleQueueUnsupported)

	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/stop", HandleStop(m.agentSvc, m.hub))
	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/start", HandleStart(m.agentSvc, m.hub))
	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/restart", HandleRestart(m.agentSvc, m.hub))
	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/yield", HandleYield(m.agentSvc, m.hub))
}

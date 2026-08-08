package agents

import (
	"context"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// Module registers fleet-db-backed agent assignment routes.
type Module struct {
	agentSvc    service.AgentService
	hub         *realtime.Hub
	provisioner leadProvisioner
}

type leadProvisioner interface {
	ProvisionForAgent(ctx context.Context, workspaceKey, agentName string) error
}

func NewModule(agentSvc service.AgentService, hub *realtime.Hub, provisioner leadProvisioner) *Module {
	return &Module{agentSvc: agentSvc, hub: hub, provisioner: provisioner}
}

func (m *Module) Register(mux *http.ServeMux) {
	if m.agentSvc == nil {
		return
	}
	mux.HandleFunc("GET /api/workspaces/{ws}/interactive-prompts", HandleInteractivePrompts())
	mux.HandleFunc("GET /api/workspaces/{ws}/agents", HandleList(m.agentSvc))
	mux.HandleFunc("POST /api/workspaces/{ws}/agents", HandleCreate(m.agentSvc, m.hub, m.provisioner))
	mux.HandleFunc("PATCH /api/workspaces/{ws}/agents/{name}", HandleUpdate(m.agentSvc, m.hub))
	mux.HandleFunc("DELETE /api/workspaces/{ws}/agents/{name}", HandleDelete(m.agentSvc, m.hub))
	mux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/queue", HandleQueueUnsupported)

	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/stop", HandleStop(m.agentSvc, m.hub))
	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/start", HandleStart(m.agentSvc, m.hub))
	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/restart", HandleRestart(m.agentSvc, m.hub))
	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/yield", HandleYield(m.agentSvc, m.hub))
}

package agents

import (
	"context"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	workflowcataloghttp "github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog/httpapi"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// BindingGrantCompatibility is the narrow Phase-3 connector seam needed by
// agent deletion. Grant creation remains owned by the existing connector store
// until the Connectors capability moves in Phase 5.
type BindingGrantCompatibility interface {
	RevokeBindingGrants(ctx context.Context, workspaceKey, bindingID string) (int, error)
}

// Config composes the agent transport with Automation's public binding surface.
// The composite Store remains only for AgentService, Role, Grant, Driver, and
// run-history concerns that move in later phases.
type Config struct {
	AgentService         service.AgentService
	Store                store.Store
	Hub                  *realtime.Hub
	Bindings             automation.BindingOperations
	OperatorAuthority    workflowcataloghttp.OperatorAuthorityResolver
	WorkspaceFromContext func(context.Context) string
	BindingGrants        BindingGrantCompatibility
}

// Module registers fleet-db-backed agent assignment routes.
type Module struct {
	agentSvc             service.AgentService
	store                store.Store
	hub                  *realtime.Hub
	bindings             automation.BindingOperations
	operatorAuthority    workflowcataloghttp.OperatorAuthorityResolver
	workspaceFromContext func(context.Context) string
	bindingGrants        BindingGrantCompatibility
}

// New constructs the Automation-aware agent HTTP module.
func New(config Config) *Module {
	return &Module{
		agentSvc: config.AgentService, store: config.Store, hub: config.Hub,
		bindings: config.Bindings, operatorAuthority: config.OperatorAuthority,
		workspaceFromContext: config.WorkspaceFromContext, bindingGrants: config.BindingGrants,
	}
}

// NewModule is retained for non-binding legacy/test composition. Binding reads
// and writes fail closed until callers migrate to New(Config).
func NewModule(agentSvc service.AgentService, st store.Store, hub *realtime.Hub) *Module {
	return New(Config{AgentService: agentSvc, Store: st, Hub: hub})
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

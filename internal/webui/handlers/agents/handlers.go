package agents

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/webui/server/dto"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

func HandleList(agentSvc service.AgentService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agents, err := agentSvc.ListAgents(r.Context(), r.PathValue("ws"))
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, dto.NewListResponse(agents, len(agents)))
	}
}

func HandleCreate(agentSvc service.AgentService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in service.AgentCreateInput
		if err := handler.ReadJSON(w, r, &in); err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		ws := r.PathValue("ws")
		if in.WorkspaceKey == "" {
			in.WorkspaceKey = ws
		}
		if in.WorkspaceKey != ws {
			handler.HandleServiceError(w, service.ErrValidation("workspace_key must match request workspace"))
			return
		}
		created, err := agentSvc.CreateAgent(r.Context(), in)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusCreated, created)
	}
}

func HandleUpdate(agentSvc service.AgentService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var patch service.AgentUpdateInput
		if err := handler.ReadJSON(w, r, &patch); err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		if patch.State != nil && !validAgentState(*patch.State) {
			handler.HandleServiceError(w, service.ErrValidation("invalid state"))
			return
		}
		updated, err := agentSvc.UpdateAgent(r.Context(), r.PathValue("ws"), r.PathValue("name"), patch)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, updated)
	}
}

func HandleDelete(agentSvc service.AgentService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := agentSvc.DeleteAgent(r.Context(), r.PathValue("ws"), r.PathValue("name")); err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, dto.NewMessageResponse("agent deleted"))
	}
}

func HandleLifecycleUnsupported(w http.ResponseWriter, _ *http.Request) {
	handler.HandleServiceError(w, service.ErrNotImplemented("agent lifecycle control is not available in fleet-db store mode"))
}

func HandleQueueUnsupported(w http.ResponseWriter, _ *http.Request) {
	handler.HandleServiceError(w, service.ErrNotImplemented("agent-specific queue is not available in fleet-db store mode; use monitor task queues"))
}

func validAgentState(state domain.AgentState) bool {
	switch state {
	case "", domain.AgentStateIdle, domain.AgentStateActive, domain.AgentStateStopped:
		return true
	default:
		return false
	}
}

package agents

import (
	"encoding/json"
	"fmt"
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
		if patch.DesiredState != nil && !validAgentDesiredState(*patch.DesiredState) {
			handler.HandleServiceError(w, service.ErrValidation("invalid desired_state"))
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

func HandleStart(agentSvc service.AgentService) http.HandlerFunc {
	return handleLifecycle(agentSvc, lifecyclePatch{
		state:       domain.AgentStateActive,
		desired:     domain.AgentDesiredRunning,
		commandType: "start",
		status:      http.StatusOK,
		message:     "started",
	})
}

func HandleStop(agentSvc service.AgentService) http.HandlerFunc {
	return handleLifecycle(agentSvc, lifecyclePatch{
		state:       domain.AgentStateStopped,
		desired:     domain.AgentDesiredStopped,
		commandType: "stop",
		status:      http.StatusOK,
		message:     "stopped",
	})
}

func HandleRestart(agentSvc service.AgentService) http.HandlerFunc {
	return handleLifecycle(agentSvc, lifecyclePatch{
		state:       domain.AgentStateActive,
		desired:     domain.AgentDesiredRunning,
		commandType: "restart",
		status:      http.StatusAccepted,
		message:     "restart requested",
	})
}

func HandleYield(agentSvc service.AgentService) http.HandlerFunc {
	return handleLifecycle(agentSvc, lifecyclePatch{
		state:       domain.AgentStateIdle,
		desired:     domain.AgentDesiredDraining,
		commandType: "yield",
		status:      http.StatusAccepted,
		message:     "yield requested",
	})
}

func HandleQueueUnsupported(w http.ResponseWriter, _ *http.Request) {
	handler.HandleServiceError(w, service.ErrNotImplemented("agent-specific queue is not available in fleet-db store mode; use monitor task queues"))
}

type lifecyclePatch struct {
	state       domain.AgentState
	desired     domain.AgentDesiredState
	commandType string
	payload     map[string]string
	status      int
	message     string
}

type lifecycleRequest struct {
	Payload map[string]string `json:"payload,omitempty"`
	TaskID  string            `json:"task_id,omitempty"`
}

func handleLifecycle(agentSvc service.AgentService, patch lifecyclePatch) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		payload := patch.payload
		if r.Body != nil && r.ContentLength != 0 {
			var req lifecycleRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				handler.HandleServiceError(w, service.ErrValidation("invalid request body"))
				return
			}
			if len(req.Payload) > 0 {
				payload = map[string]string{}
				for k, v := range patch.payload {
					payload[k] = v
				}
				for k, v := range req.Payload {
					payload[k] = v
				}
			}
			if req.TaskID != "" {
				if payload == nil {
					payload = map[string]string{}
				}
				payload["task_id"] = req.TaskID
			}
		}
		updated, err := agentSvc.RequestAgentLifecycle(r.Context(), r.PathValue("ws"), r.PathValue("name"), service.AgentLifecycleInput{
			State:        patch.state,
			DesiredState: patch.desired,
			CommandType:  patch.commandType,
			Payload:      payload,
		})
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		handler.WriteJSON(w, patch.status, dto.NewMessageResponse(fmt.Sprintf("agent %q %s", updated.Name, patch.message)))
	}
}

func validAgentState(state domain.AgentState) bool {
	switch state {
	case "", domain.AgentStateIdle, domain.AgentStateActive, domain.AgentStateStopped:
		return true
	default:
		return false
	}
}

func validAgentDesiredState(state domain.AgentDesiredState) bool {
	switch state {
	case "", domain.AgentDesiredStopped, domain.AgentDesiredIdle, domain.AgentDesiredRunning, domain.AgentDesiredDraining:
		return true
	default:
		return false
	}
}

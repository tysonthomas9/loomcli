package agents

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/webui/server/dto"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

type interactivePromptsResponse struct {
	Prompts []domain.BuiltinInteractivePrompt `json:"prompts"`
}

func HandleList(agentSvc service.AgentService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agents, err := agentSvc.ListAgents(r.Context(), requestWorkspaceID(r))
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		items := make([]supervisedAgentDTO, 0, len(agents))
		for _, agent := range agents {
			if agent == nil {
				continue
			}
			items = append(items, newSupervisedAgentDTO(agent))
		}
		handler.WriteJSON(w, http.StatusOK, dto.NewListResponse(items, len(items)))
	}
}

func HandleInteractivePrompts() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		handler.WriteJSON(w, http.StatusOK, interactivePromptsResponse{
			Prompts: visibleInteractivePrompts(),
		})
	}
}

func visibleInteractivePrompts() []domain.BuiltinInteractivePrompt {
	prompts := domain.BuiltinInteractivePrompts()
	out := make([]domain.BuiltinInteractivePrompt, 0, len(prompts))
	for _, prompt := range prompts {
		if prompt.Hidden {
			continue
		}
		out = append(out, prompt)
	}
	return out
}

func HandleCreate(agentSvc service.AgentService, hub *realtime.Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ws := requestWorkspaceID(r)
		ctx, span := startSpan(r.Context(), "service.Agent.Create",
			attribute.String("loom.workspace", ws))
		defer span.End()

		var in service.AgentCreateInput
		if err := handler.ReadJSON(w, r, &in); err != nil {
			recordErr(span, err)
			handler.HandleServiceError(w, err)
			return
		}
		if in.WorkspaceKey == "" {
			in.WorkspaceKey = ws
		}
		if in.WorkspaceKey != ws {
			err := service.ErrValidation("workspace_key must match request workspace")
			recordErr(span, err)
			handler.HandleServiceError(w, err)
			return
		}
		// Agent name is an internal short identifier (per-workspace handle like
		// "falcon"), not user-supplied free-form text — safe to record.
		span.SetAttributes(attribute.String("loom.agent", in.Name))
		created, err := agentSvc.CreateAgent(ctx, in)
		if err != nil {
			recordErr(span, err)
			handler.HandleServiceError(w, err)
			return
		}
		broadcastAgentRefresh(hub, ws, created.Name, r.Header.Get("X-Actor"))
		handler.WriteJSON(w, http.StatusCreated, newSupervisedAgentDTO(created))
	}
}

func HandleUpdate(agentSvc service.AgentService, hub *realtime.Hub) http.HandlerFunc {
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
		ws := requestWorkspaceID(r)
		updated, err := agentSvc.UpdateAgent(r.Context(), ws, r.PathValue("name"), patch)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		broadcastAgentRefresh(hub, ws, updated.Name, r.Header.Get("X-Actor"))
		handler.WriteJSON(w, http.StatusOK, newSupervisedAgentDTO(updated))
	}
}

func HandleDelete(agentSvc service.AgentService, hub *realtime.Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ws := requestWorkspaceID(r)
		name := r.PathValue("name")
		if err := agentSvc.DeleteAgent(r.Context(), ws, name); err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		broadcastAgentRefresh(hub, ws, name, r.Header.Get("X-Actor"))
		handler.WriteJSON(w, http.StatusOK, dto.NewMessageResponse("agent deleted"))
	}
}

func HandleStart(agentSvc service.AgentService, hub *realtime.Hub) http.HandlerFunc {
	return handleLifecycle(agentSvc, hub, lifecyclePatch{
		state:       domain.AgentStateActive,
		desired:     domain.AgentDesiredRunning,
		commandType: "start",
		status:      http.StatusOK,
		message:     "started",
	})
}

func HandleStop(agentSvc service.AgentService, hub *realtime.Hub) http.HandlerFunc {
	return handleLifecycle(agentSvc, hub, lifecyclePatch{
		state:       domain.AgentStateStopped,
		desired:     domain.AgentDesiredStopped,
		commandType: "stop",
		status:      http.StatusOK,
		message:     "stopped",
	})
}

func HandleRestart(agentSvc service.AgentService, hub *realtime.Hub) http.HandlerFunc {
	return handleLifecycle(agentSvc, hub, lifecyclePatch{
		state:       domain.AgentStateActive,
		desired:     domain.AgentDesiredRunning,
		commandType: "restart",
		status:      http.StatusOK,
		message:     "restarted",
	})
}

func HandleYield(agentSvc service.AgentService, hub *realtime.Hub) http.HandlerFunc {
	return handleLifecycle(agentSvc, hub, lifecyclePatch{
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
	Force   bool              `json:"force,omitempty"`
}

func handleLifecycle(agentSvc service.AgentService, hub *realtime.Hub, patch lifecyclePatch) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ws := requestWorkspaceID(r)
		name := r.PathValue("name")
		var req lifecycleRequest
		if r.Body != nil && r.ContentLength != 0 {
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				handler.HandleServiceError(w, service.ErrValidation("invalid request body"))
				return
			}
		}

		effective, input := resolveLifecycleRequest(patch, req)
		updated, err := agentSvc.RequestAgentLifecycle(r.Context(), ws, name, input)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		// A non-force Stop remains a graceful daemon yield for supervised
		// workers, but interactive agents are stopped synchronously by their
		// process-local terminal owner. Reflect the settled placement-specific
		// result instead of claiming that an interactive runtime merely yielded.
		if patch.commandType == "stop" && !req.Force && updated.DesiredState == domain.AgentDesiredStopped {
			effective = patch
		}
		broadcastAgentRefresh(hub, ws, updated.Name, r.Header.Get("X-Actor"))
		handler.WriteJSON(w, effective.status, dto.NewMessageResponse(fmt.Sprintf("agent %q %s", updated.Name, effective.message)))
	}
}

func resolveLifecycleRequest(patch lifecyclePatch, req lifecycleRequest) (lifecyclePatch, service.AgentLifecycleInput) {
	effective := patch
	if patch.commandType == "stop" {
		if req.Force {
			effective.payload = map[string]string{"force": "true"}
			effective.message = "force-stopped"
		} else {
			effective = lifecyclePatch{
				state:       domain.AgentStateIdle,
				desired:     domain.AgentDesiredDraining,
				commandType: "yield",
				status:      http.StatusAccepted,
				message:     "yield requested",
			}
		}
	}

	payload := cloneLifecyclePayload(effective.payload)
	for key, value := range req.Payload {
		if payload == nil {
			payload = map[string]string{}
		}
		payload[key] = value
	}
	if req.TaskID != "" {
		if payload == nil {
			payload = map[string]string{}
		}
		payload["task_id"] = req.TaskID
	}
	if patch.commandType == "stop" && req.Force {
		payload["force"] = "true"
	}

	// Preserve the requested Stop for the service layer. It owns the role-kind
	// placement decision: workers convert a graceful Stop to a daemon yield,
	// while interactive agents terminate their local PTY directly.
	inputPatch := effective
	if patch.commandType == "stop" {
		inputPatch = patch
	}

	return effective, service.AgentLifecycleInput{
		State:        inputPatch.state,
		DesiredState: inputPatch.desired,
		CommandType:  inputPatch.commandType,
		Payload:      payload,
	}
}

func cloneLifecyclePayload(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func requestWorkspaceID(r *http.Request) string {
	if ws := middleware.WorkspaceFromContext(r.Context()); ws != "" {
		return ws
	}
	return r.PathValue("ws")
}

func broadcastAgentRefresh(hub *realtime.Hub, workspace, agentName, actor string) {
	if hub == nil || workspace == "" {
		return
	}
	hub.Broadcast(&realtime.MutationPayload{
		Type:        "refresh",
		EntityType:  "agent",
		EntityID:    agentName,
		Action:      "agent.refresh",
		Title:       agentName,
		Actor:       actor,
		Timestamp:   time.Now().UTC().Format(time.RFC3339Nano),
		WorkspaceID: workspace,
	})
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

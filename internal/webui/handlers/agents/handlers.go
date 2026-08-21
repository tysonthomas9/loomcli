package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
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
		handler.WriteJSON(w, http.StatusOK, dto.NewListResponse(agents, len(agents)))
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

func HandleCreate(agentSvc service.AgentService, hub *realtime.Hub, provisioner leadProvisioner) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ws := requestWorkspaceID(r)
		actor := r.Header.Get("X-Actor")
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
		broadcastAgentRefresh(hub, ws, created.Name, actor)
		kickLeadProvision(agentSvc, provisioner, hub, ws, created.Name, actor)
		handler.WriteJSON(w, http.StatusCreated, created)
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
		handler.WriteJSON(w, http.StatusOK, updated)
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

// HandleStart and HandleRestart re-drive lead provisioning so a lead stranded
// by a transient create-time provision failure (quota, provider 5xx, expired
// key) recovers on the next start instead of being wedged at desired=running
// with no placement. kickLeadProvision is idempotent, so a healthy running lead
// is resumed, never duplicated.
func HandleStart(agentSvc service.AgentService, hub *realtime.Hub, provisioner leadProvisioner) http.HandlerFunc {
	return handleLifecycle(agentSvc, hub, provisioner, lifecyclePatch{
		state:       domain.AgentStateActive,
		desired:     domain.AgentDesiredRunning,
		commandType: "start",
		status:      http.StatusOK,
		message:     "started",
	})
}

func HandleStop(agentSvc service.AgentService, hub *realtime.Hub) http.HandlerFunc {
	return handleLifecycle(agentSvc, hub, nil, lifecyclePatch{
		state:       domain.AgentStateStopped,
		desired:     domain.AgentDesiredStopped,
		commandType: "stop",
		status:      http.StatusOK,
		message:     "stopped",
	})
}

func HandleRestart(agentSvc service.AgentService, hub *realtime.Hub, provisioner leadProvisioner) http.HandlerFunc {
	return handleLifecycle(agentSvc, hub, provisioner, lifecyclePatch{
		state:       domain.AgentStateActive,
		desired:     domain.AgentDesiredRunning,
		commandType: "restart",
		status:      http.StatusAccepted,
		message:     "restart requested",
	})
}

func HandleYield(agentSvc service.AgentService, hub *realtime.Hub) http.HandlerFunc {
	return handleLifecycle(agentSvc, hub, nil, lifecyclePatch{
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

func handleLifecycle(agentSvc service.AgentService, hub *realtime.Hub, provisioner leadProvisioner, patch lifecyclePatch) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ws := requestWorkspaceID(r)
		name := r.PathValue("name")
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
		updated, err := agentSvc.RequestAgentLifecycle(r.Context(), ws, name, service.AgentLifecycleInput{
			State:        patch.state,
			DesiredState: patch.desired,
			CommandType:  patch.commandType,
			Payload:      payload,
		})
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		actor := r.Header.Get("X-Actor")
		broadcastAgentRefresh(hub, ws, updated.Name, actor)
		if patch.desired == domain.AgentDesiredRunning {
			kickLeadProvision(agentSvc, provisioner, hub, ws, updated.Name, actor)
		}
		handler.WriteJSON(w, patch.status, dto.NewMessageResponse(fmt.Sprintf("agent %q %s", updated.Name, patch.message)))
	}
}

// kickLeadProvision eagerly (re)drives Daytona lead provisioning in the
// background. ProvisionForAgent self-filters to interactive Daytona leads and
// is idempotent: an already-live placement is resumed, never duplicated, so it
// is safe to call on both create and every start/restart. This is the only
// recovery path when a create-time provision fails transiently (quota, provider
// 5xx, expired key) and would otherwise strand the lead at desired=running with
// no placement and nothing to re-trigger it.
const provisionAttemptErrorMaxLen = 500

func kickLeadProvision(agentSvc service.AgentService, provisioner leadProvisioner, hub *realtime.Hub, ws, name, actor string) {
	if provisioner == nil {
		return
	}
	go func() {
		persistLeadProvisionAttempt(agentSvc, ws, name, domain.LeadProvisionOutcomeInProgress, "", time.Now().UTC())
		broadcastAgentRefresh(hub, ws, name, actor)

		pctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
		defer cancel()
		if err := provisioner.ProvisionForAgent(pctx, ws, name); err != nil {
			persistLeadProvisionAttempt(agentSvc, ws, name, domain.LeadProvisionOutcomeFailed, truncateProvisionAttemptError(err.Error()), time.Now().UTC())
			slog.Error("eager lead provisioning failed", "ws", ws, "agent", name, "err", err)
			broadcastAgentRefresh(hub, ws, name, actor)
			return
		}
		persistLeadProvisionAttempt(agentSvc, ws, name, domain.LeadProvisionOutcomeSucceeded, "", time.Now().UTC())
		broadcastAgentRefresh(hub, ws, name, actor)
	}()
}

func persistLeadProvisionAttempt(agentSvc service.AgentService, ws, name, outcome, attemptError string, at time.Time) {
	if agentSvc == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := agentSvc.UpdateAgent(ctx, ws, name, service.AgentUpdateInput{
		LastProvisionOutcome: &outcome,
		LastProvisionError:   &attemptError,
		LastProvisionAt:      &at,
	}); err != nil {
		slog.Warn("persist eager lead provision attempt failed", "ws", ws, "agent", name, "outcome", outcome, "err", err)
	}
}

func truncateProvisionAttemptError(message string) string {
	message = strings.TrimSpace(message)
	if len(message) <= provisionAttemptErrorMaxLen {
		return message
	}
	return message[:provisionAttemptErrorMaxLen] + "…"
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

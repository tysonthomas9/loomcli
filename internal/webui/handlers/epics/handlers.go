package epics

import (
	"net/http"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/epicrunner"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/server/dto"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

// runEpicRequest is the JSON payload for POST /epics/{id}/run.
type runEpicRequest struct {
	Lead string `json:"lead"`
}

type runEpicResponse struct {
	EpicID                string `json:"epic_id"`
	LeadName              string `json:"lead_name,omitempty"`
	OrchestratorSessionID string `json:"orchestrator_session_id,omitempty"`
	State                 string `json:"state"`
	DeliveryState         string `json:"delivery_state,omitempty"`
}

// handleRunEpic starts or resumes a lead-owned epic run using the same
// epicrunner.Start service that `loom epic run` uses for lead binding.
func handleRunEpic(st store.Store, hub *realtime.Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsKey := r.PathValue("ws")
		epicID := r.PathValue("id")
		if epicID == "" {
			handler.WriteJSON(w, http.StatusBadRequest, dto.NewErrorResponse("epic id required", "bad_request"))
			return
		}

		var req runEpicRequest
		if err := handler.ReadJSON(w, r, &req); err != nil {
			handler.WriteJSON(w, http.StatusBadRequest, dto.NewErrorResponse("invalid request body", "bad_request"))
			return
		}
		req.Lead = strings.TrimSpace(req.Lead)
		if req.Lead == "" {
			handler.WriteJSON(w, http.StatusBadRequest, dto.NewErrorResponse("lead required", "bad_request"))
			return
		}

		result, err := epicrunner.Start(r.Context(), st, epicrunner.StartInput{
			WorkspaceKey: wsKey,
			EpicID:       epicID,
			LeadName:     req.Lead,
			Mutate:       true,
		})
		if err != nil {
			writeEpicRunnerError(w, err)
			return
		}
		if result == nil {
			handler.WriteJSON(w, http.StatusInternalServerError, dto.NewErrorResponse("run epic returned no result", "internal"))
			return
		}
		broadcastAgentRefresh(hub, wsKey, result.LeadName, r.Header.Get("X-Actor"))
		handler.WriteJSON(w, http.StatusOK, runEpicResponse{
			EpicID:                result.EpicID,
			LeadName:              result.LeadName,
			OrchestratorSessionID: result.OrchestratorSessionID,
			State:                 string(result.State),
			DeliveryState:         result.DeliveryState,
		})
	}
}

func writeEpicRunnerError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	code := "internal"
	switch epicrunner.ErrorKindOf(err) {
	case epicrunner.ErrorKindValidation:
		status = http.StatusBadRequest
		code = "bad_request"
	case epicrunner.ErrorKindNotFound:
		status = http.StatusNotFound
		code = "not_found"
	case epicrunner.ErrorKindConflict:
		status = http.StatusConflict
		code = "conflict"
	case epicrunner.ErrorKindUnavailable:
		status = http.StatusServiceUnavailable
		code = "unavailable"
	}
	handler.WriteJSON(w, status, dto.NewErrorResponse(err.Error(), code))
}

func broadcastAgentRefresh(hub *realtime.Hub, workspace, agentName, actor string) {
	if hub == nil || workspace == "" || agentName == "" {
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

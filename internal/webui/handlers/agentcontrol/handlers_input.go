package agentcontrol

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/server/dto"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
)

// The web half of the human answer path: GET shows what an agent is waiting
// on, POST resolves it. Both are thin proxies over the daemon control socket —
// the daemon's registry owns validation (live request, matching id, offered
// option), so a stale browser tab gets the daemon's precise error, not a
// silent misdelivery.

// handlePendingInputList handles GET /api/workspaces/{ws}/pending-inputs —
// every agent currently waiting on a human, for the monitor panel's badge.
func handlePendingInputList(inputFn AgentInputFn) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writePendingInputs(w, inputFn, "")
	}
}

// handlePendingInputGet handles GET /api/workspaces/{ws}/agents/{name}/input.
func handlePendingInputGet(inputFn AgentInputFn) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writePendingInputs(w, inputFn, r.PathValue("name"))
	}
}

func writePendingInputs(w http.ResponseWriter, inputFn AgentInputFn, agent string) {
	result, err := inputFn("agent_input_get", agent, nil)
	if err != nil {
		writeDaemonError(w, err)
		return
	}
	if !result.Success {
		writeControlError(w, result)
		return
	}
	var pending []PendingInputView
	if err := json.Unmarshal(result.Data, &pending); err != nil {
		handler.WriteJSON(w, http.StatusBadGateway,
			dto.NewErrorResponse("malformed pending-input payload from daemon", "daemon_error"))
		return
	}
	handler.WriteJSON(w, http.StatusOK, pending)
}

// handleAgentAnswer handles POST /api/workspaces/{ws}/agents/{name}/answer.
func handleAgentAnswer(inputFn AgentInputFn) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")

		var req answerRequest
		if err := handler.ReadJSON(w, r, &req); err != nil {
			handler.WriteJSON(w, http.StatusBadRequest,
				dto.NewErrorResponse("invalid request body", "bad_request"))
			return
		}
		if req.OptionID == "" && req.Text == "" && !req.Decline {
			handler.WriteJSON(w, http.StatusBadRequest,
				dto.NewErrorResponse("an answer needs option_id, text, or decline", "bad_request"))
			return
		}

		args, err := json.Marshal(req)
		if err != nil {
			handler.WriteJSON(w, http.StatusBadRequest,
				dto.NewErrorResponse("invalid request body", "bad_request"))
			return
		}
		result, err := inputFn("agent_input_answer", name, args)
		if err != nil {
			writeDaemonError(w, err)
			return
		}
		if !result.Success {
			writeControlError(w, result)
			return
		}
		handler.WriteJSON(w, http.StatusOK,
			dto.NewMessageResponse(fmt.Sprintf("answer delivered to agent %q", name)))
	}
}

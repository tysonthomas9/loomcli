package agentcontrol

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/webui/server/dto"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
)

// handleAgentStop handles POST /api/workspaces/{ws}/agents/{name}/stop.
// Without force (or empty body): sends agent_yield, returns 202.
// With {"force": true}: sends agent_stop(force=true), returns 200.
func handleAgentStop(controlFn AgentControlFn) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")

		var req stopRequest
		if r.Body != nil && r.ContentLength > 0 {
			if err := handler.ReadJSON(w, r, &req); err != nil {
				handler.WriteJSON(w, http.StatusBadRequest,
					dto.NewErrorResponse("invalid request body", "bad_request"))
				return
			}
		}

		if req.Force {
			result, err := controlFn("agent_stop", name, true)
			if err != nil {
				writeDaemonError(w, err)
				return
			}
			if !result.Success {
				writeControlError(w, result)
				return
			}
			handler.WriteJSON(w, http.StatusOK,
				dto.NewMessageResponse(fmt.Sprintf("agent %q force-stopped", name)))
			return
		}

		// Non-force: send yield and return 202.
		result, err := controlFn("agent_yield", name, false)
		if err != nil {
			writeDaemonError(w, err)
			return
		}
		if !result.Success {
			writeControlError(w, result)
			return
		}
		handler.WriteJSON(w, http.StatusAccepted,
			dto.NewMessageResponse(fmt.Sprintf("yield requested for agent %q; poll GET /agents to track status", name)))
	}
}

// handleAgentStart handles POST /api/workspaces/{ws}/agents/{name}/start.
func handleAgentStart(controlFn AgentControlFn) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		result, err := controlFn("agent_start", name, false)
		if err != nil {
			writeDaemonError(w, err)
			return
		}
		if !result.Success {
			writeControlError(w, result)
			return
		}
		handler.WriteJSON(w, http.StatusOK,
			dto.NewMessageResponse(fmt.Sprintf("agent %q started", name)))
	}
}

// handleAgentRestart handles POST /api/workspaces/{ws}/agents/{name}/restart.
func handleAgentRestart(controlFn AgentControlFn) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		result, err := controlFn("agent_restart", name, false)
		if err != nil {
			writeDaemonError(w, err)
			return
		}
		if !result.Success {
			writeControlError(w, result)
			return
		}
		handler.WriteJSON(w, http.StatusOK,
			dto.NewMessageResponse(fmt.Sprintf("agent %q restarted", name)))
	}
}

// handleAgentYield handles POST /api/workspaces/{ws}/agents/{name}/yield.
func handleAgentYield(controlFn AgentControlFn) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		result, err := controlFn("agent_yield", name, false)
		if err != nil {
			writeDaemonError(w, err)
			return
		}
		if !result.Success {
			writeControlError(w, result)
			return
		}
		handler.WriteJSON(w, http.StatusOK,
			dto.NewMessageResponse(fmt.Sprintf("yield requested for agent %q", name)))
	}
}

// handleAgentList handles GET /api/workspaces/{ws}/agents.
func handleAgentList(controlFn AgentControlFn) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		result, err := controlFn("agent_list", "", false)
		if err != nil {
			writeDaemonError(w, err)
			return
		}
		if !result.Success {
			writeControlError(w, result)
			return
		}

		var entries []AgentControlEntry
		if result.Data != nil {
			if err := json.Unmarshal(result.Data, &entries); err != nil {
				handler.WriteJSON(w, http.StatusBadGateway,
					dto.NewErrorResponse("invalid agent list data from daemon", "daemon_error"))
				return
			}
		}
		handler.WriteJSON(w, http.StatusOK, dto.NewListResponse(entries, len(entries)))
	}
}

// writeDaemonError handles the case where AgentControlFn returns a non-nil error
// (daemon unreachable or socket timeout).
func writeDaemonError(w http.ResponseWriter, err error) {
	if strings.Contains(err.Error(), "timeout") {
		handler.WriteJSON(w, http.StatusGatewayTimeout,
			dto.NewErrorResponse("daemon did not respond in time", "daemon_timeout"))
		return
	}
	handler.WriteJSON(w, http.StatusServiceUnavailable,
		dto.NewErrorResponse("daemon is not running", "daemon_unavailable"))
}

// writeControlError handles the case where the daemon responded with Success=false.
func writeControlError(w http.ResponseWriter, result *AgentControlResult) {
	status, code := classifyAgentControlError(result)
	handler.WriteJSON(w, status, dto.NewErrorResponse(result.Error, code))
}

// classifyAgentControlError maps daemon error strings to HTTP status codes.
func classifyAgentControlError(result *AgentControlResult) (int, string) {
	e := result.Error
	switch {
	case strings.Contains(e, "not found"):
		return http.StatusNotFound, "agent_not_found"
	case strings.Contains(e, "already stopped"),
		strings.Contains(e, "already running"),
		strings.Contains(e, "not stopped"):
		return http.StatusConflict, "agent_conflict"
	default:
		return http.StatusBadGateway, "daemon_error"
	}
}

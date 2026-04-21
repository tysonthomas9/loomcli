package agentcontrol

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/webui/server/dto"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
)

// resolveAgentKey builds the compound agent key ("repo/worktree" or bare worktree)
// the daemon control socket expects. The worktree name comes from the {name} URL
// path segment and the optional "repo" query parameter disambiguates two agents
// that share the same bare worktree name across repos within a workspace. URL
// patterns cannot embed "/" in a path segment, so the repo travels as a query
// param rather than an extra segment.
func resolveAgentKey(r *http.Request) string {
	name := r.PathValue("name")
	repo := r.URL.Query().Get("repo")
	if repo != "" {
		return repo + "/" + name
	}
	return name
}

// handleAgentStop handles POST /api/workspaces/{ws}/agents/{name}/stop.
// Without force (or empty body): sends agent_yield, returns 202.
// With {"force": true}: sends agent_stop(force=true), returns 200.
// The optional ?repo= query parameter disambiguates duplicate worktree names
// across repos within a workspace.
func handleAgentStop(controlFn AgentControlFn) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := resolveAgentKey(r)

		var req stopRequest
		if r.Body != nil && r.ContentLength > 0 {
			if err := handler.ReadJSON(w, r, &req); err != nil {
				handler.WriteJSON(w, http.StatusBadRequest,
					dto.NewErrorResponse("invalid request body", "bad_request"))
				return
			}
		}

		if req.Force {
			result, err := controlFn("agent_stop", key, true)
			if err != nil {
				writeDaemonError(w, err)
				return
			}
			if !result.Success {
				writeControlError(w, result)
				return
			}
			handler.WriteJSON(w, http.StatusOK,
				dto.NewMessageResponse(fmt.Sprintf("agent %q force-stopped", key)))
			return
		}

		// Non-force: send yield and return 202.
		result, err := controlFn("agent_yield", key, false)
		if err != nil {
			writeDaemonError(w, err)
			return
		}
		if !result.Success {
			writeControlError(w, result)
			return
		}
		handler.WriteJSON(w, http.StatusAccepted,
			dto.NewMessageResponse(fmt.Sprintf("yield requested for agent %q; poll GET /agents to track status", key)))
	}
}

// handleAgentStart handles POST /api/workspaces/{ws}/agents/{name}/start.
// Accepts an optional ?repo= query parameter to disambiguate duplicate worktree names.
func handleAgentStart(controlFn AgentControlFn) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := resolveAgentKey(r)
		result, err := controlFn("agent_start", key, false)
		if err != nil {
			writeDaemonError(w, err)
			return
		}
		if !result.Success {
			writeControlError(w, result)
			return
		}
		handler.WriteJSON(w, http.StatusOK,
			dto.NewMessageResponse(fmt.Sprintf("agent %q started", key)))
	}
}

// handleAgentRestart handles POST /api/workspaces/{ws}/agents/{name}/restart.
// Accepts an optional ?repo= query parameter to disambiguate duplicate worktree names.
func handleAgentRestart(controlFn AgentControlFn) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := resolveAgentKey(r)
		result, err := controlFn("agent_restart", key, false)
		if err != nil {
			writeDaemonError(w, err)
			return
		}
		if !result.Success {
			writeControlError(w, result)
			return
		}
		handler.WriteJSON(w, http.StatusOK,
			dto.NewMessageResponse(fmt.Sprintf("agent %q restarted", key)))
	}
}

// handleAgentYield handles POST /api/workspaces/{ws}/agents/{name}/yield.
// Accepts an optional ?repo= query parameter to disambiguate duplicate worktree names.
func handleAgentYield(controlFn AgentControlFn) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := resolveAgentKey(r)
		result, err := controlFn("agent_yield", key, false)
		if err != nil {
			writeDaemonError(w, err)
			return
		}
		if !result.Success {
			writeControlError(w, result)
			return
		}
		handler.WriteJSON(w, http.StatusOK,
			dto.NewMessageResponse(fmt.Sprintf("yield requested for agent %q", key)))
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

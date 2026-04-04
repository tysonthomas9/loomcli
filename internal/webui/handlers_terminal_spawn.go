package webui

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"

	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// terminalSpawnRequest is the JSON body for POST /api/terminal/spawn.
type terminalSpawnRequest struct {
	SessionName string `json:"session_name"`
	Backend     string `json:"backend"`
}

// terminalSpawnData is the data payload in a successful spawn response.
type terminalSpawnData struct {
	SessionName string `json:"session_name"`
	Backend     string `json:"backend"`
	Command     string `json:"command"`
	Created     bool   `json:"created"`
}

// terminalSpawnResponse is the JSON response for POST /api/terminal/spawn.
type terminalSpawnResponse struct {
	Success bool               `json:"success"`
	Data    *terminalSpawnData `json:"data,omitempty"`
	Error   string             `json:"error,omitempty"`
}

// issueSessionPattern matches issue-linked session names: "issue-{project}-{number}".
var issueSessionPattern = regexp.MustCompile(`^issue-(.+)-(\d+)$`)

// extractIssueID converts a sanitized session name back to an issue ID.
// e.g., "issue-loomcli-fghge-1" → "loomcli-fghge.1"
func extractIssueID(sessionName string) string {
	m := issueSessionPattern.FindStringSubmatch(sessionName)
	if m == nil {
		return ""
	}
	return m[1] + "." + m[2]
}

// handleTerminalSpawn returns a handler that creates a tmux session for a given issue and backend.
func handleTerminalSpawn(svc TerminalService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)

		var req terminalSpawnRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				respondJSON(w, http.StatusRequestEntityTooLarge, terminalSpawnResponse{
					Error: "request body too large",
				})
				return
			}
			respondJSON(w, http.StatusBadRequest, terminalSpawnResponse{
				Error: "invalid request body",
			})
			return
		}

		wsID := middleware.WorkspaceFromContext(r.Context())
		params := &SpawnParams{
			SessionName: req.SessionName,
			Backend:     req.Backend,
		}

		result, err := svc.SpawnSession(r.Context(), wsID, params)
		if err != nil {
			WriteServiceError(w, err)
			return
		}

		respondJSON(w, http.StatusOK, terminalSpawnResponse{
			Success: true,
			Data: &terminalSpawnData{
				SessionName: result.SessionName,
				Backend:     result.Backend,
				Command:     result.Command,
				Created:     result.Created,
			},
		})
	}
}

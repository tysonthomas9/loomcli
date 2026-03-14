package webui

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
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

// terminalSpawner is an interface for the subset of TerminalManager used by the spawn handler.
type terminalSpawner interface {
	Spawn(name, command string, cols, rows uint16) (bool, error)
}

// handleTerminalSpawn returns a handler that creates a tmux session for a given issue and backend.
func handleTerminalSpawn(manager *TerminalManager) http.HandlerFunc {
	if manager == nil {
		return func(w http.ResponseWriter, r *http.Request) {
			respondJSON(w, http.StatusServiceUnavailable, terminalSpawnResponse{
				Error: "terminal manager not initialized",
			})
		}
	}
	return handleTerminalSpawnImpl(manager)
}

// handleTerminalSpawnImpl is the internal testable implementation that accepts an interface.
func handleTerminalSpawnImpl(manager terminalSpawner) http.HandlerFunc {
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

		if req.SessionName == "" {
			respondJSON(w, http.StatusBadRequest, terminalSpawnResponse{
				Error: "missing required field: session_name",
			})
			return
		}
		if req.Backend == "" {
			respondJSON(w, http.StatusBadRequest, terminalSpawnResponse{
				Error: "missing required field: backend",
			})
			return
		}

		// Sanitize dots to dashes (issue IDs like loomcli-fghge.1 contain dots)
		sanitizedName := strings.ReplaceAll(req.SessionName, ".", "-")

		if !validSessionName.MatchString(sanitizedName) {
			respondJSON(w, http.StatusBadRequest, terminalSpawnResponse{
				Error: fmt.Sprintf("invalid session name %q after sanitization: must match [a-zA-Z0-9_-]+", sanitizedName),
			})
			return
		}

		if !isValidBackend(req.Backend) {
			respondJSON(w, http.StatusBadRequest, terminalSpawnResponse{
				Error: fmt.Sprintf("invalid backend %q; valid: %s", req.Backend, strings.Join(validBackends, ", ")),
			})
			return
		}

		// The command is the backend name itself (e.g., "claude")
		command := req.Backend

		created, err := manager.Spawn(sanitizedName, command, 120, 40)
		if err != nil {
			respondJSON(w, http.StatusInternalServerError, terminalSpawnResponse{
				Error: fmt.Sprintf("failed to spawn terminal session: %v", err),
			})
			return
		}

		respondJSON(w, http.StatusOK, terminalSpawnResponse{
			Success: true,
			Data: &terminalSpawnData{
				SessionName: sanitizedName,
				Backend:     req.Backend,
				Command:     command,
				Created:     created,
			},
		})
	}
}

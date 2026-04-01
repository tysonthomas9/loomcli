package webui

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/webui/sessionhistory"
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
func handleTerminalSpawn(manager *TerminalManager, sessionHistoryStore *sessionhistory.Store) http.HandlerFunc {
	if manager == nil {
		return func(w http.ResponseWriter, r *http.Request) {
			respondJSON(w, http.StatusServiceUnavailable, terminalSpawnResponse{
				Error: "terminal manager not initialized",
			})
		}
	}
	return handleTerminalSpawnImplWithHistory(manager, sessionHistoryStore)
}

// handleTerminalSpawnImplWithHistory is the implementation that accepts an interface and optional session history store.
func handleTerminalSpawnImplWithHistory(manager terminalSpawner, sessionHistoryStore *sessionhistory.Store) http.HandlerFunc {
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

		var command string
		if req.Backend == "shell" {
			command = shellCommand()
		} else if !isValidBackend(req.Backend) {
			respondJSON(w, http.StatusBadRequest, terminalSpawnResponse{
				Error: fmt.Sprintf("invalid backend %q; valid: %s", req.Backend, strings.Join(validBackends, ", ")),
			})
			return
		} else {
			// The command is the backend name itself (e.g., "claude")
			command = req.Backend
		}

		created, err := manager.Spawn(sanitizedName, command, 120, 40)
		if err != nil {
			respondJSON(w, http.StatusInternalServerError, terminalSpawnResponse{
				Error: fmt.Sprintf("failed to spawn terminal session: %v", err),
			})
			return
		}

		// Record session creation in session history (only for issue-linked sessions).
		if created && sessionHistoryStore != nil {
			if issueID := extractIssueID(sanitizedName); issueID != "" {
				now := time.Now().UTC()
				record := sessionhistory.SessionRecord{
					ID:          fmt.Sprintf("%s:%d", sanitizedName, now.Unix()),
					SessionName: sanitizedName,
					IssueID:     issueID,
					Backend:     req.Backend,
					Status:      "active",
					Launcher:    "user",
					StartedAt:   now,
				}
				wsID := WorkspaceFromContext(r.Context())
				if err := sessionHistoryStore.Add(r.Context(), wsID, record); err != nil {
					slog.Warn("failed to record session history", "session", sanitizedName, "err", err)
				}
			}
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

// handleTerminalSpawnImpl is the internal testable implementation that accepts an interface (no session history).
func handleTerminalSpawnImpl(manager terminalSpawner) http.HandlerFunc {
	return handleTerminalSpawnImplWithHistory(manager, nil)
}

package webui

import (
	"net/http"
	"strings"
)

// handleGetScrollback returns the scrollback buffer for a terminal session.
// GET /api/workspaces/{ws}/terminal/sessions/{session}/scrollback
func handleGetScrollback(termManager *TerminalManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session := r.PathValue("session")
		if session == "" {
			respondError(w, http.StatusBadRequest, "session name is required")
			return
		}

		content, err := termManager.CaptureScrollback(session)
		if err != nil {
			if strings.Contains(err.Error(), "not found") {
				respondError(w, http.StatusNotFound, "session not found")
				return
			}
			respondError(w, http.StatusInternalServerError, "failed to capture scrollback")
			return
		}

		lines := 0
		if content != "" {
			lines = strings.Count(content, "\n") + 1
		}

		respondJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
			"data": map[string]interface{}{
				"content": content,
				"lines":   lines,
			},
		})
	}
}

package webui

import (
	"net/http"
)

// handleGetScrollback returns the scrollback buffer for a terminal session.
func handleGetScrollback(svc TerminalService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session := r.PathValue("session")

		result, err := svc.GetScrollback(r.Context(), session)
		if err != nil {
			WriteServiceError(w, err)
			return
		}

		respondJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
			"data": map[string]interface{}{
				"content": result.Content,
				"lines":   result.Lines,
			},
		})
	}
}

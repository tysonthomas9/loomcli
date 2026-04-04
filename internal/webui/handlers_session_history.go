package webui

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// handleListSessionHistory returns session history records for an issue.
func handleListSessionHistory(svc SessionService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		issueID := r.PathValue("issueId")

		records, err := svc.ListSessionHistory(r.Context(), wsID, issueID)
		if err != nil {
			WriteServiceError(w, err)
			return
		}

		respondJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
			"data":    records,
		})
	}
}

// handleGetSessionScrollback returns the scrollback content for a completed session.
func handleGetSessionScrollback(svc SessionService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		issueID := r.PathValue("issueId")
		recordID := r.PathValue("recordId")

		result, err := svc.GetSessionScrollback(r.Context(), wsID, issueID, recordID)
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

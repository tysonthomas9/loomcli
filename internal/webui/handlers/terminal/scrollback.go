package terminal

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// HandleGetScrollback returns the scrollback buffer for a terminal session.
func HandleGetScrollback(svc service.TerminalService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session := r.PathValue("session")

		wsID := middleware.WorkspaceFromContext(r.Context())
		result, err := svc.GetScrollback(r.Context(), wsID, session)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}

		handler.WriteJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
			"data": map[string]interface{}{
				"content": result.Content,
				"lines":   result.Lines,
			},
		})
	}
}

package terminal

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

// HandleListSessionsByIssue returns a map of issue_id -> session_names, read
// from the Redis-backed tab metadata store. The tmux-backed session-listing
// endpoints were removed; this one survives because the data lives in Redis.
func HandleListSessionsByIssue(svc terminal.TerminalService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sessionMap, err := svc.ListSessionsByIssue(r.Context())
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
			"data":    sessionMap,
		})
	}
}

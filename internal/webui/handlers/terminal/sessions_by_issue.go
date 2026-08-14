package terminal

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
)

// HandleListSessionsByIssue returns a map of issue_id -> session_names, read
// from the Redis-backed tab metadata store. The tmux-backed session-listing
// endpoints were removed; this one survives because the data lives in Redis.
func HandleListSessionsByIssue(svc interaction.TerminalTabs) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sessionMap, err := svc.ListSessionsByIssue(r.Context())
		if err != nil {
			handler.HandleTerminalError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
			"data":    sessionMap,
		})
	}
}

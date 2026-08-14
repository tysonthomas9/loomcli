package terminal

import (
	"log/slog"
	"net/http"
	"net/url"
	"regexp"

	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

// validTerminalSession matches alphanumeric characters, hyphens, and underscores.
var validTerminalSession = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// originHosts extracts host (with port) from origin URLs for use as
// nhooyr.io/websocket OriginPatterns. For example, "http://localhost:3000"
// becomes "localhost:3000". Malformed URLs are logged and skipped.
func originHosts(origins []string) []string {
	if len(origins) == 0 {
		return nil
	}
	hosts := make([]string, 0, len(origins))
	for _, o := range origins {
		u, err := url.Parse(o)
		if err != nil || u.Host == "" {
			slog.Warn("skipping malformed origin", "origin", o, "err", err)
			continue
		}
		hosts = append(hosts, u.Host)
	}
	return hosts
}

// HandleTerminalToken returns a handler that generates a one-time terminal auth token.
func HandleTerminalToken(auth *realtime.TerminalAuth) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session := r.URL.Query().Get("session")

		var userID string
		if identity, ok := middleware.UserIdentityFromContext(r.Context()); ok {
			userID = identity.UserID
		}

		workspace := middleware.WorkspaceFromContext(r.Context())
		if err := interaction.ValidateTerminalSessionName(session); err != nil {
			handler.RespondError(w, http.StatusBadRequest, "invalid session name")
			return
		}
		token, err := auth.GenerateToken(session, workspace, userID)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}

		w.Header().Set("Cache-Control", "no-store")
		handler.WriteJSON(w, http.StatusOK, map[string]string{"token": token})
	}
}

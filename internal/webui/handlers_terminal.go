package webui

import (
	"log/slog"
	"net/http"
	"net/url"
	"regexp"

	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

// Constants for terminal WebSocket communication.
const (
	terminalReadBufSize = 4096
	resizeMsgMarker     = 0x01
	resizeMsgLen        = 5
	maxTerminalCols     = 500
	maxTerminalRows     = 200
	wsReadLimit         = 32768 // 32KB; explicit limit for defense-in-depth (matches nhooyr.io/websocket default)
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

// handleTerminalToken returns a handler that generates a one-time terminal auth token.
func handleTerminalToken(svc TerminalService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session := r.URL.Query().Get("session")

		var userID string
		if identity, ok := middleware.UserIdentityFromContext(r.Context()); ok {
			userID = identity.UserID
		}

		token, err := svc.GenerateToken(r.Context(), session, userID)
		if err != nil {
			writeServiceError(w, err)
			return
		}

		w.Header().Set("Cache-Control", "no-store")
		respondJSON(w, http.StatusOK, map[string]string{"token": token})
	}
}

// handleTerminalRestart returns a handler that restarts the terminal session.
func handleTerminalRestart(svc TerminalService, auth *realtime.TerminalAuth) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session := r.URL.Query().Get("session")
		if session == "" || !validTerminalSession.MatchString(session) {
			respondJSON(w, http.StatusBadRequest, map[string]interface{}{"success": false, "error": "invalid session name"})
			return
		}

		// Validate terminal token
		if auth != nil {
			token := r.URL.Query().Get("token")
			if _, err := auth.ValidateToken(token, session); err != nil {
				respondJSON(w, http.StatusUnauthorized, map[string]interface{}{"success": false, "error": "terminal authentication failed"})
				return
			}
		}

		wsID := middleware.WorkspaceFromContext(r.Context())
		result, err := svc.RestartSession(r.Context(), wsID, session)
		if err != nil {
			writeServiceError(w, err)
			return
		}

		respondJSON(w, http.StatusOK, map[string]interface{}{"success": true, "backend": result.Backend})
	}
}

// handleTerminalKill returns a handler that forcibly kills a terminal session.
func handleTerminalKill(svc TerminalService, auth *realtime.TerminalAuth) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session := r.URL.Query().Get("session")
		if session == "" || !validTerminalSession.MatchString(session) {
			respondJSON(w, http.StatusBadRequest, map[string]interface{}{"success": false, "error": "invalid session"})
			return
		}

		if auth != nil {
			token := r.URL.Query().Get("token")
			if _, err := auth.ValidateToken(token, session); err != nil {
				respondJSON(w, http.StatusUnauthorized, map[string]interface{}{"success": false, "error": "terminal authentication failed"})
				return
			}
		}

		if err := svc.KillSession(r.Context(), session); err != nil {
			writeServiceError(w, err)
			return
		}

		respondJSON(w, http.StatusOK, map[string]interface{}{"success": true})
	}
}

// handleTerminalSessionStatus returns a handler that checks whether a tmux session is alive.
func handleTerminalSessionStatus(svc TerminalService, auth *realtime.TerminalAuth) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session := r.URL.Query().Get("session")
		if session == "" || !validTerminalSession.MatchString(session) {
			respondJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid session"})
			return
		}

		if auth != nil {
			token := r.URL.Query().Get("token")
			if _, err := auth.ValidateToken(token, session); err != nil {
				respondJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": "terminal authentication failed"})
				return
			}
		}

		result, err := svc.GetSessionStatus(r.Context(), session)
		if err != nil {
			writeServiceError(w, err)
			return
		}

		resp := map[string]interface{}{
			"alive": result.Alive,
		}
		if result.ExitReason != "" {
			resp["exit_reason"] = result.ExitReason
		}
		respondJSON(w, http.StatusOK, resp)
	}
}

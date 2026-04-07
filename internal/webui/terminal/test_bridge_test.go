package terminal

import (
	"encoding/json"
	"net/http"
	"regexp"

	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// validTerminalSessionTest is a local copy for test-only handler wrappers.
var validTerminalSessionTest = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// handleTerminalKill is a test-local HTTP handler that mirrors
// handlers/terminal.HandleTerminalKill. We duplicate it here because the
// terminal package cannot import handlers/terminal (import cycle).
func handleTerminalKill(svc service.TerminalService, auth *realtime.TerminalAuth) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session := r.URL.Query().Get("session")
		if session == "" || !validTerminalSessionTest.MatchString(session) {
			writeTestJSON(w, http.StatusBadRequest, map[string]interface{}{"success": false, "error": "invalid session"})
			return
		}

		if auth != nil {
			token := r.URL.Query().Get("token")
			if _, err := auth.ValidateToken(token, session); err != nil {
				writeTestJSON(w, http.StatusUnauthorized, map[string]interface{}{"success": false, "error": "terminal authentication failed"})
				return
			}
		}

		if err := svc.KillSession(r.Context(), session); err != nil {
			handleTestServiceError(w, err)
			return
		}

		writeTestJSON(w, http.StatusOK, map[string]interface{}{"success": true})
	}
}

// handleTerminalSessionStatus is a test-local HTTP handler that mirrors
// handlers/terminal.HandleTerminalSessionStatus.
func handleTerminalSessionStatus(svc service.TerminalService, auth *realtime.TerminalAuth) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session := r.URL.Query().Get("session")
		if session == "" || !validTerminalSessionTest.MatchString(session) {
			writeTestJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid session"})
			return
		}

		if auth != nil {
			token := r.URL.Query().Get("token")
			if _, err := auth.ValidateToken(token, session); err != nil {
				writeTestJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": "terminal authentication failed"})
				return
			}
		}

		result, err := svc.GetSessionStatus(r.Context(), session)
		if err != nil {
			handleTestServiceError(w, err)
			return
		}

		resp := map[string]interface{}{
			"alive": result.Alive,
		}
		if result.ExitReason != "" {
			resp["exit_reason"] = result.ExitReason
		}
		writeTestJSON(w, http.StatusOK, resp)
	}
}

// writeTestJSON writes a JSON response with the given status code.
func writeTestJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}

// handleTestServiceError maps service errors to HTTP responses.
func handleTestServiceError(w http.ResponseWriter, err error) {
	svcErr, ok := err.(*service.ServiceError)
	if !ok {
		writeTestJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": err.Error()})
		return
	}
	status := http.StatusInternalServerError
	switch svcErr.Kind {
	case service.KindValidation:
		status = http.StatusBadRequest
	case service.KindNotFound:
		status = http.StatusNotFound
	case service.KindForbidden:
		status = http.StatusForbidden
	case service.KindUnavailable:
		status = http.StatusServiceUnavailable
	}
	writeTestJSON(w, status, map[string]interface{}{"error": svcErr.Message})
}

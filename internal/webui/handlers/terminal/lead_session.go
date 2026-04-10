package terminal

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// leadSessionRequest is the JSON body for POST /api/workspaces/{ws}/terminal/lead-session.
type leadSessionRequest struct {
	Message string `json:"message"`
	Backend string `json:"backend"`
}

// leadSessionData is the data payload in a successful lead-session response.
type leadSessionData struct {
	SessionName string `json:"session_name"`
	Backend     string `json:"backend"`
}

// leadSessionResponse is the JSON response for POST /api/workspaces/{ws}/terminal/lead-session.
type leadSessionResponse struct {
	Success bool             `json:"success"`
	Data    *leadSessionData `json:"data,omitempty"`
	Error   string           `json:"error,omitempty"`
}

// HandleCreateLeadSession returns a handler that spawns a detached tmux session
// running `loom lead --backend <backend> --message <message>`. The message is
// baked into the argv so the backend agent receives the user's request as part
// of its initial prompt — no send-keys, no readiness polling, no TUI scraping.
func HandleCreateLeadSession(svc service.TerminalService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, handler.MaxRequestBody)

		var req leadSessionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				handler.WriteJSON(w, http.StatusRequestEntityTooLarge, leadSessionResponse{
					Error: "request body too large",
				})
				return
			}
			handler.WriteJSON(w, http.StatusBadRequest, leadSessionResponse{
				Error: "invalid request body",
			})
			return
		}

		wsID := middleware.WorkspaceFromContext(r.Context())
		result, err := svc.CreateLeadSession(r.Context(), wsID, &service.LeadSessionParams{
			Message: req.Message,
			Backend: req.Backend,
		})
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}

		handler.WriteJSON(w, http.StatusOK, leadSessionResponse{
			Success: true,
			Data: &leadSessionData{
				SessionName: result.SessionName,
				Backend:     result.Backend,
			},
		})
	}
}

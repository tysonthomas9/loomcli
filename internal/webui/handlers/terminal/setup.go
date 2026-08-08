package terminal

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

type terminalSetupRequest struct {
	Backend string `json:"backend"`
	Action  string `json:"action"`
}

// HandleStartTerminalSetup starts a typed setup command inside a
// workspace-scoped PTY session and returns the tab the frontend should attach
// to. The handler never accepts arbitrary shell input; service.StartSetup maps
// backend/action pairs to known commands.
func HandleStartTerminalSetup(svc terminal.TerminalService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workspace := middleware.WorkspaceFromContext(r.Context())

		var req terminalSetupRequest
		if err := handler.DecodeOneJSON(w, r, &req, handler.JSONDecodeOptions{}); err != nil {
			handler.WriteJSON(w, http.StatusBadRequest, tabMetadataResponse{
				Success: false,
				Error:   "invalid request body",
			})
			return
		}

		result, err := svc.StartSetup(r.Context(), workspace, terminal.TerminalSetupRequest{
			Backend: req.Backend,
			Action:  req.Action,
		})
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}

		handler.WriteJSON(w, http.StatusOK, tabMetadataResponse{
			Success: true,
			Data:    result,
		})
	}
}

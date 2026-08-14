package terminal

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	loomapi "github.com/tysonthomas9/loomcli/internal/platform/loomapi/gen"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// HandleStartTerminalSetup starts a typed setup command inside a
// workspace-scoped PTY session and returns the tab the frontend should attach
// to. The handler never accepts arbitrary shell input; service.StartSetup maps
// backend/action pairs to known commands.
func HandleStartTerminalSetup(svc interaction.TerminalTabs) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workspace := middleware.WorkspaceFromContext(r.Context())

		var req loomapi.TerminalSetupRequest
		if err := handler.DecodeOneJSON(w, r, &req, handler.JSONDecodeOptions{DisallowUnknownFields: true}); err != nil {
			handler.WriteJSON(w, http.StatusBadRequest, tabMetadataResponse{
				Success: false,
				Error:   "invalid request body",
			})
			return
		}

		result, err := svc.StartSetup(r.Context(), workspace, interaction.TerminalSetupRequest{
			Backend: string(req.Backend),
			Action:  string(req.Action),
		})
		if err != nil {
			handler.HandleTerminalError(w, err)
			return
		}

		handler.WriteJSON(w, http.StatusOK, tabMetadataResponse{
			Success: true,
			Data:    terminalSetupDTO(result),
		})
	}
}

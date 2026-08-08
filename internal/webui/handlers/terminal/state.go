package terminal

import (
	"encoding/json"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

// HandleGetTerminalState returns the persisted terminal UI state.
func HandleGetTerminalState(svc terminal.TerminalService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		activeTab, err := svc.GetTerminalState(r.Context(), wsID)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}

		handler.WriteJSON(w, http.StatusOK, map[string]interface{}{
			"active_tab": activeTab,
		})
	}
}

// HandlePatchTerminalState updates the persisted terminal UI state.
func HandlePatchTerminalState(svc terminal.TerminalService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ActiveTab string `json:"active_tab"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, handler.MaxRequestBody)).Decode(&req); err != nil {
			handler.RespondError(w, http.StatusBadRequest, "invalid request body")
			return
		}

		wsID := middleware.WorkspaceFromContext(r.Context())
		if err := svc.PatchTerminalState(r.Context(), wsID, req.ActiveTab); err != nil {
			handler.HandleServiceError(w, err)
			return
		}

		handler.WriteJSON(w, http.StatusOK, map[string]interface{}{
			"active_tab": req.ActiveTab,
		})
	}
}

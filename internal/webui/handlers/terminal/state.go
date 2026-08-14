package terminal

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// HandleGetTerminalState returns the persisted terminal UI state.
func HandleGetTerminalState(state PresentationState) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		activeTab, err := state.GetActiveTab(r.Context(), wsID)
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
func HandlePatchTerminalState(state PresentationState) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ActiveTab string `json:"active_tab"`
		}
		if err := handler.DecodeOneJSON(w, r, &req, handler.JSONDecodeOptions{}); err != nil {
			handler.RespondError(w, http.StatusBadRequest, "invalid request body")
			return
		}

		wsID := middleware.WorkspaceFromContext(r.Context())
		if err := state.SetActiveTab(r.Context(), wsID, req.ActiveTab); err != nil {
			handler.HandleServiceError(w, err)
			return
		}

		handler.WriteJSON(w, http.StatusOK, map[string]interface{}{
			"active_tab": req.ActiveTab,
		})
	}
}

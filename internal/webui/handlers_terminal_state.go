package webui

import (
	"encoding/json"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// handleGetTerminalState returns the persisted terminal UI state.
func handleGetTerminalState(svc TerminalService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		activeTab, err := svc.GetTerminalState(r.Context(), wsID)
		if err != nil {
			writeServiceError(w, err)
			return
		}

		respondJSON(w, http.StatusOK, map[string]interface{}{
			"active_tab": activeTab,
		})
	}
}

// handlePatchTerminalState updates the persisted terminal UI state.
func handlePatchTerminalState(svc TerminalService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ActiveTab string `json:"active_tab"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBody)).Decode(&req); err != nil {
			respondError(w, http.StatusBadRequest, "invalid request body")
			return
		}

		wsID := middleware.WorkspaceFromContext(r.Context())
		if err := svc.PatchTerminalState(r.Context(), wsID, req.ActiveTab); err != nil {
			writeServiceError(w, err)
			return
		}

		respondJSON(w, http.StatusOK, map[string]interface{}{
			"active_tab": req.ActiveTab,
		})
	}
}

package webui

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// handleWorkspaceDelete returns a handler for DELETE /api/workspaces/{ws}.
func handleWorkspaceDelete(svc service.WorkspaceService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		if wsID == "" {
			respondJSON(w, http.StatusBadRequest, workspaceResponse{Success: false, Error: "workspace ID is required"})
			return
		}
		data, err := svc.DeleteWorkspace(r.Context(), wsID)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		respondJSON(w, http.StatusOK, workspaceResponse{Success: true, Data: data})
	}
}

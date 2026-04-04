package webui

import (
	"net/http"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// handleListWorkspaces returns GET /api/workspaces — a list of all registered
// workspaces with basic status information.
func handleListWorkspaces(svc service.WorkspaceService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		items, err := svc.ListWorkspaces(r.Context())
		if err != nil {
			WriteServiceError(w, err)
			return
		}
		respondJSON(w, http.StatusOK, map[string]interface{}{
			"success":    true,
			"workspaces": items,
		})
	}
}

// handleGetWorkspace returns GET /api/workspaces/{ws} — full WorkspaceData
// (same shape as /api/workspaces/active) so the frontend uses the same
// unwrap<WorkspaceData>() logic for both endpoints.
func handleGetWorkspace(svc service.WorkspaceService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := strings.TrimSpace(r.PathValue("ws"))
		if wsID == "" {
			respondError(w, http.StatusBadRequest, "workspace ID is required")
			return
		}
		data, err := svc.GetWorkspace(r.Context(), wsID)
		if err != nil {
			WriteServiceError(w, err)
			return
		}
		respondJSON(w, http.StatusOK, workspaceResponse{Success: true, Data: data})
	}
}

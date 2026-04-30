package workspace

import (
	"net/http"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// HandleListWorkspaces returns GET /api/workspaces — a list of all registered
// workspaces with basic status information.
func HandleListWorkspaces(svc service.WorkspaceService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		items, err := svc.ListWorkspaces(r.Context())
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, map[string]interface{}{
			"success":    true,
			"workspaces": items,
		})
	}
}

// HandleGetWorkspace returns GET /api/workspaces/{ws} — full WorkspaceData
// (same shape as /api/workspaces/active) so the frontend uses the same
// unwrap<WorkspaceData>() logic for both endpoints.
func HandleGetWorkspace(svc service.WorkspaceService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := strings.TrimSpace(r.PathValue("ws"))
		if wsID == "" {
			handler.RespondError(w, http.StatusBadRequest, "workspace ID is required")
			return
		}
		data, err := svc.GetWorkspace(r.Context(), wsID)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, WorkspaceResponse{Success: true, Data: data})
	}
}

// HandleListWorkspaceRepos returns GET /api/workspaces/{ws}/repos — the
// workspace's repo list as a lightweight endpoint for clients that do not need
// the full WorkspaceData payload.
func HandleListWorkspaceRepos(svc service.WorkspaceService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := strings.TrimSpace(r.PathValue("ws"))
		if wsID == "" {
			handler.RespondError(w, http.StatusBadRequest, "workspace ID is required")
			return
		}
		data, err := svc.GetWorkspace(r.Context(), wsID)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
			"repos":   data.Repos,
		})
	}
}

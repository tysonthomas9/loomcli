package workspace

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/workspacecoord"
)

// HandleGetWorkspaceJob returns a handler that polls the status of an async
// workspace mutation job.
func HandleGetWorkspaceJob(svc workspacecoord.WorkspaceService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id == "" {
			handler.WriteJSON(w, http.StatusBadRequest, map[string]any{
				"success": false,
				"error":   "job id is required",
			})
			return
		}

		job, err := svc.GetWorkspaceJob(r.Context(), id)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}

		handler.WriteJSON(w, http.StatusOK, map[string]any{
			"success":      true,
			"status":       job.Status,
			"progress":     job.Progress,
			"workspace_id": job.WorkspaceID,
			"error":        job.Error,
		})
	}
}

package webui

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// handleGetWorkspaceJob returns a handler that polls the status of an async
// workspace creation job.
func handleGetWorkspaceJob(svc service.WorkspaceService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id == "" {
			respondJSON(w, http.StatusBadRequest, map[string]any{
				"success": false,
				"error":   "job id is required",
			})
			return
		}

		job, err := svc.GetWorkspaceJob(r.Context(), id)
		if err != nil {
			WriteServiceError(w, err)
			return
		}

		respondJSON(w, http.StatusOK, map[string]any{
			"success":      true,
			"status":       job.Status,
			"progress":     job.Progress,
			"workspace_id": job.WorkspaceID,
			"error":        job.Error,
		})
	}
}

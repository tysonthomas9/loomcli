package webui

import "net/http"

// handleGetWorkspaceJob returns a handler that polls the status of an async
// workspace creation job. Returns 404 if the job is not found or has expired.
func handleGetWorkspaceJob(jobStore *WorkspaceJobStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id == "" {
			respondJSON(w, http.StatusBadRequest, map[string]any{
				"success": false,
				"error":   "job id is required",
			})
			return
		}

		if jobStore == nil {
			respondJSON(w, http.StatusNotFound, map[string]any{
				"success": false,
				"error":   "job not found",
			})
			return
		}

		job := jobStore.Get(id)
		if job == nil {
			respondJSON(w, http.StatusNotFound, map[string]any{
				"success": false,
				"error":   "job not found",
			})
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

package webui

import (
	"fmt"
	"net/http"
)

// handleWorkspaceDelete returns a handler for DELETE /api/workspace/{name}.
// It removes a workspace from the global config (keeps worktrees on disk).
// After deletion, returns refreshed workspace data via workspaceConfigFn.
func handleWorkspaceDelete(deleteFn func(name string) error, workspaceConfigFn func() (*WorkspaceData, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if name == "" {
			respondJSON(w, http.StatusBadRequest, workspaceResponse{Success: false, Error: "workspace name is required"})
			return
		}

		if deleteFn == nil {
			respondJSON(w, http.StatusNotImplemented, workspaceResponse{Success: false, Error: "workspace deletion not available"})
			return
		}

		if err := deleteFn(name); err != nil {
			// Map known error patterns to HTTP status codes
			status := http.StatusInternalServerError
			switch err.Error() {
			case fmt.Sprintf("workspace %q not found", name):
				status = http.StatusNotFound
			case fmt.Sprintf("workspace %q has running agents", name):
				status = http.StatusConflict
			}
			respondJSON(w, status, workspaceResponse{Success: false, Error: err.Error()})
			return
		}

		// Return refreshed workspace data
		if workspaceConfigFn != nil {
			data, err := workspaceConfigFn()
			if err == nil && data != nil {
				normalizeWorkspaceData(data)
			}
			respondJSON(w, http.StatusOK, workspaceResponse{Success: true, Data: data})
			return
		}

		respondJSON(w, http.StatusOK, workspaceResponse{Success: true})
	}
}

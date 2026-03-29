package webui

import (
	"fmt"
	"net/http"
)

// handleWorkspaceDelete returns a handler for DELETE /api/workspaces/{ws}.
// The workspace is identified by UUID from WorkspaceMiddleware context.
// It resolves the UUID to a workspace name via workspaceConfigFn, then
// calls deleteFn with the name. The deleteFn is expected to be wrapped by
// wrapWorkspaceDeleteFn, which handles post-deletion cleanup (pool, subscriber,
// and fleet deregistration) transparently. Returns refreshed workspace data.
func handleWorkspaceDelete(
	deleteFn func(name string) error,
	workspaceConfigFn func() (*WorkspaceData, error),
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := WorkspaceFromContext(r.Context())
		if wsID == "" {
			respondJSON(w, http.StatusBadRequest, workspaceResponse{Success: false, Error: "workspace ID is required"})
			return
		}

		if deleteFn == nil {
			respondJSON(w, http.StatusNotImplemented, workspaceResponse{Success: false, Error: "workspace deletion not available"})
			return
		}

		// Resolve UUID to workspace name via workspaceConfigFn
		name, err := resolveWorkspaceNameByUUID(workspaceConfigFn, wsID)
		if err != nil {
			respondJSON(w, http.StatusInternalServerError, workspaceResponse{Success: false, Error: "failed to resolve workspace"})
			return
		}
		if name == "" {
			respondJSON(w, http.StatusNotFound, workspaceResponse{Success: false, Error: fmt.Sprintf("workspace with ID %q not found", wsID)})
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

// resolveWorkspaceNameByUUID looks up a workspace name from the WorkspaceData by UUID.
func resolveWorkspaceNameByUUID(configFn func() (*WorkspaceData, error), wsID string) (string, error) {
	if configFn == nil {
		return "", nil
	}
	data, err := configFn()
	if err != nil {
		return "", err
	}
	if data == nil {
		return "", nil
	}
	for _, ws := range data.Workspaces {
		if ws.ID == wsID {
			return ws.Name, nil
		}
	}
	return "", nil
}

package webui

import (
	"fmt"
	"log/slog"
	"net/http"
)

// handleWorkspaceDelete returns a handler for DELETE /api/workspace/{name}.
// It removes a workspace from the global config (keeps worktrees on disk).
// After successful deletion, deregisters the workspace's pool and subscriber
// via the registry. Returns refreshed workspace data via workspaceConfigFn.
func handleWorkspaceDelete(
	deleteFn func(name string) error,
	workspaceConfigFn func() (*WorkspaceData, error),
	registry *WorkspaceRegistry,
	resolveID WorkspaceIDResolverFn,
) http.HandlerFunc {
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

		// Resolve UUID before deletion (config still has the workspace entry).
		var wsID string
		if resolveID != nil {
			if id, err := resolveID(name); err != nil {
				slog.Warn("failed to resolve workspace UUID for deletion",
					"workspace", name, "err", err)
			} else {
				wsID = id
			}
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

		// Deregister pool and subscriber after successful config deletion.
		if registry != nil {
			if wsID != "" {
				registry.Deregister(wsID)
			} else {
				// Best-effort: try deregistering by name in case the pool was
				// registered by name (pre-UUID migration).
				registry.Deregister(name)
			}
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

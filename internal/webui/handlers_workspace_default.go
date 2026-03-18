package webui

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// handleSetDefaultWorkspace handles PUT /api/workspace/default.
// Sets the default workspace in the config file.
func handleSetDefaultWorkspace(setFn func(string) error, workspaceConfigFn func() (*WorkspaceData, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if setFn == nil {
			respondJSON(w, http.StatusNotImplemented, workspaceResponse{Success: false, Error: "set default workspace not available"})
			return
		}

		var body struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			respondJSON(w, http.StatusBadRequest, workspaceResponse{Success: false, Error: "invalid request body"})
			return
		}
		if body.Name == "" {
			respondJSON(w, http.StatusBadRequest, workspaceResponse{Success: false, Error: "name is required"})
			return
		}

		if err := setFn(body.Name); err != nil {
			status := http.StatusInternalServerError
			switch err.Error() {
			case fmt.Sprintf("workspace %q not found", body.Name):
				status = http.StatusNotFound
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

// handleClearDefaultWorkspace handles DELETE /api/workspace/default.
// Clears the default workspace, reverting to first-workspace behavior.
func handleClearDefaultWorkspace(clearFn func() error, workspaceConfigFn func() (*WorkspaceData, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if clearFn == nil {
			respondJSON(w, http.StatusNotImplemented, workspaceResponse{Success: false, Error: "clear default workspace not available"})
			return
		}

		if err := clearFn(); err != nil {
			respondJSON(w, http.StatusInternalServerError, workspaceResponse{Success: false, Error: err.Error()})
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

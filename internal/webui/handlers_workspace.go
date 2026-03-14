package webui

import "net/http"

type workspaceResponse struct {
	Success bool           `json:"success"`
	Data    *WorkspaceData `json:"data,omitempty"`
	Error   string         `json:"error,omitempty"`
}

// handleWorkspace returns workspace topology (repos with names and paths).
// If configFn is nil, returns an empty workspace (single-repo mode).
func handleWorkspace(configFn func() (*WorkspaceData, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if configFn == nil {
			respondJSON(w, http.StatusOK, workspaceResponse{
				Success: true,
				Data:    &WorkspaceData{Repos: []WorkspaceRepo{}},
			})
			return
		}

		data, err := configFn()
		if err != nil {
			respondJSON(w, http.StatusInternalServerError, workspaceResponse{
				Success: false,
				Error:   "failed to load workspace config",
			})
			return
		}

		if data == nil {
			data = &WorkspaceData{}
		}
		if data.Repos == nil {
			data.Repos = []WorkspaceRepo{}
		}

		respondJSON(w, http.StatusOK, workspaceResponse{
			Success: true,
			Data:    data,
		})
	}
}

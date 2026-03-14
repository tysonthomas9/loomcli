package webui

import "net/http"

type workspaceResponse struct {
	Repos []WorkspaceRepo `json:"repos"`
}

// handleWorkspace returns workspace topology (repos with names and paths).
// If configFn is nil, returns an empty repos array (single-repo mode).
func handleWorkspace(configFn func() ([]WorkspaceRepo, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if configFn == nil {
			respondJSON(w, http.StatusOK, workspaceResponse{Repos: []WorkspaceRepo{}})
			return
		}

		repos, err := configFn()
		if err != nil {
			respondError(w, http.StatusInternalServerError, "failed to load workspace config")
			return
		}

		if repos == nil {
			repos = []WorkspaceRepo{}
		}

		respondJSON(w, http.StatusOK, workspaceResponse{Repos: repos})
	}
}

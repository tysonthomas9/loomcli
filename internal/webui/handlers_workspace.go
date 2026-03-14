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
				Data:    emptyWorkspaceData(),
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
		normalizeWorkspaceData(data)

		respondJSON(w, http.StatusOK, workspaceResponse{
			Success: true,
			Data:    data,
		})
	}
}

// emptyWorkspaceData returns a WorkspaceData with all slices initialized to empty (not nil).
func emptyWorkspaceData() *WorkspaceData {
	return &WorkspaceData{
		Repos:  []WorkspaceRepo{},
		Groups: []string{},
		Agents: []WorkspaceAgentInfo{},
	}
}

// normalizeWorkspaceData ensures all slice fields are non-nil so JSON marshals as [] not null.
func normalizeWorkspaceData(data *WorkspaceData) {
	if data.Repos == nil {
		data.Repos = []WorkspaceRepo{}
	}
	if data.Groups == nil {
		data.Groups = []string{}
	}
	if data.Agents == nil {
		data.Agents = []WorkspaceAgentInfo{}
	}
	for i := range data.Repos {
		if data.Repos[i].Groups == nil {
			data.Repos[i].Groups = []string{}
		}
	}
	for i := range data.Agents {
		if data.Agents[i].Repos == nil {
			data.Agents[i].Repos = []string{}
		}
		if data.Agents[i].RepoGroups == nil {
			data.Agents[i].RepoGroups = []string{}
		}
	}
}

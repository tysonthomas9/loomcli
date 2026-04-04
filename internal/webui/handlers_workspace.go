package webui

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

type workspaceResponse struct {
	Success  bool                   `json:"success"`
	Data     *service.WorkspaceData `json:"data,omitempty"`
	Error    string                 `json:"error,omitempty"`
	Warnings []string               `json:"warnings,omitempty"`
}

// handleActiveWorkspace returns the active workspace topology.
func handleActiveWorkspace(svc service.WorkspaceService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := svc.GetActiveWorkspace(r.Context())
		if err != nil {
			WriteServiceError(w, err)
			return
		}
		respondJSON(w, http.StatusOK, workspaceResponse{Success: true, Data: data})
	}
}

// normalizeWorkspaceData ensures all slice fields are non-nil so JSON marshals as [] not null.
// Kept here as a convenience for non-service callers (server_workspace.go etc.).
func normalizeWorkspaceData(data *service.WorkspaceData) {
	if data.Repos == nil {
		data.Repos = []service.WorkspaceRepo{}
	}
	if data.Groups == nil {
		data.Groups = []string{}
	}
	if data.Agents == nil {
		data.Agents = []service.WorkspaceAgentInfo{}
	}
	if data.Workspaces == nil {
		data.Workspaces = []service.WorkspaceSummary{}
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

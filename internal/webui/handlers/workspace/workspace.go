package workspace

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/app/query/operationalview"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/workspacecoord"
)

// WorkspaceResponse wraps workspace data for JSON response.
type WorkspaceResponse struct {
	Success  bool                       `json:"success"`
	Data     *operationalview.Workspace `json:"data,omitempty"`
	Error    string                     `json:"error,omitempty"`
	Warnings []string                   `json:"warnings,omitempty"`
}

// HandleActiveWorkspace returns the active workspace topology.
func HandleActiveWorkspace(svc workspacecoord.WorkspaceService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := svc.GetActiveWorkspace(r.Context())
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, WorkspaceResponse{Success: true, Data: data})
	}
}

// NormalizeWorkspaceData ensures all slice fields are non-nil so JSON marshals as [] not null.
// Kept here as a convenience for non-service callers (server_workspace.go etc.).
func NormalizeWorkspaceData(data *operationalview.Workspace) {
	if data.Repos == nil {
		data.Repos = []operationalview.Repository{}
	}
	if data.Groups == nil {
		data.Groups = []string{}
	}
	if data.Agents == nil {
		data.Agents = []operationalview.Agent{}
	}
	if data.Workspaces == nil {
		data.Workspaces = []operationalview.Summary{}
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

package workspace

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// AddRepoRequest is the JSON body for POST /api/workspaces/{ws}/repos.
// Name is optional — when blank, the service derives it from the path's basename.
// SourceRepoID is not exposed: the service always sets it equal to Name. Repos
// added through this endpoint cannot reference a different source repo identity.
type AddRepoRequest struct {
	Name          string   `json:"name,omitempty"`
	Path          string   `json:"path"`
	DefaultBranch string   `json:"default_branch,omitempty"`
	Remote        string   `json:"remote,omitempty"`
	Groups        []string `json:"groups,omitempty"`
}

// HandleAddRepo returns a handler that appends a new repo entry to a workspace.
func HandleAddRepo(svc service.WorkspaceService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		if wsID == "" {
			handler.WriteJSON(w, http.StatusBadRequest, WorkspaceResponse{Success: false, Error: "workspace ID is required"})
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, handler.MaxRequestBody)

		var req AddRepoRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				handler.WriteJSON(w, http.StatusRequestEntityTooLarge, WorkspaceResponse{Success: false, Error: "request body too large"})
				return
			}
			handler.WriteJSON(w, http.StatusBadRequest, WorkspaceResponse{Success: false, Error: "invalid request body"})
			return
		}

		if req.Path == "" {
			handler.WriteJSON(w, http.StatusBadRequest, WorkspaceResponse{Success: false, Error: "repo path is required"})
			return
		}

		data, err := svc.AddWorkspaceRepo(r.Context(), wsID, service.AddRepoParams{
			Name:          req.Name,
			Path:          req.Path,
			DefaultBranch: req.DefaultBranch,
			Remote:        req.Remote,
			Groups:        req.Groups,
		})
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusCreated, WorkspaceResponse{Success: true, Data: data})
	}
}

// HandleRemoveRepo returns a handler that removes a repo entry from a workspace.
// The repo is identified solely by its name (path param), so no body is needed.
// Files on disk and git worktrees are NOT touched — this only updates config.
func HandleRemoveRepo(svc service.WorkspaceService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		if wsID == "" {
			handler.WriteJSON(w, http.StatusBadRequest, WorkspaceResponse{Success: false, Error: "workspace ID is required"})
			return
		}

		repoName := r.PathValue("repo")
		if repoName == "" {
			handler.WriteJSON(w, http.StatusBadRequest, WorkspaceResponse{Success: false, Error: "repo name is required"})
			return
		}

		data, err := svc.RemoveWorkspaceRepo(r.Context(), wsID, repoName)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, WorkspaceResponse{Success: true, Data: data})
	}
}

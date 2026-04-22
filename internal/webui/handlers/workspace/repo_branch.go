package workspace

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// validGitRef matches safe git ref names: alphanumeric, hyphens, underscores, dots, slashes.
// Must start with an alphanumeric; mirrors the pattern used across handlers/git.
var validGitRef = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_./-]*$`)

// isValidBranchName applies the shape check plus git's own rules: no ".." or
// "//" components, no trailing "/" or ".", and no ".lock" suffix. Branch names
// that pass here still round-trip through `git checkout` safely.
func isValidBranchName(b string) bool {
	if !validGitRef.MatchString(b) {
		return false
	}
	if strings.Contains(b, "..") || strings.Contains(b, "//") {
		return false
	}
	if strings.HasSuffix(b, "/") || strings.HasSuffix(b, ".") || strings.HasSuffix(b, ".lock") {
		return false
	}
	return true
}

// RepoDefaultBranchPatchRequest is the JSON body for
// PATCH /api/workspaces/{ws}/repos/{repo}/default-branch.
type RepoDefaultBranchPatchRequest struct {
	Branch string `json:"branch"`
}

// HandleRepoDefaultBranchPatch returns a handler that updates the default_branch
// of a repo within a workspace in the global config.
func HandleRepoDefaultBranchPatch(svc service.WorkspaceService) http.HandlerFunc {
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

		r.Body = http.MaxBytesReader(w, r.Body, handler.MaxRequestBody)

		var req RepoDefaultBranchPatchRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				handler.WriteJSON(w, http.StatusRequestEntityTooLarge, WorkspaceResponse{Success: false, Error: "request body too large"})
				return
			}
			handler.WriteJSON(w, http.StatusBadRequest, WorkspaceResponse{Success: false, Error: "invalid request body"})
			return
		}

		if req.Branch == "" {
			handler.WriteJSON(w, http.StatusBadRequest, WorkspaceResponse{Success: false, Error: "branch is required"})
			return
		}

		if !isValidBranchName(req.Branch) {
			handler.WriteJSON(w, http.StatusBadRequest, WorkspaceResponse{Success: false, Error: "invalid branch name"})
			return
		}

		data, err := svc.PatchRepoDefaultBranch(r.Context(), wsID, repoName, req.Branch)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, WorkspaceResponse{Success: true, Data: data})
	}
}

package webui

import (
	"net/http"
	"path"
	"strings"
)

// diffResponse is the standard response envelope for diff endpoints.
// Matches the frontend's ApiResult<T> pattern: {success: true, data: T} or {success: false, error: "msg"}.
type diffResponse struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}

func respondDiffOK(w http.ResponseWriter, data interface{}) {
	respondJSON(w, http.StatusOK, diffResponse{Success: true, Data: data})
}

func respondDiffError(w http.ResponseWriter, status int, msg string) {
	respondJSON(w, status, diffResponse{Success: false, Error: msg})
}

// resolveMergeBaseDefault extracts the "from" query param or computes merge-base as default.
// Returns (fromRef, ok). On failure it writes the error response.
func resolveMergeBaseDefault(w http.ResponseWriter, r *http.Request, ops GitOps, wt *AgentWorktree) (string, bool) {
	from := r.URL.Query().Get("from")
	if from != "" {
		if !validGitRef.MatchString(from) || strings.Contains(from, "..") {
			respondDiffError(w, http.StatusBadRequest, "invalid from ref")
			return "", false
		}
		return from, true
	}
	mergeBase, err := ops.ResolveMergeBase(wt.Path, wt.DefaultBranch)
	if err != nil {
		respondDiffError(w, http.StatusInternalServerError, "failed to resolve merge-base: "+err.Error())
		return "", false
	}
	return mergeBase, true
}

// validateDiffPath checks that a file path is safe (no traversal, not absolute, not empty).
func validateDiffPath(p string) bool {
	if p == "" {
		return false
	}
	if strings.HasPrefix(p, "/") {
		return false
	}
	cleaned := path.Clean(p)
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return false
	}
	return true
}

// handleDiffCommits handles GET /api/agents/{name}/diff/commits?limit=N
func handleDiffCommits(ops GitOps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wt, ok := resolveAgent(w, r, ops)
		if !ok {
			return
		}

		limitPtr, err := parseIntParam(r.URL.Query(), "limit")
		if err != nil {
			respondDiffError(w, http.StatusBadRequest, err.Error())
			return
		}
		limit := 0
		if limitPtr != nil {
			limit = *limitPtr
		}

		mergeBase, ok := resolveMergeBaseDefault(w, r, ops, wt)
		if !ok {
			return
		}

		commits, err := ops.DiffCommits(wt.Path, mergeBase, limit)
		if err != nil {
			respondDiffError(w, http.StatusInternalServerError, "failed to get diff commits: "+err.Error())
			return
		}
		if commits == nil {
			commits = []DiffCommitResult{}
		}

		respondDiffOK(w, map[string]interface{}{"commits": commits})
	}
}

// handleDiffFiles handles GET /api/agents/{name}/diff/files?to=HEAD&from=X
func handleDiffFiles(ops GitOps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wt, ok := resolveAgent(w, r, ops)
		if !ok {
			return
		}

		to := r.URL.Query().Get("to")
		if to == "" {
			respondDiffError(w, http.StatusBadRequest, "missing required query parameter: to")
			return
		}
		if !validGitRef.MatchString(to) || strings.Contains(to, "..") {
			respondDiffError(w, http.StatusBadRequest, "invalid to ref")
			return
		}

		from, ok := resolveMergeBaseDefault(w, r, ops, wt)
		if !ok {
			return
		}

		files, err := ops.DiffFiles(wt.Path, from, to)
		if err != nil {
			respondDiffError(w, http.StatusInternalServerError, "failed to get diff files: "+err.Error())
			return
		}
		if files == nil {
			files = []DiffFileResult{}
		}

		respondDiffOK(w, map[string]interface{}{"files": files})
	}
}

// handleDiffFile handles GET /api/agents/{name}/diff/file?path=X&to=HEAD&from=Y
func handleDiffFile(ops GitOps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wt, ok := resolveAgent(w, r, ops)
		if !ok {
			return
		}

		q := r.URL.Query()
		path := q.Get("path")
		if path == "" {
			respondDiffError(w, http.StatusBadRequest, "missing required query parameter: path")
			return
		}
		if !validateDiffPath(path) {
			respondDiffError(w, http.StatusBadRequest, "invalid path: must be relative with no '..' traversal")
			return
		}

		to := q.Get("to")
		if to == "" {
			respondDiffError(w, http.StatusBadRequest, "missing required query parameter: to")
			return
		}
		if !validGitRef.MatchString(to) || strings.Contains(to, "..") {
			respondDiffError(w, http.StatusBadRequest, "invalid to ref")
			return
		}

		from, ok := resolveMergeBaseDefault(w, r, ops, wt)
		if !ok {
			return
		}

		result, err := ops.DiffFilePatch(wt.Path, from, to, path)
		if err != nil {
			respondDiffError(w, http.StatusInternalServerError, "failed to get diff patch: "+err.Error())
			return
		}

		respondDiffOK(w, result)
	}
}

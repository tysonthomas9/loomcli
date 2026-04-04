package webui

import (
	"net/http"
	"path/filepath"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// diffResponse is the standard response envelope for diff endpoints.
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

// validateDiffPath checks that a file path is safe (no traversal, not absolute, not empty).
func validateDiffPath(p string) bool {
	if p == "" {
		return false
	}
	if strings.HasPrefix(p, "/") {
		return false
	}
	cleaned := filepath.Clean(p)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return false
	}
	return true
}

// handleDiffCommits handles GET /api/agents/{name}/diff/commits?limit=N
func handleDiffCommits(svc DiffService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agentName := r.PathValue("name")
		wsID := middleware.WorkspaceFromContext(r.Context())

		limitPtr, err := parseIntParam(r.URL.Query(), "limit")
		if err != nil {
			respondDiffError(w, http.StatusBadRequest, err.Error())
			return
		}
		limit := 0
		if limitPtr != nil {
			limit = *limitPtr
		}

		from := r.URL.Query().Get("from")

		commits, svcErr := svc.DiffCommits(r.Context(), wsID, agentName, from, limit)
		if svcErr != nil {
			WriteServiceError(w, svcErr)
			return
		}

		respondDiffOK(w, map[string]interface{}{"commits": commits})
	}
}

// handleDiffFiles handles GET /api/agents/{name}/diff/files?to=HEAD&from=X
func handleDiffFiles(svc DiffService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agentName := r.PathValue("name")
		wsID := middleware.WorkspaceFromContext(r.Context())

		from := r.URL.Query().Get("from")
		to := r.URL.Query().Get("to")

		files, err := svc.DiffFiles(r.Context(), wsID, agentName, from, to)
		if err != nil {
			WriteServiceError(w, err)
			return
		}

		respondDiffOK(w, map[string]interface{}{"files": files})
	}
}

// handleDiffFile handles GET /api/agents/{name}/diff/file?path=X&to=HEAD&from=Y
func handleDiffFile(svc DiffService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agentName := r.PathValue("name")
		wsID := middleware.WorkspaceFromContext(r.Context())

		q := r.URL.Query()
		filePath := q.Get("path")
		from := q.Get("from")
		to := q.Get("to")

		result, err := svc.DiffFilePatch(r.Context(), wsID, agentName, from, to, filePath)
		if err != nil {
			WriteServiceError(w, err)
			return
		}

		respondDiffOK(w, result)
	}
}

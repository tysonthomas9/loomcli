package git

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// diffResponse is the standard response envelope for diff endpoints.
type diffResponse struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}

func respondDiffOK(w http.ResponseWriter, data interface{}) {
	handler.WriteJSON(w, http.StatusOK, diffResponse{Success: true, Data: data})
}

func respondDiffError(w http.ResponseWriter, status int, msg string) {
	handler.WriteJSON(w, status, diffResponse{Success: false, Error: msg})
}

// HandleDiffCommits handles GET /api/agents/{name}/diff/commits?limit=N
func HandleDiffCommits(svc service.DiffService) http.HandlerFunc {
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
			handler.HandleServiceError(w, svcErr)
			return
		}

		respondDiffOK(w, map[string]interface{}{"commits": commits})
	}
}

// HandleDiffFiles handles GET /api/agents/{name}/diff/files?to=HEAD&from=X
func HandleDiffFiles(svc service.DiffService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agentName := r.PathValue("name")
		wsID := middleware.WorkspaceFromContext(r.Context())

		from := r.URL.Query().Get("from")
		to := r.URL.Query().Get("to")

		files, err := svc.DiffFiles(r.Context(), wsID, agentName, from, to)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}

		respondDiffOK(w, map[string]interface{}{"files": files})
	}
}

// HandleDiffFile handles GET /api/agents/{name}/diff/file?path=X&to=HEAD&from=Y
func HandleDiffFile(svc service.DiffService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agentName := r.PathValue("name")
		wsID := middleware.WorkspaceFromContext(r.Context())

		q := r.URL.Query()
		filePath := q.Get("path")
		from := q.Get("from")
		to := q.Get("to")

		result, err := svc.DiffFilePatch(r.Context(), wsID, agentName, from, to, filePath)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}

		respondDiffOK(w, result)
	}
}

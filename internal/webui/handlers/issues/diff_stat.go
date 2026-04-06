package issues

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// DiffStatResponse is the JSON shape for GET /api/issues/{id}/git/diff-stat.
type DiffStatResponse struct {
	Branch  string `json:"branch"`
	Added   int    `json:"added"`
	Removed int    `json:"removed"`
}

// handleGetIssueDiffStat returns diff statistics for an issue's assigned agent worktree.
func handleGetIssueDiffStat(svc service.DiffService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		issueID := r.PathValue("id")
		wsID := middleware.WorkspaceFromContext(r.Context())

		result, err := svc.GetIssueDiffStat(r.Context(), wsID, issueID)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}

		handler.WriteJSON(w, http.StatusOK, DiffStatResponse{
			Branch:  result.Branch,
			Added:   result.Added,
			Removed: result.Removed,
		})
	}
}

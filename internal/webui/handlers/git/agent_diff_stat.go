package git

import (
	"errors"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
	loomapi "github.com/tysonthomas9/loomcli/internal/platform/loomapi/gen"
	"github.com/tysonthomas9/loomcli/internal/webui/readprojection"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// HandleAgentDiffStat returns diff statistics (added/removed lines, branch)
// for an agent's worktree, resolved directly by agent name.
func HandleAgentDiffStat(browse DiffBrowse) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agentName := r.PathValue("name")
		wsID := middleware.WorkspaceFromContext(r.Context())

		result, err := browse.DiffStat(r.Context(), sourcecontrol.AgentQuery{WorkspaceKey: wsID, AgentID: agentName})
		if err != nil {
			handler.HandleSourceControlError(w, err)
			return
		}

		handler.WriteJSON(w, http.StatusOK, loomapi.DiffStatResponse{
			Branch:  result.Branch,
			Added:   result.LinesAdded,
			Removed: result.LinesRemoved,
		})
	}
}

// HandleGetIssueDiffStat returns diff statistics for an issue's assigned agent worktree.
func HandleGetIssueDiffStat(projection readprojection.IssueDiffProjection) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		issueID := r.PathValue("id")
		wsID := middleware.WorkspaceFromContext(r.Context())

		result, err := projection.GetIssueDiff(r.Context(), readprojection.IssueDiffQuery{
			WorkspaceKey: wsID,
			IssueID:      issueID,
		})
		if err != nil {
			status := http.StatusInternalServerError
			switch {
			case errors.Is(err, readprojection.ErrInvalidIssueDiffQuery):
				status = http.StatusBadRequest
			case errors.Is(err, readprojection.ErrIssueDiffNotFound):
				status = http.StatusNotFound
			case errors.Is(err, readprojection.ErrIssueDiffUnavailable):
				status = http.StatusServiceUnavailable
			}
			handler.RespondError(w, status, readprojection.IssueDiffPublicErrorMessage(err))
			return
		}

		handler.WriteJSON(w, http.StatusOK, loomapi.DiffStatResponse{
			Branch:  result.Branch,
			Added:   result.Added,
			Removed: result.Removed,
		})
	}
}

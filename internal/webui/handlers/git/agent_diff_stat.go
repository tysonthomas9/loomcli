package git

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/agentcoord"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/sourcecontrolcoord"
)

// DiffStatResponse is the JSON shape for agent diff-stat endpoints.
type DiffStatResponse struct {
	Branch  string `json:"branch"`
	Added   int    `json:"added"`
	Removed int    `json:"removed"`
}

// HandleAgentDiffStat returns diff statistics (added/removed lines, branch)
// for an agent's worktree, resolved directly by agent name.
func HandleAgentDiffStat(svc agentcoord.AgentService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agentName := r.PathValue("name")
		wsID := middleware.WorkspaceFromContext(r.Context())

		result, err := svc.GetDiffStat(r.Context(), wsID, agentName)
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

// HandleGetIssueDiffStat returns diff statistics for an issue's assigned agent worktree.
func HandleGetIssueDiffStat(svc sourcecontrolcoord.DiffService) http.HandlerFunc {
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

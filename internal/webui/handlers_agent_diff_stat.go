package webui

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// handleAgentDiffStat returns diff statistics (added/removed lines, branch)
// for an agent's worktree, resolved directly by agent name.
func handleAgentDiffStat(svc AgentService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agentName := r.PathValue("name")
		wsID := middleware.WorkspaceFromContext(r.Context())

		result, err := svc.GetDiffStat(r.Context(), wsID, agentName)
		if err != nil {
			WriteServiceError(w, err)
			return
		}

		respondJSON(w, http.StatusOK, DiffStatResponse{
			Branch:  result.Branch,
			Added:   result.Added,
			Removed: result.Removed,
		})
	}
}

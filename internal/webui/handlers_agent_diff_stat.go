package webui

import "net/http"

// handleAgentDiffStat returns diff statistics (added/removed lines, branch)
// for an agent's worktree, resolved directly by agent name.
func handleAgentDiffStat(gitOps GitOps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wt, ok := resolveAgent(w, r, gitOps)
		if !ok {
			return
		}

		stats := gitOps.DiffStat(wt.Path, wt.DefaultBranch)

		respondJSON(w, http.StatusOK, DiffStatResponse{
			Branch:  wt.Branch,
			Added:   stats.LinesAdded,
			Removed: stats.LinesRemoved,
		})
	}
}

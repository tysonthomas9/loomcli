package webui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
)

// DiffStatResponse is the JSON shape for GET /api/issues/{id}/git/diff-stat.
type DiffStatResponse struct {
	Branch  string `json:"branch"`
	Added   int    `json:"added"`
	Removed int    `json:"removed"`
}

// handleGetIssueDiffStat returns diff statistics (added/removed lines, branch)
// for an issue's assigned agent worktree.
func handleGetIssueDiffStat(pool daemon.Pool, gitOps GitOps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		issueID := r.PathValue("id")
		if issueID == "" {
			respondError(w, http.StatusBadRequest, "missing issue ID")
			return
		}

		// Look up the issue via daemon RPC to get the assignee.
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		client, err := pool.Get(ctx)
		if err != nil {
			respondError(w, http.StatusServiceUnavailable, "daemon not available")
			return
		}
		defer pool.Put(client)

		resp, err := client.Show(&rpc.ShowArgs{ID: issueID})
		if err != nil {
			if strings.Contains(err.Error(), "not found") {
				respondError(w, http.StatusNotFound, fmt.Sprintf("issue not found: %s", issueID))
				return
			}
			respondError(w, http.StatusInternalServerError, "internal server error")
			return
		}

		// Extract assignee from the issue data.
		var issue struct {
			Assignee string `json:"assignee"`
		}
		if err := json.Unmarshal(resp.Data, &issue); err != nil {
			respondError(w, http.StatusInternalServerError, "failed to parse issue data")
			return
		}
		if issue.Assignee == "" {
			respondError(w, http.StatusNotFound, "issue has no assignee (no agent worktree)")
			return
		}

		// Resolve assignee to worktree.
		wt, err := gitOps.ResolveAgentWorktree(issue.Assignee)
		if err != nil {
			respondError(w, http.StatusNotFound, fmt.Sprintf("agent worktree not found for %s", issue.Assignee))
			return
		}

		// Compute diff stats against the default branch.
		stats := gitOps.DiffStat(wt.Path, wt.DefaultBranch)

		respondJSON(w, http.StatusOK, DiffStatResponse{
			Branch:  wt.Branch,
			Added:   stats.LinesAdded,
			Removed: stats.LinesRemoved,
		})
	}
}

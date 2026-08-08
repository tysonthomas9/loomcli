package git

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/webui/agentcoord"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

type pullRequestsData struct {
	PullRequests []ops.GitPullRequest `json:"pull_requests"`
	Warnings     []string             `json:"warnings,omitempty"`
}

type pullRequestsResponse struct {
	Success bool             `json:"success"`
	Data    pullRequestsData `json:"data"`
	Error   string           `json:"error,omitempty"`
}

// HandleListPullRequests handles GET /api/workspaces/{ws}/pull-requests?state=all|open|merged|review
func HandleListPullRequests(svc agentcoord.AgentService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		state := r.URL.Query().Get("state")
		if state == "" {
			state = "all"
		}

		result, err := svc.ListPullRequests(r.Context(), wsID, state)
		if err != nil {
			writeAgentGitError(w, err, http.StatusBadGateway)
			return
		}
		prs := []ops.GitPullRequest{}
		var warnings []string
		if result != nil {
			if result.PullRequests != nil {
				prs = result.PullRequests
			}
			warnings = result.Warnings
		}

		handler.WriteJSON(w, http.StatusOK, pullRequestsResponse{
			Success: true,
			Data:    pullRequestsData{PullRequests: prs, Warnings: warnings},
		})
	}
}

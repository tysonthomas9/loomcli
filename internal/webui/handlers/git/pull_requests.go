package git

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

type pullRequestsResponse struct {
	Success bool `json:"success"`
	Data    struct {
		PullRequests []ops.GitPullRequest `json:"pull_requests"`
	} `json:"data"`
	Error string `json:"error,omitempty"`
}

// HandleListPullRequests handles GET /api/workspaces/{ws}/pull-requests?state=all|open|merged|review
func HandleListPullRequests(svc service.AgentService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		state := r.URL.Query().Get("state")
		if state == "" {
			state = "all"
		}

		prs, err := svc.ListPullRequests(r.Context(), wsID, state)
		if err != nil {
			writeAgentGitError(w, err, http.StatusBadGateway)
			return
		}
		if prs == nil {
			prs = []ops.GitPullRequest{}
		}

		handler.WriteJSON(w, http.StatusOK, pullRequestsResponse{
			Success: true,
			Data: struct {
				PullRequests []ops.GitPullRequest `json:"pull_requests"`
			}{PullRequests: prs},
		})
	}
}

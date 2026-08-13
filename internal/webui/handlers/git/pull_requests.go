package git

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
	loomapi "github.com/tysonthomas9/loomcli/internal/platform/loomapi/gen"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// HandleListPullRequests handles GET /api/workspaces/{ws}/pull-requests?state=all|open|merged|review.
func HandleListPullRequests(svc sourcecontrol.Checkout) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		state := r.URL.Query().Get("state")
		if state == "" {
			state = "all"
		}

		result, err := svc.ListPullRequests(r.Context(), sourcecontrol.ListPullRequestsQuery{WorkspaceKey: wsID, State: state})
		if err != nil {
			writeSourceControlError(w, err)
			return
		}
		data := loomapi.PullRequestsData{PullRequests: []loomapi.PullRequestSummary{}}
		if result != nil {
			data.PullRequests = make([]loomapi.PullRequestSummary, len(result.PullRequests))
			for index, pull := range result.PullRequests {
				data.PullRequests[index] = pullRequestSummaryFromSourceControl(pull)
			}
			if len(result.Warnings) > 0 {
				warnings := append([]string(nil), result.Warnings...)
				data.Warnings = &warnings
			}
		}

		handler.WriteJSON(w, http.StatusOK, loomapi.PullRequestsResponse{Success: true, Data: data})
	}
}

func pullRequestSummaryFromSourceControl(pull sourcecontrol.PullRequest) loomapi.PullRequestSummary {
	response := loomapi.PullRequestSummary{
		Number: pull.Number, Title: pull.Title, Url: pull.URL, State: pull.State,
		IsDraft: pull.Draft, HeadRefName: pull.HeadBranch, BaseRefName: pull.BaseBranch,
		RepoName: pull.Repository,
	}
	setPullRequestSummaryOptionalFields(&response, pull)
	return response
}

func setPullRequestSummaryOptionalFields(response *loomapi.PullRequestSummary, pull sourcecontrol.PullRequest) {
	if pull.Author != "" {
		response.AuthorLogin = gitPointer(pull.Author)
	}
	if pull.CreatedAt != "" {
		response.CreatedAt = gitPointer(pull.CreatedAt)
	}
	if pull.UpdatedAt != "" {
		response.UpdatedAt = gitPointer(pull.UpdatedAt)
	}
	if pull.ReviewDecision != "" {
		response.ReviewDecision = gitPointer(pull.ReviewDecision)
	}
	if pull.SourceRepo != "" {
		response.SourceRepo = gitPointer(pull.SourceRepo)
	}
	if pull.Additions != 0 {
		response.Additions = gitPointer(pull.Additions)
	}
	if pull.Deletions != 0 {
		response.Deletions = gitPointer(pull.Deletions)
	}
	if pull.ChangedFiles != 0 {
		response.ChangedFiles = gitPointer(pull.ChangedFiles)
	}
}

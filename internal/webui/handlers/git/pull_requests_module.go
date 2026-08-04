package git

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// PullRequestListModule registers the gh-backed pull-request list route for
// serve modes without a store. Store-backed serve gets the richer
// connector-primary route from the prreview module instead; without this
// fallback, non-store deployments would lose GET /pull-requests entirely.
type PullRequestListModule struct {
	agentSvc service.AgentService
}

// NewPullRequestListModule returns the gh-backed PR list fallback module.
func NewPullRequestListModule(agentSvc service.AgentService) *PullRequestListModule {
	return &PullRequestListModule{agentSvc: agentSvc}
}

// Register adds the workspace-scoped pull-request list route.
func (m *PullRequestListModule) Register(mux *http.ServeMux) {
	if m == nil || m.agentSvc == nil {
		return
	}
	mux.HandleFunc("GET /api/workspaces/{ws}/pull-requests", HandleListPullRequests(m.agentSvc))
}

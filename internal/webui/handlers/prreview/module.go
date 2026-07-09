package prreview

import (
	"net/http"
	"sync"

	"github.com/tysonthomas9/loomcli/internal/connector"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

const (
	bindingID   = "webui-review"
	connectorID = "github-webui"

	webuiGitHubTokenEnv = "LOOM_WEBUI_GITHUB_TOKEN"
)

// Module serves connector-backed, read-only pull request review routes.
type Module struct {
	store      store.Store
	dispatcher *connector.Dispatcher
	agentSvc   service.AgentService
	// seeded caches "connector+grants already ensured" per canonical
	// ws|owner/repo so a polled read API does not re-seal + re-Create on
	// every request. Key is the canonical resource; value struct{}{}.
	seeded sync.Map
}

// NewModule constructs the pull request review route module.
func NewModule(st store.Store, disp *connector.Dispatcher, agentSvc service.AgentService) *Module {
	return &Module{store: st, dispatcher: disp, agentSvc: agentSvc}
}

// Register adds the workspace-scoped pull request review routes.
func (m *Module) Register(mux *http.ServeMux) {
	if m == nil || m.store == nil {
		return
	}
	mux.HandleFunc("GET /api/workspaces/{ws}/pull-requests", m.listPullRequests)
	mux.HandleFunc("GET /api/workspaces/{ws}/pull-requests/{owner}/{repo}/{number}", m.getPullRequest)
	mux.HandleFunc("GET /api/workspaces/{ws}/pull-requests/{owner}/{repo}/{number}/diff", m.getPullRequestDiff)
	mux.HandleFunc("POST /api/workspaces/{ws}/pull-requests/{owner}/{repo}/{number}/review", m.postReview)
	mux.HandleFunc("POST /api/workspaces/{ws}/pull-requests/{owner}/{repo}/{number}/reviewer", m.ensureReviewer)
	mux.HandleFunc("POST /api/workspaces/{ws}/pull-requests/{owner}/{repo}/{number}/messages", m.postReviewerMessage)
}

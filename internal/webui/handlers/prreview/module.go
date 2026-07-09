package prreview

import (
	"net/http"
	"sync"

	"github.com/tysonthomas9/loomcli/internal/connector"
	"github.com/tysonthomas9/loomcli/internal/store"
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
	// seeded caches "connector+grants already ensured" per canonical
	// ws|owner/repo so a polled read API does not re-seal + re-Create on
	// every request. Key is the canonical resource; value struct{}{}.
	seeded sync.Map
}

// NewModule constructs the pull request review route module.
func NewModule(st store.Store, disp *connector.Dispatcher) *Module {
	return &Module{store: st, dispatcher: disp}
}

// Register adds the workspace-scoped pull request review routes.
func (m *Module) Register(mux *http.ServeMux) {
	if m == nil || m.store == nil {
		return
	}
	mux.HandleFunc("GET /api/workspaces/{ws}/pull-requests/{owner}/{repo}/{number}", m.getPullRequest)
	mux.HandleFunc("GET /api/workspaces/{ws}/pull-requests/{owner}/{repo}/{number}/diff", m.getPullRequestDiff)
	mux.HandleFunc("POST /api/workspaces/{ws}/pull-requests/{owner}/{repo}/{number}/review", m.postReview)
}

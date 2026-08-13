package git

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
)

// PullRequestListModule registers the gh-backed pull-request list route for
// serve modes without a store. Store-backed serve gets the richer
// connector-primary route from the prreview module instead; without this
// fallback, non-store deployments would lose GET /pull-requests entirely.
type PullRequestListModule struct {
	checkout sourcecontrol.Checkout
}

// NewPullRequestListModule returns the gh-backed PR list fallback module.
func NewPullRequestListModule(checkout sourcecontrol.Checkout) *PullRequestListModule {
	return &PullRequestListModule{checkout: checkout}
}

// Register adds the workspace-scoped pull-request list route.
func (m *PullRequestListModule) Register(mux *http.ServeMux) {
	if m == nil || m.checkout == nil {
		return
	}
	mux.HandleFunc("GET /api/workspaces/{ws}/pull-requests", HandleListPullRequests(m.checkout))
}

package issues

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/issuetabs"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

// IssueTabModule registers the 3 workspace-scoped issue tab persistence
// routes on a [*http.ServeMux].
//
// The module is only constructed when issueTabStore is non-nil. The tmux
// session aliveness filter that used to live here was removed along with
// tmux — stale tabs are now the client's problem.
type IssueTabModule struct {
	issueTabStore *issuetabs.Store
	hub           *realtime.Hub
}

// NewIssueTabModule returns an IssueTabModule that will register routes
// using the given store and SSE hub.
func NewIssueTabModule(issueTabStore *issuetabs.Store, hub *realtime.Hub) *IssueTabModule {
	return &IssueTabModule{
		issueTabStore: issueTabStore,
		hub:           hub,
	}
}

// Register registers the 3 issue tab persistence routes.
func (m *IssueTabModule) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/workspaces/{ws}/issues/{issueId}/tabs", handleGetIssueTabs(m.issueTabStore))
	mux.HandleFunc("PUT /api/workspaces/{ws}/issues/{issueId}/tabs", handleSaveIssueTabs(m.issueTabStore, m.hub))
	mux.HandleFunc("DELETE /api/workspaces/{ws}/issues/{issueId}/tabs", handleDeleteIssueTabs(m.issueTabStore))
}

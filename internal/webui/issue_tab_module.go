package webui

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/issuetabs"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

// IssueTabModule registers the 3 workspace-scoped issue tab persistence
// routes on a [*http.ServeMux].
//
// The module is only constructed when issueTabStore is non-nil.
// All routes are unconditional within this module.
type IssueTabModule struct {
	issueTabStore *issuetabs.Store
	termMgr       *TerminalManager
	hub           *realtime.Hub
}

// NewIssueTabModule returns an IssueTabModule that will register routes
// using the given store, terminal manager, and SSE hub.
func NewIssueTabModule(issueTabStore *issuetabs.Store, termMgr *TerminalManager, hub *realtime.Hub) *IssueTabModule {
	return &IssueTabModule{
		issueTabStore: issueTabStore,
		termMgr:       termMgr,
		hub:           hub,
	}
}

// Register implements [Module] by registering 3 issue tab persistence routes.
func (m *IssueTabModule) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/workspaces/{ws}/issues/{issueId}/tabs", handleGetIssueTabs(m.issueTabStore, m.termMgr))
	mux.HandleFunc("PUT /api/workspaces/{ws}/issues/{issueId}/tabs", handleSaveIssueTabs(m.issueTabStore, m.hub))
	mux.HandleFunc("DELETE /api/workspaces/{ws}/issues/{issueId}/tabs", handleDeleteIssueTabs(m.issueTabStore))
}

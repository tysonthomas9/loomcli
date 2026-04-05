package webui

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// IssueModule registers the 11 workspace-scoped issue CRUD, comment, event,
// and dependency routes on a [*http.ServeMux].
//
// It delegates to the same package-level handler functions that
// registerWorkspaceRoutes currently calls inline. The module does not contain
// any handler logic — it is purely a registration coordinator.
//
// Register must be called at most once per mux; calling it twice on the same
// mux will panic (duplicate route patterns in Go 1.22+ ServeMux).
type IssueModule struct {
	svc               service.IssueService
	workspaceConfigFn func() (*service.WorkspaceData, error)
}

// NewIssueModule returns an IssueModule that will register routes using the
// given service and workspace config function. Nil values are accepted — the
// underlying handler functions handle nil deps at request time.
func NewIssueModule(svc service.IssueService, workspaceConfigFn func() (*service.WorkspaceData, error)) *IssueModule {
	return &IssueModule{
		svc:               svc,
		workspaceConfigFn: workspaceConfigFn,
	}
}

// Register implements [Module] by registering 11 workspace-scoped issue routes.
func (m *IssueModule) Register(mux *http.ServeMux) {
	// Issue CRUD
	mux.HandleFunc("GET /api/workspaces/{ws}/issues/{id}", handleGetIssue(m.svc))
	mux.HandleFunc("GET /api/workspaces/{ws}/issues", handleListIssues(m.svc))
	mux.HandleFunc("POST /api/workspaces/{ws}/issues", handleCreateIssue(m.svc))
	mux.HandleFunc("PATCH /api/workspaces/{ws}/issues/{id}", handlePatchIssue(m.svc))
	mux.HandleFunc("POST /api/workspaces/{ws}/issues/{id}/close", handleCloseIssue(m.svc))
	mux.HandleFunc("POST /api/workspaces/{ws}/issues/{id}/move", handleMoveIssue(m.svc, m.workspaceConfigFn))
	mux.HandleFunc("DELETE /api/workspaces/{ws}/issues/{id}", handleDeleteIssue(m.svc))

	// Comments
	mux.HandleFunc("POST /api/workspaces/{ws}/issues/{id}/comments", handleAddComment(m.svc))

	// Events
	mux.HandleFunc("GET /api/workspaces/{ws}/issues/{id}/events", handleGetIssueEvents(m.svc))

	// Dependencies
	mux.HandleFunc("POST /api/workspaces/{ws}/issues/{id}/dependencies", handleAddDependency(m.svc))
	mux.HandleFunc("DELETE /api/workspaces/{ws}/issues/{id}/dependencies/{depId}", handleRemoveDependency(m.svc))
}

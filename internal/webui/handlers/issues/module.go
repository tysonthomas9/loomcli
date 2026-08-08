package issues

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/app/workitemmove"
	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
)

// IssueModule registers the workspace-scoped issue CRUD, comment, event,
// and dependency routes on a [*http.ServeMux].
//
// It delegates transport concerns to package-level handlers backed by the
// Work Items capability and the named cross-workspace move coordinator. The
// module contains no policy or persistence logic.
//
// Register must be called at most once per mux; calling it twice on the same
// mux will panic (duplicate route patterns in Go 1.22+ ServeMux).
type IssueModule struct {
	workItems workitems.API
	mover     workitemmove.Commands
}

// NewIssueModule returns an IssueModule that will register routes using the
// given capability dependencies. Nil values are accepted — the
// underlying handler functions handle nil deps at request time.
func NewIssueModule(workItems workitems.API, mover workitemmove.Commands) *IssueModule {
	return &IssueModule{
		workItems: workItems,
		mover:     mover,
	}
}

// Register implements [Module] by registering the workspace-scoped issue routes.
func (m *IssueModule) Register(mux *http.ServeMux) {
	// Search — must register alongside {id} because Go 1.22+ ServeMux prefers
	// the literal "search" segment over the {id} wildcard, so this will route
	// correctly even though both patterns share the same prefix.
	mux.HandleFunc("GET /api/workspaces/{ws}/issues/search", HandleSearchWorkItems(m.workItems))

	// Issue CRUD
	mux.HandleFunc("GET /api/workspaces/{ws}/issues/{id}", HandleGetWorkItem(m.workItems))
	mux.HandleFunc("GET /api/workspaces/{ws}/issues", HandleListWorkItems(m.workItems))
	mux.HandleFunc("POST /api/workspaces/{ws}/issues", HandleCreateWorkItem(m.workItems))
	mux.HandleFunc("PATCH /api/workspaces/{ws}/issues/{id}", HandlePatchWorkItem(m.workItems))
	mux.HandleFunc("POST /api/workspaces/{ws}/issues/{id}/close", HandleCloseWorkItem(m.workItems))
	mux.HandleFunc("POST /api/workspaces/{ws}/issues/{id}/reopen", HandleReopenWorkItem(m.workItems))
	mux.HandleFunc("PUT /api/workspaces/{ws}/issues/{id}/repository", HandleAssignWorkItemRepository(m.workItems))
	mux.HandleFunc("POST /api/workspaces/{ws}/issues/{id}/claim", HandleClaimWorkItem(m.workItems))
	mux.HandleFunc("POST /api/workspaces/{ws}/issues/{id}/move", HandleMoveWorkItem(m.mover))
	mux.HandleFunc("DELETE /api/workspaces/{ws}/issues/{id}", HandleDeleteWorkItem(m.workItems))

	// Comments
	mux.HandleFunc("GET /api/workspaces/{ws}/issues/{id}/comments", HandleListWorkItemComments(m.workItems))
	mux.HandleFunc("POST /api/workspaces/{ws}/issues/{id}/comments", HandleAddWorkItemComment(m.workItems))

	// Events
	mux.HandleFunc("GET /api/workspaces/{ws}/issues/{id}/events", HandleGetWorkItemEvents(m.workItems))

	// Dependencies
	mux.HandleFunc("GET /api/workspaces/{ws}/issues/{id}/dependencies", HandleListWorkItemDependencies(m.workItems))
	mux.HandleFunc("POST /api/workspaces/{ws}/issues/{id}/dependencies", HandleAddWorkItemDependency(m.workItems))
	mux.HandleFunc("DELETE /api/workspaces/{ws}/issues/{id}/dependencies/{depId}", HandleRemoveWorkItemDependency(m.workItems))
}

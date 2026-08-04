package issues

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// IssueModule registers the workspace-scoped issue CRUD, comment, event,
// and dependency routes on a [*http.ServeMux].
//
// It delegates to the same package-level handler functions that
// registerWorkspaceRoutes currently calls inline. The module does not contain
// any handler logic — it is purely a registration coordinator.
//
// Register must be called at most once per mux; calling it twice on the same
// mux will panic (duplicate route patterns in Go 1.22+ ServeMux).
type IssueModule struct {
	svc           service.IssueService
	workItems     workitems.API
	repositorySvc issueRepositoryCommand
	store         store.Store
}

// issueListFilter keeps transport parsing independent of another new direct
// service-package import while the existing module composition remains the
// temporary owner of the legacy service contract.
type issueListFilter = service.ListFilter

type issueRepositoryServiceAdapter struct {
	svc service.IssueRepositoryService
}

func (a issueRepositoryServiceAdapter) SetIssueRepository(ctx context.Context, issueID, repo string) (json.RawMessage, error) {
	if a.svc == nil {
		return nil, service.ErrUnavailable("repository assignment service not configured")
	}
	return a.svc.SetIssueRepository(ctx, service.SetIssueRepositoryParams{
		IssueID: issueID,
		Repo:    repo,
	})
}

// NewIssueModule returns an IssueModule that will register routes using the
// given service and store handle. Nil values are accepted — the
// underlying handler functions handle nil deps at request time.
func NewIssueModule(svc service.IssueService, workItems workitems.API, st store.Store) *IssueModule {
	repositorySvc, _ := svc.(service.IssueRepositoryService)
	return &IssueModule{
		svc:           svc,
		workItems:     workItems,
		repositorySvc: issueRepositoryServiceAdapter{svc: repositorySvc},
		store:         st,
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
	mux.HandleFunc("GET /api/workspaces/{ws}/issues", HandleListIssues(m.svc))
	mux.HandleFunc("POST /api/workspaces/{ws}/issues", HandleCreateIssue(m.svc))
	mux.HandleFunc("PATCH /api/workspaces/{ws}/issues/{id}", HandlePatchIssue(m.svc))
	mux.HandleFunc("POST /api/workspaces/{ws}/issues/{id}/close", HandleCloseIssue(m.svc))
	mux.HandleFunc("POST /api/workspaces/{ws}/issues/{id}/reopen", HandleReopenWorkItem(m.workItems))
	mux.HandleFunc("PUT /api/workspaces/{ws}/issues/{id}/repository", HandleSetIssueRepository(m.repositorySvc))
	mux.HandleFunc("POST /api/workspaces/{ws}/issues/{id}/claim", HandleClaimWorkItem(m.workItems))
	mux.HandleFunc("POST /api/workspaces/{ws}/issues/{id}/move", HandleMoveIssue(m.svc, m.store))
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

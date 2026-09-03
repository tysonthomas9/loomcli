package issues

import (
	"net/http"

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
	svc   service.IssueService
	store store.Store
}

// NewIssueModule returns an IssueModule that will register routes using the
// given service and store handle. Nil values are accepted — the
// underlying handler functions handle nil deps at request time.
func NewIssueModule(svc service.IssueService, st store.Store) *IssueModule {
	return &IssueModule{
		svc:   svc,
		store: st,
	}
}

// Register implements [Module] by registering the workspace-scoped issue routes.
func (m *IssueModule) Register(mux *http.ServeMux) {
	// Search — must register alongside {id} because Go 1.22+ ServeMux prefers
	// the literal "search" segment over the {id} wildcard, so this will route
	// correctly even though both patterns share the same prefix.
	mux.HandleFunc("GET /api/workspaces/{ws}/issues/search", HandleSearchIssues(m.svc))

	// Issue CRUD
	mux.HandleFunc("GET /api/workspaces/{ws}/issues/{id}", HandleGetIssue(m.svc))
	mux.HandleFunc("GET /api/workspaces/{ws}/issues", HandleListIssues(m.svc))
	mux.HandleFunc("POST /api/workspaces/{ws}/issues", HandleCreateIssue(m.svc))
	mux.HandleFunc("PATCH /api/workspaces/{ws}/issues/{id}", HandlePatchIssue(m.svc))
	mux.HandleFunc("POST /api/workspaces/{ws}/issues/{id}/close", HandleCloseIssue(m.svc))
	mux.HandleFunc("POST /api/workspaces/{ws}/issues/{id}/reopen", HandleReopenIssue(m.svc))
	mux.HandleFunc("POST /api/workspaces/{ws}/issues/{id}/claim", HandleClaimIssue(m.svc))
	mux.HandleFunc("POST /api/workspaces/{ws}/issues/{id}/move", HandleMoveIssue(m.svc, m.store))
	mux.HandleFunc("POST /api/workspaces/{ws}/issues/{id}/review-decision", HandleReviewDecision(service.NewReviewDecisionService(m.svc)))
	mux.HandleFunc("DELETE /api/workspaces/{ws}/issues/{id}", HandleDeleteIssue(m.svc))

	// Comments
	mux.HandleFunc("GET /api/workspaces/{ws}/issues/{id}/comments", HandleListComments(m.svc))
	mux.HandleFunc("POST /api/workspaces/{ws}/issues/{id}/comments", HandleAddComment(m.svc))

	// Events
	mux.HandleFunc("GET /api/workspaces/{ws}/issues/{id}/events", HandleGetIssueEvents(m.svc))
	mux.HandleFunc("GET /api/workspaces/{ws}/issues/{id}/journey", HandleGetIssueJourney(m.svc))

	// Dependencies
	mux.HandleFunc("GET /api/workspaces/{ws}/issues/{id}/dependencies", HandleListDependencies(m.svc))
	mux.HandleFunc("POST /api/workspaces/{ws}/issues/{id}/dependencies", HandleAddDependency(m.svc))
	mux.HandleFunc("DELETE /api/workspaces/{ws}/issues/{id}/dependencies/{depId}", HandleRemoveDependency(m.svc))
}

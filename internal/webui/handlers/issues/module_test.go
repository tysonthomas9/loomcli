package issues

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/app/workitemmove"
	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
)

// Compile-time assertion: *IssueModule implements Module.
var _ Module = (*IssueModule)(nil)

func TestIssueModule_RegisterRoutes(t *testing.T) {
	mod := NewIssueModule(nil, nil)

	mux := http.NewServeMux()
	mod.Register(mux)

	routes := []struct {
		method string
		path   string
	}{
		{"GET", "/api/workspaces/test-ws/issues/abc"},
		{"GET", "/api/workspaces/test-ws/issues"},
		{"POST", "/api/workspaces/test-ws/issues"},
		{"PATCH", "/api/workspaces/test-ws/issues/abc"},
		{"POST", "/api/workspaces/test-ws/issues/abc/close"},
		{"POST", "/api/workspaces/test-ws/issues/abc/reopen"},
		{"PUT", "/api/workspaces/test-ws/issues/abc/repository"},
		{"POST", "/api/workspaces/test-ws/issues/abc/claim"},
		{"POST", "/api/workspaces/test-ws/issues/abc/move"},
		{"DELETE", "/api/workspaces/test-ws/issues/abc"},
		{"GET", "/api/workspaces/test-ws/issues/abc/comments"},
		{"POST", "/api/workspaces/test-ws/issues/abc/comments"},
		{"GET", "/api/workspaces/test-ws/issues/abc/events"},
		{"GET", "/api/workspaces/test-ws/issues/abc/dependencies"},
		{"POST", "/api/workspaces/test-ws/issues/abc/dependencies"},
		{"DELETE", "/api/workspaces/test-ws/issues/abc/dependencies/dep1"},
	}

	for _, rt := range routes {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(rt.method, rt.path, nil)
		mux.ServeHTTP(rec, req)

		if rec.Code == http.StatusNotFound {
			t.Errorf("%s %s: got 404, route not registered", rt.method, rt.path)
		}
		if rec.Code == http.StatusMethodNotAllowed {
			t.Errorf("%s %s: got 405, wrong method registered", rt.method, rt.path)
		}
	}
}

func TestIssueModule_ExcludedRoutes_NotRegistered(t *testing.T) {
	mod := NewIssueModule(nil, nil)

	mux := http.NewServeMux()
	mod.Register(mux)

	// Note: GET /issues/graph is NOT excluded — it matches the {id} wildcard
	// in GET /issues/{id}. It would only be excluded if registered as a
	// separate literal pattern on the same mux (done by a different module).
	excluded := []struct {
		method string
		path   string
	}{
		{"GET", "/api/workspaces/test-ws/issues/abc/tabs"},
		{"GET", "/api/workspaces/test-ws/issues/abc/sessions"},
		{"GET", "/api/workspaces/test-ws/issues/abc/git/diff-stat"},
	}

	for _, rt := range excluded {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(rt.method, rt.path, nil)
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s: expected 404 (not registered), got %d", rt.method, rt.path, rec.Code)
		}
	}
}

func TestIssueModule_WrongMethod_Returns405(t *testing.T) {
	mod := NewIssueModule(nil, nil)

	mux := http.NewServeMux()
	mod.Register(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/workspaces/test-ws/issues/abc", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("PUT /api/workspaces/test-ws/issues/abc: expected 405, got %d", rec.Code)
	}
}

func TestIssueModule_NilService(t *testing.T) {
	mod := NewIssueModule(nil, nil)

	mux := http.NewServeMux()
	mod.Register(mux) // must not panic during registration
}

type routeWorkItems struct {
	createCalls         int
	listCalls           int
	comment             workitems.AddCommentCommand
	listCommentsCalls   int
	addDependencyCalls  int
	removeDependencyIDs []string
	listDependencyCalls int
	searchCalls         int
	getIDs              []string
	claimIDs            []string
	reopenIDs           []string
	deleteIDs           []string
	eventCalls          int
	patchIDs            []string
	closeIDs            []string
	repositoryIDs       []string
	assignRepositoryErr error
}

type routeMover struct {
	calls int
}

func (m *routeMover) Move(_ context.Context, command workitemmove.Command) (*workitemmove.Result, error) {
	m.calls++
	return &workitemmove.Result{SourceID: command.IssueID, TargetID: "TARGET-1"}, nil
}

func (f *routeWorkItems) Create(_ context.Context, command workitems.CreateCommand) (*workitems.IssueSummary, error) {
	f.createCalls++
	return &workitems.IssueSummary{ID: "TASK-NEW", Title: command.Title, Status: "open"}, nil
}
func (f *routeWorkItems) List(context.Context, workitems.ListQuery) (*workitems.ListResult, error) {
	f.listCalls++
	return &workitems.ListResult{Issues: []workitems.ListItem{}}, nil
}

func (f *routeWorkItems) Ready(context.Context, workitems.AvailabilityQuery) ([]workitems.IssueSummary, error) {
	return []workitems.IssueSummary{}, nil
}

func (f *routeWorkItems) Blocked(context.Context, workitems.AvailabilityQuery) ([]workitems.IssueSummary, error) {
	return []workitems.IssueSummary{}, nil
}

func (f *routeWorkItems) Search(context.Context, workitems.SearchQuery) ([]workitems.IssueSummary, error) {
	f.searchCalls++
	return []workitems.IssueSummary{}, nil
}
func (f *routeWorkItems) Get(_ context.Context, query workitems.GetQuery) (*workitems.IssueDetail, error) {
	f.getIDs = append(f.getIDs, query.IssueID)
	return &workitems.IssueDetail{ID: query.IssueID, Status: "open", Labels: []string{}, Dependencies: []workitems.Dependency{}, Dependents: []workitems.Dependency{}, Comments: []*workitems.Comment{}}, nil
}
func (f *routeWorkItems) Patch(_ context.Context, command workitems.PatchCommand) (*workitems.IssueDetail, error) {
	f.patchIDs = append(f.patchIDs, command.IssueID)
	return &workitems.IssueDetail{ID: command.IssueID, Status: "open", Labels: []string{}, Dependencies: []workitems.Dependency{}, Dependents: []workitems.Dependency{}, Comments: []*workitems.Comment{}}, nil
}
func (f *routeWorkItems) Close(_ context.Context, command workitems.CloseCommand) (*workitems.CloseResult, error) {
	f.closeIDs = append(f.closeIDs, command.IssueID)
	return &workitems.CloseResult{Closed: &workitems.IssueSummary{ID: command.IssueID, Status: "closed"}, Unblocked: []workitems.IssueSummary{}}, nil
}
func (f *routeWorkItems) AssignRepository(_ context.Context, command workitems.AssignRepositoryCommand) (*workitems.IssueSummary, error) {
	f.repositoryIDs = append(f.repositoryIDs, command.IssueID)
	if f.assignRepositoryErr != nil {
		return nil, f.assignRepositoryErr
	}
	return &workitems.IssueSummary{ID: command.IssueID, SourceRepo: command.Repository, Repo: command.Repository}, nil
}
func (f *routeWorkItems) BlockRepositoryRequired(context.Context, workitems.BlockRepositoryRequiredCommand) (*workitems.RepositoryAdmissionResult, error) {
	return &workitems.RepositoryAdmissionResult{}, nil
}
func (f *routeWorkItems) Claim(_ context.Context, command workitems.ClaimCommand) (*workitems.IssueDetail, error) {
	f.claimIDs = append(f.claimIDs, command.IssueID)
	return &workitems.IssueDetail{ID: command.IssueID, Status: "in_progress", Labels: []string{}, Dependencies: []workitems.Dependency{}, Dependents: []workitems.Dependency{}, Comments: []*workitems.Comment{}}, nil
}
func (f *routeWorkItems) Reopen(_ context.Context, command workitems.ReopenCommand) error {
	f.reopenIDs = append(f.reopenIDs, command.IssueID)
	return nil
}
func (f *routeWorkItems) Delete(_ context.Context, command workitems.DeleteCommand) (workitems.DeleteResult, error) {
	f.deleteIDs = append(f.deleteIDs, command.IssueID)
	return workitems.DeleteResult{DeletedCount: 1, DeletedIDs: []string{command.IssueID}}, nil
}
func (f *routeWorkItems) ListEvents(context.Context, workitems.ListEventsQuery) ([]*workitems.Event, error) {
	f.eventCalls++
	return []*workitems.Event{}, nil
}

func (f *routeWorkItems) AddComment(_ context.Context, command workitems.AddCommentCommand) (*workitems.Comment, error) {
	f.comment = command
	return &workitems.Comment{ID: 7, IssueID: command.IssueID, Author: command.Author, Text: strings.TrimSpace(command.Text)}, nil
}

func (f *routeWorkItems) ListComments(context.Context, workitems.ListCommentsQuery) ([]*workitems.Comment, error) {
	f.listCommentsCalls++
	return []*workitems.Comment{}, nil
}
func (f *routeWorkItems) AddDependency(context.Context, workitems.AddDependencyCommand) error {
	f.addDependencyCalls++
	return nil
}
func (f *routeWorkItems) RemoveDependency(_ context.Context, command workitems.RemoveDependencyCommand) error {
	f.removeDependencyIDs = append(f.removeDependencyIDs, command.DependsOnID)
	return nil
}
func (f *routeWorkItems) ListDependencies(context.Context, workitems.ListDependenciesQuery) ([]workitems.Dependency, error) {
	f.listDependencyCalls++
	return []workitems.Dependency{}, nil
}

func TestIssueModule_WorkItemRoutesUseCapability(t *testing.T) {
	capability := &routeWorkItems{}
	mover := &routeMover{}
	mod := NewIssueModule(capability, mover)
	mux := http.NewServeMux()
	mod.Register(mux)

	requests := []struct {
		method string
		path   string
		body   string
		status int
	}{
		{http.MethodGet, "/api/workspaces/ws/issues/search?q=proof", "", http.StatusOK},
		{http.MethodGet, "/api/workspaces/ws/issues/TASK-1", "", http.StatusOK},
		{http.MethodPost, "/api/workspaces/ws/issues/TASK-1/claim", "", http.StatusOK},
		{http.MethodPost, "/api/workspaces/ws/issues/TASK-1/reopen", `{}`, http.StatusOK},
		{http.MethodDelete, "/api/workspaces/ws/issues/TASK-1", "", http.StatusOK},
		{http.MethodGet, "/api/workspaces/ws/issues/TASK-1/events", "", http.StatusOK},
		{http.MethodPost, "/api/workspaces/ws/issues", `{"title":"new task","issue_type":"task","priority":2,"source_repo":"loomcli"}`, http.StatusCreated},
		{http.MethodGet, "/api/workspaces/ws/issues", "", http.StatusOK},
		{http.MethodPatch, "/api/workspaces/ws/issues/TASK-1", `{"title":"updated"}`, http.StatusOK},
		{http.MethodPost, "/api/workspaces/ws/issues/TASK-1/close", `{"reason":"done"}`, http.StatusOK},
		{http.MethodPut, "/api/workspaces/ws/issues/TASK-1/repository", `{"repo":"loomcli"}`, http.StatusOK},
		{http.MethodPost, "/api/workspaces/ws/issues/TASK-1/move", `{"target_workspace":"target"}`, http.StatusOK},
		{http.MethodPost, "/api/workspaces/ws/issues/TASK-1/comments", `{"text":" proof "}`, http.StatusCreated},
		{http.MethodGet, "/api/workspaces/ws/issues/TASK-1/comments", "", http.StatusOK},
		{http.MethodPost, "/api/workspaces/ws/issues/TASK-1/dependencies", `{"depends_on_id":"TASK-2"}`, http.StatusOK},
		{http.MethodDelete, "/api/workspaces/ws/issues/TASK-1/dependencies/TASK-2", "", http.StatusOK},
		{http.MethodGet, "/api/workspaces/ws/issues/TASK-1/dependencies", "", http.StatusOK},
	}
	for _, request := range requests {
		req := httptest.NewRequest(request.method, request.path, strings.NewReader(request.body))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != request.status {
			t.Fatalf("%s %s: expected %d, got %d: %s", request.method, request.path, request.status, rec.Code, rec.Body.String())
		}
	}
	if capability.comment.IssueID != "TASK-1" || capability.comment.Text != " proof " {
		t.Fatalf("route did not invoke Work Items API: %#v", capability.comment)
	}
	if mover.calls != 1 {
		t.Fatalf("move route did not invoke coordinator: %#v", mover)
	}
	if capability.createCalls != 1 || capability.listCalls != 1 || capability.searchCalls != 1 || len(capability.getIDs) != 1 || capability.getIDs[0] != "TASK-1" || len(capability.claimIDs) != 1 || capability.claimIDs[0] != "TASK-1" || len(capability.reopenIDs) != 1 || capability.reopenIDs[0] != "TASK-1" || len(capability.deleteIDs) != 1 || capability.deleteIDs[0] != "TASK-1" || capability.eventCalls != 1 || len(capability.patchIDs) != 1 || capability.patchIDs[0] != "TASK-1" || len(capability.closeIDs) != 1 || capability.closeIDs[0] != "TASK-1" || len(capability.repositoryIDs) != 1 || capability.repositoryIDs[0] != "TASK-1" || capability.listCommentsCalls != 1 || capability.addDependencyCalls != 1 || capability.listDependencyCalls != 1 || len(capability.removeDependencyIDs) != 1 || capability.removeDependencyIDs[0] != "TASK-2" {
		t.Fatalf("unexpected Work Items route calls: %#v", capability)
	}
}

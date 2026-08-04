package issues

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
	"github.com/tysonthomas9/loomcli/internal/types"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// Compile-time assertion: *IssueModule implements Module.
var _ Module = (*IssueModule)(nil)

func TestIssueModule_RegisterRoutes(t *testing.T) {
	svc := &mockIssueService{}
	mod := NewIssueModule(svc, nil, nil)

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
	svc := &mockIssueService{}
	mod := NewIssueModule(svc, nil, nil)

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
	svc := &mockIssueService{}
	mod := NewIssueModule(svc, nil, nil)

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
	mod := NewIssueModule(nil, nil, nil)

	mux := http.NewServeMux()
	mod.Register(mux) // must not panic during registration
}

type routeWorkItems struct {
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
}

func (f *routeWorkItems) Search(context.Context, workitems.SearchQuery) ([]workitems.IssueSummary, error) {
	f.searchCalls++
	return []workitems.IssueSummary{}, nil
}
func (f *routeWorkItems) Get(_ context.Context, query workitems.GetQuery) (*workitems.IssueDetail, error) {
	f.getIDs = append(f.getIDs, query.IssueID)
	return &workitems.IssueDetail{ID: query.IssueID, Status: "open", Labels: []string{}, Dependencies: []workitems.Dependency{}, Dependents: []workitems.Dependency{}, Comments: []*workitems.Comment{}}, nil
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
	legacy := &mockIssueService{
		getIssueFunc: func(context.Context, string) (json.RawMessage, error) {
			t.Fatal("legacy IssueService get path was called")
			return nil, nil
		},
		claimIssueFunc: func(context.Context, service.ClaimIssueParams) (json.RawMessage, error) {
			t.Fatal("legacy IssueService claim path was called")
			return nil, nil
		},
		searchIssuesFunc: func(context.Context, service.SearchIssuesParams) (json.RawMessage, error) {
			t.Fatal("legacy IssueService search path was called")
			return nil, nil
		},
		reopenIssueFunc: func(context.Context, service.ReopenIssueParams) error {
			t.Fatal("legacy IssueService reopen path was called")
			return nil
		},
		deleteIssueFunc: func(context.Context, string) (json.RawMessage, error) {
			t.Fatal("legacy IssueService delete path was called")
			return nil, nil
		},
		listEventsFunc: func(context.Context, service.EventListParams) ([]*types.Event, error) {
			t.Fatal("legacy IssueService list-events path was called")
			return nil, nil
		},
		addCommentFunc: func(context.Context, service.AddCommentParams) (*types.Comment, error) {
			t.Fatal("legacy IssueService add-comment path was called")
			return nil, nil
		},
		listCommentsFunc: func(context.Context, string) ([]*types.Comment, error) {
			t.Fatal("legacy IssueService list-comments path was called")
			return nil, nil
		},
		addDependencyFunc: func(context.Context, service.AddDependencyParams) error {
			t.Fatal("legacy IssueService add-dependency path was called")
			return nil
		},
		removeDependencyFunc: func(context.Context, service.RemoveDependencyParams) error {
			t.Fatal("legacy IssueService remove-dependency path was called")
			return nil
		},
		listDependenciesFunc: func(context.Context, string) (json.RawMessage, error) {
			t.Fatal("legacy IssueService list-dependencies path was called")
			return nil, nil
		},
	}
	capability := &routeWorkItems{}
	mod := NewIssueModule(legacy, capability, nil)
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
	if capability.searchCalls != 1 || len(capability.getIDs) != 1 || capability.getIDs[0] != "TASK-1" || len(capability.claimIDs) != 1 || capability.claimIDs[0] != "TASK-1" || len(capability.reopenIDs) != 1 || capability.reopenIDs[0] != "TASK-1" || len(capability.deleteIDs) != 1 || capability.deleteIDs[0] != "TASK-1" || capability.eventCalls != 1 || capability.listCommentsCalls != 1 || capability.addDependencyCalls != 1 || capability.listDependencyCalls != 1 || len(capability.removeDependencyIDs) != 1 || capability.removeDependencyIDs[0] != "TASK-2" {
		t.Fatalf("unexpected Work Items route calls: %#v", capability)
	}
}

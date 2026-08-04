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
	if capability.listCommentsCalls != 1 || capability.addDependencyCalls != 1 || capability.listDependencyCalls != 1 || len(capability.removeDependencyIDs) != 1 || capability.removeDependencyIDs[0] != "TASK-2" {
		t.Fatalf("unexpected Work Items route calls: %#v", capability)
	}
}

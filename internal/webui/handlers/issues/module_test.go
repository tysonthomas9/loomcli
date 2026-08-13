package issues

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/types"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// Compile-time assertion: *IssueModule implements Module.
var _ Module = (*IssueModule)(nil)

func TestIssueModule_RegisterRoutes(t *testing.T) {
	svc := &mockIssueService{}
	mod := NewIssueModule(svc, nil)

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
	mod := NewIssueModule(svc, nil)

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
	mod := NewIssueModule(svc, nil)

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

func TestIssueModule_AllRoutesStampWebUIActor(t *testing.T) {
	var (
		seen     bool
		gotActor middleware.Actor
	)
	record := func(ctx context.Context) {
		seen = true
		actor, ok := middleware.ActorFromContext(ctx)
		if !ok {
			gotActor = middleware.Actor{}
			return
		}
		gotActor = actor
	}
	svc := &mockIssueService{
		getIssueFunc: func(ctx context.Context, _ string) (json.RawMessage, error) {
			record(ctx)
			return json.RawMessage(`{}`), nil
		},
		listIssuesFunc: func(ctx context.Context, _ service.ListIssuesParams) (*service.ListIssuesResult, error) {
			record(ctx)
			return &service.ListIssuesResult{Issues: []service.IssueWithParent{}}, nil
		},
		createIssueFunc: func(ctx context.Context, _ service.CreateIssueParams) (json.RawMessage, error) {
			record(ctx)
			return json.RawMessage(`{"id":"issue-1"}`), nil
		},
		patchIssueFunc: func(ctx context.Context, _ service.PatchIssueParams) error {
			record(ctx)
			return nil
		},
		closeIssueFunc: func(ctx context.Context, _ service.CloseIssueParams) (json.RawMessage, error) {
			record(ctx)
			return json.RawMessage(`{}`), nil
		},
		reopenIssueFunc: func(ctx context.Context, _ service.ReopenIssueParams) error {
			record(ctx)
			return nil
		},
		claimIssueFunc: func(ctx context.Context, _ service.ClaimIssueParams) (json.RawMessage, error) {
			record(ctx)
			return json.RawMessage(`{}`), nil
		},
		deleteIssueFunc: func(ctx context.Context, _ string) (json.RawMessage, error) {
			record(ctx)
			return json.RawMessage(`{}`), nil
		},
		addCommentFunc: func(ctx context.Context, _ service.AddCommentParams) (*types.Comment, error) {
			record(ctx)
			return &types.Comment{}, nil
		},
		listCommentsFunc: func(ctx context.Context, _ string) ([]*types.Comment, error) {
			record(ctx)
			return []*types.Comment{}, nil
		},
		addDependencyFunc: func(ctx context.Context, _ service.AddDependencyParams) error {
			record(ctx)
			return nil
		},
		removeDependencyFunc: func(ctx context.Context, _ service.RemoveDependencyParams) error {
			record(ctx)
			return nil
		},
		listDependenciesFunc: func(ctx context.Context, _ string) (json.RawMessage, error) {
			record(ctx)
			return json.RawMessage(`[]`), nil
		},
		listEventsFunc: func(ctx context.Context, _ service.EventListParams) ([]*types.Event, error) {
			record(ctx)
			return []*types.Event{}, nil
		},
		moveIssueFunc: func(ctx context.Context, _ service.MoveIssueParams) (*service.MoveIssueResult, error) {
			record(ctx)
			return &service.MoveIssueResult{SourceID: "issue-1", TargetID: "issue-1"}, nil
		},
		searchIssuesFunc: func(ctx context.Context, _ service.SearchIssuesParams) (json.RawMessage, error) {
			record(ctx)
			return json.RawMessage(`[]`), nil
		},
	}
	mux := http.NewServeMux()
	NewIssueModule(svc, nil).Register(mux)

	routes := []struct {
		method string
		path   string
		body   string
	}{
		{method: http.MethodGet, path: "/api/workspaces/test-ws/issues/search?q=test"},
		{method: http.MethodGet, path: "/api/workspaces/test-ws/issues/issue-1"},
		{method: http.MethodGet, path: "/api/workspaces/test-ws/issues"},
		{method: http.MethodPost, path: "/api/workspaces/test-ws/issues", body: `{"title":"test","issue_type":"task","priority":2}`},
		{method: http.MethodPatch, path: "/api/workspaces/test-ws/issues/issue-1", body: `{}`},
		{method: http.MethodPost, path: "/api/workspaces/test-ws/issues/issue-1/close", body: `{}`},
		{method: http.MethodPost, path: "/api/workspaces/test-ws/issues/issue-1/reopen", body: `{}`},
		{method: http.MethodPost, path: "/api/workspaces/test-ws/issues/issue-1/claim"},
		{method: http.MethodPost, path: "/api/workspaces/test-ws/issues/issue-1/move", body: `{"target_workspace":"other-ws"}`},
		{method: http.MethodDelete, path: "/api/workspaces/test-ws/issues/issue-1"},
		{method: http.MethodGet, path: "/api/workspaces/test-ws/issues/issue-1/comments"},
		{method: http.MethodPost, path: "/api/workspaces/test-ws/issues/issue-1/comments", body: `{"body":"hello"}`},
		{method: http.MethodGet, path: "/api/workspaces/test-ws/issues/issue-1/events"},
		{method: http.MethodGet, path: "/api/workspaces/test-ws/issues/issue-1/dependencies"},
		{method: http.MethodPost, path: "/api/workspaces/test-ws/issues/issue-1/dependencies", body: `{"depends_on_id":"issue-2"}`},
		{method: http.MethodDelete, path: "/api/workspaces/test-ws/issues/issue-1/dependencies/issue-2"},
	}

	for _, route := range routes {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			seen = false
			gotActor = middleware.Actor{}
			req := httptest.NewRequest(route.method, route.path, strings.NewReader(route.body))
			rec := httptest.NewRecorder()

			mux.ServeHTTP(rec, req)

			if !seen {
				t.Fatalf("service spy was not reached; status = %d, body = %s", rec.Code, rec.Body.String())
			}
			if err := gotActor.Validate(); err != nil {
				t.Fatalf("stamped actor is invalid: %v", err)
			}
			if got := gotActor.Kind(); got != middleware.ActorKindWebUI {
				t.Fatalf("actor kind = %q, want %q", got, middleware.ActorKindWebUI)
			}
		})
	}
}

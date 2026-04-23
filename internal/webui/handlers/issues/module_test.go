package issues

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/ops"
)

// Compile-time assertion: *IssueModule implements Module.
var _ Module = (*IssueModule)(nil)

func TestIssueModule_RegisterRoutes(t *testing.T) {
	svc := &mockIssueService{}
	configFn := func() (*ops.WorkspaceData, error) { return nil, nil }
	mod := NewIssueModule(svc, configFn)

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
	configFn := func() (*ops.WorkspaceData, error) { return nil, nil }
	mod := NewIssueModule(svc, configFn)

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
	configFn := func() (*ops.WorkspaceData, error) { return nil, nil }
	mod := NewIssueModule(svc, configFn)

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

package issues

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Compile-time assertion: *IssueTabModule implements Module.
var _ Module = (*IssueTabModule)(nil)

func TestIssueTabModule_RegisterRoutes(t *testing.T) {
	mod := NewIssueTabModule(nil, nil)

	mux := http.NewServeMux()
	mod.Register(mux)

	routes := []struct {
		method string
		path   string
	}{
		{"GET", "/api/workspaces/test-ws/issues/issue1/tabs"},
		{"PUT", "/api/workspaces/test-ws/issues/issue1/tabs"},
		{"DELETE", "/api/workspaces/test-ws/issues/issue1/tabs"},
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

func TestIssueTabModule_WrongMethod_Returns405(t *testing.T) {
	mod := NewIssueTabModule(nil, nil)

	mux := http.NewServeMux()
	mod.Register(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/workspaces/test-ws/issues/issue1/tabs", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST .../issues/issue1/tabs: expected 405, got %d", rec.Code)
	}
}

package handlermux

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
)

// Compile-time assertion: *WorkspaceOpsModule implements Module.
var _ Module = (*WorkspaceOpsModule)(nil)

func TestWorkspaceOpsModule_RegisterRoutes(t *testing.T) {
	mod := NewWorkspaceOpsModule(&mockWorkspaceService{}, &stubErrorPool{}, nil)

	mux := http.NewServeMux()
	mod.Register(mux)

	// Note: PATCH /api/workspaces/{ws}/name and PATCH /api/workspaces/{ws}/config/backend
	// are deliberately registered on the outer mux (in app/routes.go), not via this
	// module, because Go 1.22+ http.ServeMux has a bug where r.Body.Read() hangs for
	// PATCH requests routed through a nested mux via wildcard subtree pattern.
	routes := []struct {
		method string
		path   string
	}{
		{"GET", "/api/workspaces/test-ws/stats"},
		{"GET", "/api/workspaces/test-ws/ready"},
		{"GET", "/api/workspaces/test-ws/blocked"},
		{"GET", "/api/workspaces/test-ws/issues/graph"},
		{"GET", "/api/workspaces/test-ws/config/backend"},
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

func TestWorkspaceOpsModule_WrongMethod_Returns405(t *testing.T) {
	mod := NewWorkspaceOpsModule(&mockWorkspaceService{}, &stubErrorPool{}, nil)

	mux := http.NewServeMux()
	mod.Register(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/workspaces/test-ws/stats", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("DELETE /api/workspaces/test-ws/stats: expected 405, got %d", rec.Code)
	}
}

func TestWorkspaceOpsModule_NilDeps(t *testing.T) {
	mod := NewWorkspaceOpsModule(nil, nil, nil)

	mux := http.NewServeMux()
	mod.Register(mux) // must not panic during registration
}

func TestWorkspaceOpsModule_TypedNilPoolUsesBackend(t *testing.T) {
	var typedNilPool *daemon.MultiPool
	mod := NewWorkspaceOpsModule(&mockWorkspaceService{}, typedNilPool, nil).
		WithIssueBackendFn(func(context.Context) backend.IssueBackend {
			return &stubIssueBackend{}
		}).
		WithDaemonExpected(false)

	mux := http.NewServeMux()
	mod.Register(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/test-ws/ready", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /ready status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
}

type stubIssueBackend struct {
	backend.IssueBackend
}

func (s *stubIssueBackend) Ready(_ context.Context, _ backend.ReadyOpts) ([]backend.IssueData, error) {
	return []backend.IssueData{}, nil
}

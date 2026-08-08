package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
	workspacemodule "github.com/tysonthomas9/loomcli/internal/modules/workspace"
	"github.com/tysonthomas9/loomcli/internal/ops"
)

// Compile-time assertion: *WorkspaceOpsModule implements wsModule.
var _ wsModule = (*WorkspaceOpsModule)(nil)

func TestWorkspaceOpsModule_RegisterRoutes(t *testing.T) {
	mod := NewWorkspaceOpsModule(&workspaceRoutesMockWorkspaceService{}, nil)

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
		{"GET", "/api/workspaces/test-ws/readyz"},
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
	mod := NewWorkspaceOpsModule(&workspaceRoutesMockWorkspaceService{}, nil)

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
	mod := NewWorkspaceOpsModule(nil, nil)

	mux := http.NewServeMux()
	mod.Register(mux) // must not panic during registration
}

func TestWorkspaceOpsModuleUsesWorkItemsForReady(t *testing.T) {
	mod := NewWorkspaceOpsModule(&workspaceRoutesMockWorkspaceService{}, nil).
		WithWorkItems(stubReadyQueries{})

	mux := http.NewServeMux()
	mod.Register(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/test-ws/ready", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /ready status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
}

func TestWorkspaceOpsModuleUsesWorkspaceCatalogForRepositoryReads(t *testing.T) {
	catalog := &stubWorkspaceCatalog{}
	projection := &stubWorkspaceCatalogProjection{}
	mod := NewWorkspaceOpsModule(&workspaceRoutesMockWorkspaceService{}, nil).
		WithWorkspaceCatalog(catalog, projection)

	mux := http.NewServeMux()
	mod.Register(mux)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/workspaces/alpha/repos", nil)
	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("GET repositories status = %d, want %d; body: %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if catalog.reference != "alpha" || catalog.repositoryWorkspace != "ALPHA" || projection.workspace != "ALPHA" {
		t.Fatalf("catalog reference=%q repository workspace=%q projection workspace=%q", catalog.reference, catalog.repositoryWorkspace, projection.workspace)
	}
	if body := recorder.Body.String(); !strings.Contains(body, `"remote_url":"https://example.com/loom.git"`) || !strings.Contains(body, `"path":"/workspace/loom"`) {
		t.Fatalf("response does not compose catalog and local checkout state: %s", body)
	}
}

type stubReadyQueries struct{}

func (stubReadyQueries) Ready(context.Context, workitems.AvailabilityQuery) ([]workitems.IssueSummary, error) {
	return []workitems.IssueSummary{}, nil
}

type stubWorkspaceCatalog struct {
	reference           string
	repositoryWorkspace string
}

func (*stubWorkspaceCatalog) Create(context.Context, workspacemodule.CreateCommand) (*workspacemodule.Reference, error) {
	return nil, nil
}

func (s *stubWorkspaceCatalog) Resolve(_ context.Context, query workspacemodule.ResolveQuery) (*workspacemodule.Reference, error) {
	s.reference = query.Reference
	return &workspacemodule.Reference{Key: "ALPHA", Name: "Alpha"}, nil
}

func (*stubWorkspaceCatalog) List(context.Context, workspacemodule.ListQuery) ([]workspacemodule.Reference, error) {
	return nil, nil
}

func (*stubWorkspaceCatalog) Rename(context.Context, workspacemodule.RenameCommand) (*workspacemodule.Reference, error) {
	return nil, nil
}

func (*stubWorkspaceCatalog) SetDesignFormat(context.Context, workspacemodule.SetDesignFormatCommand) (*workspacemodule.Reference, error) {
	return nil, nil
}

func (*stubWorkspaceCatalog) SetLifecycle(context.Context, workspacemodule.SetLifecycleCommand) (*workspacemodule.Reference, error) {
	return nil, nil
}

func (*stubWorkspaceCatalog) Delete(context.Context, workspacemodule.DeleteCommand) (*workspacemodule.Reference, error) {
	return nil, nil
}

func (*stubWorkspaceCatalog) GetRepository(context.Context, workspacemodule.GetRepositoryQuery) (*workspacemodule.Repository, error) {
	return nil, nil
}

func (s *stubWorkspaceCatalog) ListRepositories(_ context.Context, query workspacemodule.ListRepositoriesQuery) ([]workspacemodule.Repository, error) {
	s.repositoryWorkspace = query.WorkspaceReference
	return []workspacemodule.Repository{{
		WorkspaceKey: "ALPHA",
		Name:         "loom",
		RemoteURL:    "https://example.com/loom.git",
	}}, nil
}

func (*stubWorkspaceCatalog) RegisterRepository(context.Context, workspacemodule.RegisterRepositoryCommand) (*workspacemodule.Repository, error) {
	return nil, nil
}

func (*stubWorkspaceCatalog) UpdateRepository(context.Context, workspacemodule.UpdateRepositoryCommand) (*workspacemodule.Repository, error) {
	return nil, nil
}

func (*stubWorkspaceCatalog) UnregisterRepository(context.Context, workspacemodule.UnregisterRepositoryCommand) (*workspacemodule.Repository, error) {
	return nil, nil
}

type stubWorkspaceCatalogProjection struct {
	workspace string
}

func (*stubWorkspaceCatalogProjection) ActiveWorkspaceKey(context.Context) string { return "" }
func (*stubWorkspaceCatalogProjection) WorkspacePath(string) string               { return "" }
func (s *stubWorkspaceCatalogProjection) WorkspaceTopology(_ context.Context, workspace string) (*ops.WorkspaceData, error) {
	s.workspace = workspace
	return &ops.WorkspaceData{Repos: []ops.WorkspaceRepo{{Name: "loom", Path: "/workspace/loom"}}}, nil
}

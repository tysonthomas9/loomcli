package workspace

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	workspacemodule "github.com/tysonthomas9/loomcli/internal/modules/workspace"
	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

type fakeCatalogAPI struct {
	values        []workspacemodule.Reference
	value         *workspacemodule.Reference
	err           error
	resolveQuery  workspacemodule.ResolveQuery
	renameCommand workspacemodule.RenameCommand
	formatCommand workspacemodule.SetDesignFormatCommand
	repoValues    []workspacemodule.Repository
	repoListQuery workspacemodule.ListRepositoriesQuery
}

func (f *fakeCatalogAPI) Resolve(_ context.Context, query workspacemodule.ResolveQuery) (*workspacemodule.Reference, error) {
	f.resolveQuery = query
	return f.value, f.err
}

func (f *fakeCatalogAPI) List(context.Context, workspacemodule.ListQuery) ([]workspacemodule.Reference, error) {
	return f.values, f.err
}

func (f *fakeCatalogAPI) Rename(_ context.Context, command workspacemodule.RenameCommand) (*workspacemodule.Reference, error) {
	f.renameCommand = command
	return f.value, f.err
}

func (f *fakeCatalogAPI) SetDesignFormat(_ context.Context, command workspacemodule.SetDesignFormatCommand) (*workspacemodule.Reference, error) {
	f.formatCommand = command
	return f.value, f.err
}

func (f *fakeCatalogAPI) Delete(context.Context, workspacemodule.DeleteCommand) (*workspacemodule.Reference, error) {
	return f.value, f.err
}

func (f *fakeCatalogAPI) GetRepository(context.Context, workspacemodule.GetRepositoryQuery) (*workspacemodule.Repository, error) {
	return nil, f.err
}

func (f *fakeCatalogAPI) ListRepositories(_ context.Context, query workspacemodule.ListRepositoriesQuery) ([]workspacemodule.Repository, error) {
	f.repoListQuery = query
	return f.repoValues, f.err
}

type fakeCatalogProjection struct {
	active      string
	paths       map[string]string
	topology    *ops.WorkspaceData
	topologyErr error
	topologyKey string
}

func (f *fakeCatalogProjection) ActiveWorkspaceKey(context.Context) string { return f.active }
func (f *fakeCatalogProjection) WorkspacePath(key string) string           { return f.paths[key] }
func (f *fakeCatalogProjection) WorkspaceTopology(_ context.Context, key string) (*ops.WorkspaceData, error) {
	f.topologyKey = key
	return f.topology, f.topologyErr
}

func TestCatalogListComposesMachineLocalProjection(t *testing.T) {
	api := &fakeCatalogAPI{values: []workspacemodule.Reference{
		{Key: "ALPHA", Name: "Alpha"}, {Key: "BETA", Name: "Beta"},
	}}
	projection := &fakeCatalogProjection{active: "BETA", paths: map[string]string{"ALPHA": "/a", "BETA": "/b"}}
	recorder := httptest.NewRecorder()
	HandleCatalogList(api, projection).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/workspaces", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		Success    bool              `json:"success"`
		Workspaces []CatalogListItem `json:"workspaces"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Success || len(body.Workspaces) != 2 || !body.Workspaces[1].Active || body.Workspaces[0].Path != "/a" {
		t.Fatalf("unexpected body: %#v", body)
	}
}

func TestCatalogGetResolvesBeforeReadingTopology(t *testing.T) {
	api := &fakeCatalogAPI{value: &workspacemodule.Reference{Key: "ALPHA", Name: "Alpha"}}
	projection := &fakeCatalogProjection{topology: &ops.WorkspaceData{ID: "ALPHA", Name: "Alpha"}}
	request := httptest.NewRequest(http.MethodGet, "/api/workspaces/Alpha", nil)
	request.SetPathValue("ws", "Alpha")
	recorder := httptest.NewRecorder()
	HandleCatalogGet(api, projection).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || api.resolveQuery.Reference != "Alpha" || projection.topologyKey != "ALPHA" {
		t.Fatalf("status=%d query=%#v topology_key=%q body=%s", recorder.Code, api.resolveQuery, projection.topologyKey, recorder.Body.String())
	}
}

func TestCatalogRepositoriesUsesWorkspaceCatalogWithLocalProjectionOnlyForCheckoutState(t *testing.T) {
	api := &fakeCatalogAPI{
		value: &workspacemodule.Reference{Key: "ALPHA", Name: "Alpha"},
		repoValues: []workspacemodule.Repository{{
			WorkspaceKey: "ALPHA", Name: "loom", RemoteURL: "https://example.com/loom.git",
			Groups: []string{"core"},
		}},
	}
	projection := &fakeCatalogProjection{topology: &ops.WorkspaceData{Repos: []ops.WorkspaceRepo{{
		Name: "loom", Path: "/workspace/loom", CurrentBranch: "feature", RemoteURL: "must-not-win",
	}}}}
	request := httptest.NewRequest(http.MethodGet, "/api/workspaces/Alpha/repos", nil)
	request.SetPathValue("ws", "Alpha")
	recorder := httptest.NewRecorder()
	HandleCatalogRepositories(api, projection).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || api.repoListQuery.WorkspaceReference != "ALPHA" || projection.topologyKey != "ALPHA" {
		t.Fatalf("status=%d query=%#v topology_key=%q body=%s", recorder.Code, api.repoListQuery, projection.topologyKey, recorder.Body.String())
	}
	var body struct {
		Repos []ops.WorkspaceRepo `json:"repos"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Repos) != 1 || body.Repos[0].Path != "/workspace/loom" || body.Repos[0].CurrentBranch != "feature" ||
		body.Repos[0].RemoteURL != "https://example.com/loom.git" || body.Repos[0].DefaultBranch != "main" || body.Repos[0].Remote != "origin" {
		t.Fatalf("unexpected repositories: %#v", body.Repos)
	}
}

func TestCatalogRenameUsesWorkspaceCommandAndReturnsTopology(t *testing.T) {
	api := &fakeCatalogAPI{value: &workspacemodule.Reference{Key: "ALPHA", Name: "Renamed"}}
	projection := &fakeCatalogProjection{topology: &ops.WorkspaceData{ID: "ALPHA", Name: "Renamed"}}
	request := httptest.NewRequest(http.MethodPatch, "/api/workspaces/ALPHA/name", strings.NewReader(`{"new_name":"Renamed"}`))
	request = request.WithContext(middleware.WithWorkspace(request.Context(), "ALPHA"))
	recorder := httptest.NewRecorder()
	HandleCatalogRename(api, projection).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || api.renameCommand.Reference != "ALPHA" || api.renameCommand.Name != "Renamed" || projection.topologyKey != "ALPHA" {
		t.Fatalf("status=%d command=%#v topology_key=%q body=%s", recorder.Code, api.renameCommand, projection.topologyKey, recorder.Body.String())
	}
}

func TestCatalogDesignFormatRejectsInvalidThroughWorkspacePolicy(t *testing.T) {
	api := &fakeCatalogAPI{err: workspacemodule.ErrInvalid}
	projection := &fakeCatalogProjection{}
	request := httptest.NewRequest(http.MethodPatch, "/api/workspaces/ALPHA/config/design-format", strings.NewReader(`{"design_format":"svg"}`))
	request = request.WithContext(middleware.WithWorkspace(request.Context(), "ALPHA"))
	recorder := httptest.NewRecorder()
	HandleCatalogDesignFormatPatch(api, projection).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest || api.formatCommand.Format != "svg" || projection.topologyKey != "" {
		t.Fatalf("status=%d command=%#v topology_key=%q body=%s", recorder.Code, api.formatCommand, projection.topologyKey, recorder.Body.String())
	}
}

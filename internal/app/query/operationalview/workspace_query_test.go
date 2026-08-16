package operationalview_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/app/query/operationalview"
	workspaceowner "github.com/tysonthomas9/loomcli/internal/modules/workspace"
)

type workspaceRecords struct{ values []*workspaceowner.Workspace }

func (records workspaceRecords) Get(_ context.Context, key string) (*workspaceowner.Workspace, error) {
	for _, value := range records.values {
		if value.Key == key {
			copy := *value
			return &copy, nil
		}
	}
	return nil, workspaceowner.ErrNotFound
}

func (records workspaceRecords) GetByName(_ context.Context, name string) (*workspaceowner.Workspace, error) {
	for _, value := range records.values {
		if value.Name == name {
			copy := *value
			return &copy, nil
		}
	}
	return nil, workspaceowner.ErrNotFound
}

func (records workspaceRecords) List(context.Context) ([]*workspaceowner.Workspace, error) {
	out := make([]*workspaceowner.Workspace, len(records.values))
	for index, value := range records.values {
		copy := *value
		out[index] = &copy
	}
	return out, nil
}

type repositoryRecords map[string][]*workspaceowner.Repository

func (records repositoryRecords) List(_ context.Context, workspace string) ([]*workspaceowner.Repository, error) {
	values := records[workspace]
	out := make([]*workspaceowner.Repository, len(values))
	for index, value := range values {
		copy := *value
		copy.Groups = append([]string(nil), value.Groups...)
		out[index] = &copy
	}
	return out, nil
}

type placement struct {
	workspaces map[string]string
	repos      map[string]string
	backends   map[string]string
}

func (value placement) WorkspacePath(key string) string { return value.workspaces[key] }
func (value placement) RepositoryPath(workspace, repository string) string {
	return value.repos[workspace+"/"+repository]
}
func (value placement) Backend(key string) string { return value.backends[key] }

func TestWorkspaceTopologyQueryComposesImmutableCrossOwnerView(t *testing.T) {
	workspaces := workspaceRecords{values: []*workspaceowner.Workspace{
		{Key: "ZULU", Name: "Zulu", State: workspaceowner.StateReady},
		{Key: "ALPHA", Name: "Alpha", DesignFormat: "gherkin", State: workspaceowner.StateReady},
	}}
	repositoryGroups := []string{"backend", "shared"}
	repositories := repositoryRecords{
		"ALPHA": {{WorkspaceKey: "ALPHA", Name: "loom", RemoteURL: "git@example/loom", Groups: repositoryGroups}},
		"ZULU":  {{WorkspaceKey: "ZULU", Name: "docs", DefaultBranch: "trunk", Remote: "upstream"}},
	}
	query := operationalview.NewWorkspaceTopologyQuery(
		workspaces, repositories,
		placement{
			workspaces: map[string]string{"ALPHA": "/work/alpha", "ZULU": "/work/zulu"},
			repos:      map[string]string{"ALPHA/loom": "/checkout/loom"},
			backends:   map[string]string{"ALPHA": "codex"},
		},
		func(context.Context) (string, error) { return "", nil },
	)

	view, err := query.Active(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if view.ID != "ALPHA" || view.Path != "/work/alpha" || view.DesignFormat != "gherkin" {
		t.Fatalf("workspace view = %+v", view)
	}
	if len(view.Repos) != 1 || view.Repos[0].Path != "/checkout/loom" ||
		view.Repos[0].DefaultBranch != "main" || view.Repos[0].Remote != "origin" {
		t.Fatalf("repository view = %+v", view.Repos)
	}
	if got := view.Groups; !reflect.DeepEqual(got, []string{"backend", "shared"}) {
		t.Fatalf("groups = %v", got)
	}
	if len(view.Workspaces) != 2 || view.Workspaces[0].Name != "Alpha" || !view.Workspaces[0].Active ||
		view.Workspaces[0].RepoCount != 1 || view.Workspaces[0].Backend != "codex" {
		t.Fatalf("workspace summaries = %+v", view.Workspaces)
	}

	repositoryGroups[0] = "mutated-after-query"
	if view.Repos[0].Groups[0] != "backend" {
		t.Fatalf("projection retained mutable repository groups: %v", view.Repos[0].Groups)
	}
}

func TestWorkspaceTopologyQueryExposesNoMutationAuthority(t *testing.T) {
	queryType := reflect.TypeOf((*operationalview.WorkspaceTopologyQuery)(nil)).Elem()
	for _, method := range []string{"Create", "Update", "Delete", "Rename", "SetLifecycle"} {
		if _, ok := queryType.MethodByName(method); ok {
			t.Fatalf("immutable query exposes mutation method %s", method)
		}
	}
	if got, want := queryType.NumMethod(), 4; got != want {
		t.Fatalf("query methods = %d, want exact immutable surface of %d", got, want)
	}
}

package capabilitycomposition

import (
	"context"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/modules/workspace"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestWorkspaceCatalogResolvesPersistedWorkspace(t *testing.T) {
	st := memstore.New()
	_, err := st.Workspaces().Create(context.Background(), store.WorkspaceCreate{Key: "HELLO", Name: "Hello"})
	if err != nil {
		t.Fatal(err)
	}
	api, err := NewWorkspaceCatalog(st.Workspaces())
	if err != nil {
		t.Fatal(err)
	}
	value, err := api.Resolve(context.Background(), workspace.ResolveQuery{Reference: "Hello"})
	if err != nil || value.Key != "HELLO" {
		t.Fatalf("unexpected resolution value=%#v err=%v", value, err)
	}
}

func TestWorkspaceCatalogListsAndUpdatesPersistedWorkspace(t *testing.T) {
	st := memstore.New()
	_, err := st.Workspaces().Create(context.Background(), store.WorkspaceCreate{Key: "HELLO", Name: "Hello"})
	if err != nil {
		t.Fatal(err)
	}
	api, err := NewWorkspaceCatalog(st.Workspaces())
	if err != nil {
		t.Fatal(err)
	}
	values, err := api.List(context.Background(), workspace.ListQuery{})
	if err != nil || len(values) != 1 || values[0].Key != "HELLO" {
		t.Fatalf("unexpected list: values=%#v err=%v", values, err)
	}
	renamed, err := api.Rename(context.Background(), workspace.RenameCommand{Reference: "HELLO", Name: "Renamed"})
	if err != nil || renamed.Name != "Renamed" {
		t.Fatalf("unexpected rename: value=%#v err=%v", renamed, err)
	}
	formatted, err := api.SetDesignFormat(context.Background(), workspace.SetDesignFormatCommand{Reference: "HELLO", Format: workspace.DesignFormatHTML})
	if err != nil || formatted.DesignFormat != workspace.DesignFormatHTML {
		t.Fatalf("unexpected format update: value=%#v err=%v", formatted, err)
	}
}

func TestWorkspaceCapabilityListsOwnedRepositoryCatalog(t *testing.T) {
	st := memstore.New()
	ctx := context.Background()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "HELLO", Name: "Hello"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Repos().Create(ctx, store.RepoCreate{WorkspaceKey: "HELLO", Name: "loom", Groups: []string{"core"}}); err != nil {
		t.Fatal(err)
	}
	api, err := NewWorkspaceCapability(st.Workspaces(), st.Repos())
	if err != nil {
		t.Fatal(err)
	}
	values, err := api.ListRepositories(ctx, workspace.ListRepositoriesQuery{WorkspaceReference: "Hello"})
	if err != nil || len(values) != 1 || values[0].Name != "loom" || values[0].WorkspaceKey != "HELLO" {
		t.Fatalf("unexpected repositories: values=%#v err=%v", values, err)
	}
}

func TestWorkspaceCapabilityPersistsOwnedWorkspaceAndRepositoryCommands(t *testing.T) {
	st := memstore.New()
	ctx := context.Background()
	api, err := NewWorkspaceCapability(st.Workspaces(), st.Repos())
	if err != nil {
		t.Fatal(err)
	}
	created, err := api.Create(ctx, workspace.CreateCommand{
		Key: "HELLO", Name: "Hello", DefaultBranch: "main",
	})
	if err != nil || created.Key != "HELLO" {
		t.Fatalf("create value=%#v err=%v", created, err)
	}
	branch := "trunk"
	updated, err := api.SetLifecycle(ctx, workspace.SetLifecycleCommand{
		Reference: "Hello", State: workspace.StateReady, DefaultBranch: &branch,
	})
	if err != nil || updated.State != workspace.StateReady || updated.DefaultBranch != "trunk" {
		t.Fatalf("lifecycle value=%#v err=%v", updated, err)
	}
	repository, err := api.RegisterRepository(ctx, workspace.RegisterRepositoryCommand{
		WorkspaceReference: "Hello", Name: "loom", RemoteURL: "https://example.invalid/loom.git",
		Remote: "origin", DefaultBranch: "main", Groups: []string{"core"},
	})
	if err != nil || repository.WorkspaceKey != "HELLO" || repository.Groups[0] != "core" {
		t.Fatalf("register repository value=%#v err=%v", repository, err)
	}
	deleted, err := api.UnregisterRepository(ctx, workspace.UnregisterRepositoryCommand{
		WorkspaceReference: "HELLO", Name: "loom",
	})
	if err != nil || deleted.Name != "loom" {
		t.Fatalf("unregister repository value=%#v err=%v", deleted, err)
	}
	if _, err := st.Repos().Get(ctx, "HELLO", "loom"); err == nil {
		t.Fatal("repository still exists after owner command")
	}
}

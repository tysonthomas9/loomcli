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

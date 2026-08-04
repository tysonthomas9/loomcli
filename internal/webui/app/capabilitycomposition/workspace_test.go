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
